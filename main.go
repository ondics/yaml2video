package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ondics/yaml2video/render"
	v2 "github.com/ondics/yaml2video/v2"
	ffmpeg "github.com/u2takey/ffmpeg-go"
)

func main() {
	videoPath, options := parseArgs()
	plan, err := v2.Load(videoPath, options.templatePath)
	if err != nil {
		fail(err)
	}
	if options.render.WorkDir != "" {
		plan.WorkDir = options.render.WorkDir
		for i := range plan.Scenes {
			plan.Scenes[i].ASSPath = filepath.Join(plan.WorkDir, "ass", fmt.Sprintf("scene-%04d.ass", i))
			plan.Scenes[i].Output = filepath.Join(plan.WorkDir, "scenes", fmt.Sprintf("scene-%04d.mkv", i))
		}
	}
	if options.render.Output != "" {
		plan.Output = options.render.Output
	}
	if options.dryRun {
		printSummary(videoPath, plan)
		ffmpeg.LogCompiledCommand = false
		commands, err := plan.Commands()
		if err != nil {
			fail(err)
		}
		fmt.Println("\nFFmpeg commands:")
		for _, args := range commands {
			fmt.Println(shellArgs(args))
		}
		return
	}

	if err := plan.Render(context.Background()); err != nil {
		fail(err)
	}
}

type cliOptions struct {
	render       render.Options
	templatePath string
	dryRun       bool
}

func parseArgs() (string, cliOptions) {
	args := os.Args[1:]
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}

	var videoPath string
	flagArgs := args
	if !strings.HasPrefix(args[0], "-") {
		videoPath = args[0]
		flagArgs = args[1:]
	}

	flags := flag.NewFlagSet("yaml2video", flag.ExitOnError)
	dryRun := flags.Bool("n", false, "validate and print render details/FFmpeg commands without rendering")
	workDir := flags.String("work-dir", "", "directory for intermediate render files")
	output := flags.String("o", "", "final output file")
	templatePath := flags.String("t", "", "v2 template YAML document (required)")
	_ = flags.Parse(flagArgs)

	if videoPath == "" {
		if flags.NArg() != 1 {
			usage()
			os.Exit(2)
		}
		videoPath = flags.Arg(0)
	} else if flags.NArg() != 0 {
		usage()
		os.Exit(2)
	}
	if *templatePath == "" {
		usage()
		os.Exit(2)
	}
	return videoPath, cliOptions{
		render:       render.Options{WorkDir: *workDir, Output: *output},
		templatePath: *templatePath,
		dryRun:       *dryRun,
	}
}

func printSummary(projectPath string, plan *render.Plan) {
	frames := int64(plan.Duration.Seconds()*float64(plan.Video.FPS) + 0.5)
	fmt.Printf("Video:      %s\n", projectPath)
	fmt.Printf("Output:     %s\n", plan.Output)
	fmt.Printf("Video:      %dx%d at %d fps\n", plan.Video.Width, plan.Video.Height, plan.Video.FPS)
	fmt.Printf("Timeline:   %s (%d frames)\n", plan.Duration, frames)
	fmt.Printf("Scenes:     %d\n", len(plan.Scenes))
	for i, scene := range plan.Scenes {
		fmt.Printf("  %02d  %-16s %s – %s (%s)\n", i+1, scene.ID, scene.Start, scene.Start+scene.Duration, scene.Duration)
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
		}
		for _, audio := range scene.Audio {
			fmt.Printf("       audio: %s at %s (volume %.2f)\n", audio.Path, scene.Start+audio.Offset, audio.Volume)
		}
	}
	if plan.Music == nil {
		fmt.Println("Music:      none")
	} else {
		fmt.Printf("Music:      %s (volume %.2f, fade out %s)\n", plan.Music.Path, plan.Music.Volume, plan.Music.FadeOut)
	}
	fmt.Printf("Work dir:   %s\n", plan.WorkDir)
	fmt.Printf("Transitions: %d\n", len(plan.Transitions))
}

func shellArgs(args []string) string {
	result := ""
	for i, arg := range args {
		if i != 0 {
			result += " "
		}
		result += fmt.Sprintf("%q", arg)
	}
	return result
}

func fail(err error) { fmt.Fprintln(os.Stderr, "yaml2video:", err); os.Exit(1) }
func usage() {
	fmt.Fprintln(os.Stderr, "usage: yaml2video [-n] -t template.yaml [-work-dir dir] [-o output.mp4] video.yaml")
	fmt.Fprintln(os.Stderr, "       yaml2video video.yaml [-n] -t template.yaml [-work-dir dir] [-o output.mp4]")
}
