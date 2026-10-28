package main

import (
	"context"

	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ondics/yaml2video/src/render"
	"github.com/ondics/yaml2video/src/server"
	ffmpeg "github.com/u2takey/ffmpeg-go"
)

func main() {
	options, err := parseArgs()
	if errors.Is(err, flag.ErrHelp) {
		printHelp()
		return
	}
	if err != nil {
		printHelp()
		fmt.Fprintf(os.Stderr, "\n\x1b[31m[Error] %v\x1b[0m", err)
		os.Exit(2)
	}

	if options.fonts {
		if err := printFontCatalog(os.Stdout, os.Stderr, render.ListFonts()); err != nil {
			fail(err)
		}
		return
	}

	if options.serve {
		if err := server.Serve(); err != nil {
			fail(err)
		}
		return
	}

	ffmpeg.LogCompiledCommand = false
	plan, err := render.LoadWithTemplateBase(options.videoPath, options.templatePath, options.render.WorkDir)
	if err != nil {
		fail(err)
	}
	applyRenderOptions(plan, options.render)

	if options.dryRun {
		if err := printDryRun(options.videoPath, plan); err != nil {
			fail(err)
		}
		return
	}
	if err := renderCLI(context.Background(), plan, os.Stderr, os.Stdout); err != nil {
		fail(err)
	}
}

func applyRenderOptions(plan *render.Plan, options render.Options) {
	if options.WorkDir != "" {
		plan.WorkDir = options.WorkDir
		for i := range plan.Scenes {
			plan.Scenes[i].ASSPath = filepath.Join(plan.WorkDir, "ass", fmt.Sprintf("scene-%04d.ass", i))
			plan.Scenes[i].Output = filepath.Join(plan.WorkDir, "scenes", fmt.Sprintf("scene-%04d.mkv", i))
		}
	}

	if options.Output != "" {
		plan.Output = options.Output
	}
}

func printHelp() {
	fmt.Printf(`yaml2video - create MP4 videos from YAML content and templates.

Usage:
  yaml2video [options] video.yaml
  yaml2video video.yaml [options]
  yaml2video serve
  yaml2video fonts           List font families (including cached Google Fonts)

Options:
`)
	flags := newFlagSet(&cliOptions{})
	flags.VisitAll(func(f *flag.Flag) {
		if f.Name == "h" {
			return
		}
		name := "-" + f.Name
		if len(f.Name) > 1 {
			name = "--" + f.Name
		}
		if f.Name == "help" {
			name = "-h, --help"
		}
		valueName, usage := flag.UnquoteUsage(f)
		if valueName != "" {
			name += " " + valueName
		}
		fmt.Printf("  %s\n        %s\n", name, usage)
	})
	fmt.Printf(`
Copyright (c) 2026 Ondics GmbH
`)
}

func printDryRun(videoPath string, plan *render.Plan) error {
	logCompiledCommand := ffmpeg.LogCompiledCommand
	ffmpeg.LogCompiledCommand = false
	defer func() { ffmpeg.LogCompiledCommand = logCompiledCommand }()

	// commands, err := plan.Commands()
	// if err != nil {
	// 	return err
	// }
	printSummary(videoPath, plan)
	// fmt.Println("\nFFmpeg commands:")
	// for _, args := range commands {
	// 	fmt.Println(shellArgs(args))
	// }
	return nil
}

func printSummary(videoPath string, plan *render.Plan) {
	frames := int64(plan.Duration.Seconds()*float64(plan.Video.FPS) + 0.5)
	fmt.Printf("Video:      %s\n", videoPath)
	fmt.Printf("Output:     %s\n", plan.Output)
	fmt.Printf("Format:     %dx%d at %d fps\n", plan.Video.Width, plan.Video.Height, plan.Video.FPS)
	fmt.Printf("Timeline:   %s (%d frames)\n", plan.Duration, frames)
	fmt.Printf("Scenes:     %d\n", len(plan.Scenes))
	for i, scene := range plan.Scenes {
		fmt.Printf("  %02d  %-16s %s – %s (%s) layout %s\n", i+1, scene.ID, scene.Start, scene.Start+scene.Duration, scene.Duration, scene.Layout)
		for _, layer := range scene.Layers {
			if layer.Kind == "text" {
				for _, span := range layer.Spans {
					fmt.Printf("       text: %s\n", span.Content)
				}
			} else if layer.Path != "" {
				fmt.Printf("       %s: %s (alt: %q)\n", layer.Kind, layer.Path, layer.AltText)
			}
			for _, effect := range layer.Effects {
				fmt.Printf("       effect: %s\n", effect.Type)
			}
			if layer.Animation != nil {
				fmt.Printf("       keyframes: %s\n", animatedProps(layer.Animation))
			}
			if layer.ShowOnly != nil {
				fmt.Printf("       shown %s – %s (modified %s)\n", scene.Start+layer.ShowOnly.Start, scene.Start+layer.ShowOnly.End, layer.Element)
			}
		}
		for _, audio := range scene.Audio {
			fmt.Printf("       audio: %s at %s (volume %.2f)\n", audio.Path, scene.Start+audio.Offset, audio.Volume)
		}
		printTracks(scene.Tracks, scene.Start)
	}
	if len(plan.Tracks) > 0 || len(plan.Audio) > 0 {
		fmt.Println("Video tracks:")
		printTracks(plan.Tracks, 0)
		for _, audio := range plan.Audio {
			fmt.Printf("       audio: %s at %s (volume %.2f)\n", audio.Path, audio.Offset, audio.Volume)
		}
	}
	if plan.Music == nil {
		fmt.Println("Music:      none")
	} else {
		fmt.Printf("Music:      %s (volume %.2f, fade out %s)\n", plan.Music.Path, plan.Music.Volume, plan.Music.FadeOut)
	}
	fmt.Printf("Work dir:   %s\n", plan.WorkDir)
	fmt.Printf("Transitions: %d\n", len(plan.Transitions))
	for _, tr := range plan.Transitions {
		blend := "cut"
		if tr.Duration > 0 {
			blend = fmt.Sprintf("%s blend", tr.Duration)
		}
		fmt.Printf("  %s → %s at %s: %s (%s", plan.Scenes[tr.FromScene].ID, plan.Scenes[tr.FromScene+1].ID, tr.Offset, tr.Type, blend)
		if len(tr.Effects) > 0 {
			fmt.Printf(", %d effect layer(s)", len(tr.Effects))
		}
		fmt.Println(")")
	}
}

// printTracks lists a scene's custom layout elements and track clips, in
// stacking order, with their absolute times.
func printTracks(tracks []render.TrackPlan, offset time.Duration) {
	for _, track := range tracks {
		fmt.Printf("       track %s:\n", track.ID)
		printClips(track.Clips, offset, "         ")
	}
}

// printClips lists clips with absolute times; folder children follow their
// folder, indented, on the folder's timeline.
func printClips(clips []render.ClipPlan, offset time.Duration, indent string) {
	for _, clip := range clips {
		detail := clip.Layer.Path
		switch clip.Kind {
		case "effect":
			names := make([]string, 0, len(clip.Adjustments))
			for _, a := range clip.Adjustments {
				names = append(names, a.Type)
			}
			detail = strings.Join(names, " + ")
		case "text":
			for _, span := range clip.Layer.Spans {
				detail += strings.ReplaceAll(span.Content, "\n", " ")
			}
			detail = fmt.Sprintf("%q", detail)
		}
		if clip.ID != "" {
			detail = "#" + clip.ID + " " + detail
		}
		fmt.Printf("%s%-6s %s – %s %s", indent, clip.Kind, offset+clip.Start, offset+clip.Start+clip.Duration, detail)
		if clip.Layer.Animation != nil {
			fmt.Printf(" [keyframes: %s]", animatedProps(clip.Layer.Animation))
		}
		if clip.Layer.ShowOnly != nil {
			fmt.Printf(" [shown %s–%s]", offset+clip.Layer.ShowOnly.Start, offset+clip.Layer.ShowOnly.End)
		}
		fmt.Println()
		if clip.Folder != nil {
			printClips(clip.Folder.Clips, offset+clip.Start, indent+"  ")
		}
	}
}

func animatedProps(anim *render.Animation) string {
	names := make([]string, 0, len(anim.Props))
	for name := range anim.Props {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}

func fail(err error) {
	fmt.Fprintf(os.Stderr, "\n\x1b[31m[Error] %v\x1b[0m", err)
	os.Exit(1)
}
