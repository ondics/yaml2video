package render

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	ffmpeg "github.com/u2takey/ffmpeg-go"
)

const (
	ffmpegFilterASS = "ass"
	eofActionEndAll = "endall"
	eofActionPass   = "pass"
)

// Commands returns the exact FFmpeg argument lists that Render executes.
func (p *Plan) Commands() ([][]string, error) {
	streams, err := p.streams()
	if err != nil {
		return nil, err
	}

	commands := make([][]string, 0, len(streams))
	for _, stream := range streams {
		commands = append(commands, stream.Compile().Args)
	}

	return commands, nil
}

// Render writes intermediate scene assets and executes the plan's FFmpeg commands.
func (p *Plan) Render(ctx context.Context) error {
	if p.usesASS() {
		if err := requireFilter(ffmpegFilterASS); err != nil {
			return err
		}
	}
	if err := p.createWorkDirectories(); err != nil {
		return err
	}
	if err := p.writeASS(); err != nil {
		return err
	}

	streams, err := p.streams()
	if err != nil {
		return err
	}

	for _, stream := range streams {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := stream.ErrorToStdOut().Run(); err != nil {
			return err
		}
	}

	return nil
}

func (p *Plan) createWorkDirectories() error {
	for _, directory := range []string{"scenes", "ass"} {
		if err := os.MkdirAll(filepath.Join(p.WorkDir, directory), 0o755); err != nil {
			return err
		}
	}

	return nil
}

func (p *Plan) streams() ([]*ffmpeg.Stream, error) {
	if err := p.validateRenderPlan(); err != nil {
		return nil, err
	}
	streams := make([]*ffmpeg.Stream, 0, len(p.Scenes)+1)

	for _, scene := range p.Scenes {
		streams = append(streams, buildScene(p, scene))
	}
	if len(p.Scenes) == 0 {
		return streams, nil
	}

	final, err := p.buildFinal()
	if err != nil {
		return nil, err
	}

	return append(streams, final), nil
}

func buildScene(plan *Plan, scene ScenePlan) *ffmpeg.Stream {
	base := sceneBackgroundStream(plan, scene)

	for _, layer := range scene.Layers {
		base = applyVisualLayer(base, plan, scene, layer)
	}
	if scene.ASS != "" {
		base = base.Filter(ffmpegFilterASS, nil, ffmpeg.KwArgs{"filename": scene.ASSPath})
	}

	return base.
		Output(scene.Output, ffmpeg.KwArgs{
			"t":       ffTime(scene.Duration),
			"an":      "",
			"vcodec":  "ffv1",
			"pix_fmt": "yuv420p",
		}).
		OverWriteOutput()
}

func sceneBackgroundStream(plan *Plan, scene ScenePlan) *ffmpeg.Stream {
	input := fmt.Sprintf(
		"color=c=%s:s=%dx%d:r=%d:d=%s",
		scene.Background,
		plan.Video.Width,
		plan.Video.Height,
		plan.Video.FPS,
		ffTime(scene.Duration),
	)

	return ffmpeg.Input(input, ffmpeg.KwArgs{"f": "lavfi"})
}

func applyVisualLayer(base *ffmpeg.Stream, plan *Plan, scene ScenePlan, layer LayerPlan) *ffmpeg.Stream {
	switch layer.Kind {
	case "image", "video":
		input := mediaLayerStream(plan, scene, layer)
		return base.Overlay(input, mediaEOFAction(layer), ffmpeg.KwArgs{"x": layer.X, "y": layer.Y})
	case "rectangle":
		return drawRectangle(base, layer)
	case "circle":
		circle := circleStream(plan, layer, scene.Duration)
		return base.Overlay(circle, eofActionEndAll, ffmpeg.KwArgs{"x": layer.X, "y": layer.Y})
	case "text":
		return drawTextBackgrounds(base, layer)
	default:
		return base
	}
}

func mediaLayerStream(plan *Plan, scene ScenePlan, layer LayerPlan) *ffmpeg.Stream {
	input := mediaInput(plan, scene, layer)
	input = fitLayerStream(input, layer)
	input = applyMediaEffects(input, plan, scene, layer)

	if layer.Opacity != 1 {
		input = input.
			Filter("format", ffmpeg.Args{"rgba"}).
			ColorChannelMixer(ffmpeg.KwArgs{"aa": layer.Opacity})
	}
	if layer.Shape == "circle" || layer.Shape == "rounded-rectangle" {
		input = maskAlpha(input, shapeMask(layer))
	}

	return input
}

func mediaInput(plan *Plan, scene ScenePlan, layer LayerPlan) *ffmpeg.Stream {
	if layer.Kind == "image" {
		return ffmpeg.Input(layer.Path, ffmpeg.KwArgs{
			"loop":      1,
			"framerate": plan.Video.FPS,
			"t":         ffTime(scene.Duration),
		})
	}

	inputArgs := ffmpeg.KwArgs{}
	if layer.SourceOffset > 0 {
		inputArgs["ss"] = ffTime(layer.SourceOffset)
	}

	input := ffmpeg.Input(layer.Path, inputArgs).
		Video().
		Filter("trim", nil, ffmpeg.KwArgs{
			"duration": ffTime(mediaDuration(layer.Duration, scene.Duration)),
		}).
		Filter("setpts", ffmpeg.Args{"PTS-STARTPTS"})

	if layer.Trim != nil {
		input = trimVideoFrame(input, *layer.Trim)
	}

	return input
}

func trimVideoFrame(input *ffmpeg.Stream, trim TrimPlan) *ffmpeg.Stream {
	return input.Filter("crop", ffmpeg.Args{
		fmt.Sprintf("iw*(1-%g-%g)", trim.Left, trim.Right),
		fmt.Sprintf("ih*(1-%g-%g)", trim.Top, trim.Bottom),
		fmt.Sprintf("iw*%g", trim.Left),
		fmt.Sprintf("ih*%g", trim.Top),
	})
}

func fitLayerStream(input *ffmpeg.Stream, layer LayerPlan) *ffmpeg.Stream {
	width := strconv.Itoa(layer.Width)
	height := strconv.Itoa(layer.Height)

	if layer.Fit == "cover" {
		return input.
			Filter("scale", ffmpeg.Args{width, height, "force_original_aspect_ratio=increase"}).
			Filter("crop", ffmpeg.Args{width, height})
	}

	padX, padY := containPadding(layer.Anchor)
	return input.
		Filter("scale", ffmpeg.Args{width, height, "force_original_aspect_ratio=decrease:force_divisible_by=2"}).
		Filter("format", ffmpeg.Args{"rgba"}).
		Filter("pad", ffmpeg.Args{width, height, padX, padY, "color=black@0"})
}

func containPadding(anchor string) (string, string) {
	x, y := "(ow-iw)/2", "(oh-ih)/2"
	if strings.HasSuffix(anchor, "-left") {
		x = "0"
	} else if strings.HasSuffix(anchor, "-right") {
		x = "ow-iw"
	}
	if strings.HasPrefix(anchor, "top-") {
		y = "0"
	} else if strings.HasPrefix(anchor, "bottom-") {
		y = "oh-ih"
	}
	return x, y
}

func mediaEOFAction(layer LayerPlan) string {
	if layer.Kind == "video" {
		return eofActionPass
	}

	return eofActionEndAll
}

func drawTextBackgrounds(base *ffmpeg.Stream, layer LayerPlan) *ffmpeg.Stream {
	backgroundPlan := layer.Background
	glassBlur := 0.0
	for _, effect := range layer.Effects {
		if effect.Type == "glass_panel" {
			glassBlur = effect.Blur
			color := effect.Color
			if color == "" {
				color = "0xFFFFFF"
			}
			if after, ok := strings.CutPrefix(color, "#"); ok {
				color = "0x" + after
			}
			backgroundPlan = &TextBackgroundPlan{Color: color, Opacity: effect.Opacity, PaddingX: int(effect.PaddingX), PaddingY: int(effect.PaddingY)}
		}
	}

	if backgroundPlan != nil {
		background := *backgroundPlan
		x, y := layer.X-background.PaddingX, layer.Y-background.PaddingY
		width, height := layer.Width+2*background.PaddingX, layer.Height+2*background.PaddingY
		if glassBlur > 0 {
			base = blurPanel(base, x, y, width, height, glassBlur)
		}
		base = drawTextBackground(base, x, y, width, height, background, layer.Opacity)
	}

	for _, box := range layer.ItemBackgrounds {
		base = drawTextBackground(base, box.X, box.Y, box.Width, box.Height, box.Background, layer.Opacity)
	}

	return base
}

func drawTextBackground(
	base *ffmpeg.Stream,
	x, y, width, height int,
	background TextBackgroundPlan,
	layerOpacity float64,
) *ffmpeg.Stream {
	return base.Filter("drawbox", nil, ffmpeg.KwArgs{
		"x":     x,
		"y":     y,
		"w":     width,
		"h":     height,
		"color": withOpacity(background.Color, background.Opacity*layerOpacity),
		"t":     "fill",
	})
}

func drawRectangle(base *ffmpeg.Stream, layer LayerPlan) *ffmpeg.Stream {
	// drawbox requires the literal value "fill" for a filled rectangle.
	return base.Filter("drawbox", nil, ffmpeg.KwArgs{
		"x":     layer.X,
		"y":     layer.Y,
		"w":     layer.Width,
		"h":     layer.Height,
		"color": withOpacity(layer.Color, layer.Opacity),
		"t":     "fill",
	})
}

func (p *Plan) buildFinal() (*ffmpeg.Stream, error) {
	visual := ffmpeg.Input(p.Scenes[0].Output)

	for sceneIndex := 1; sceneIndex < len(p.Scenes); sceneIndex++ {
		transition := boundaryFor(p.Transitions, sceneIndex-1)
		nextScene := ffmpeg.Input(p.Scenes[sceneIndex].Output)

		if transition == nil || transition.Type == "cut" {
			visual = ffmpeg.Concat([]*ffmpeg.Stream{visual, nextScene}, ffmpeg.KwArgs{"v": 1, "a": 0})
			continue
		}

		visual = ffmpeg.Filter(
			[]*ffmpeg.Stream{visual, nextScene},
			"xfade",
			nil,
			ffmpeg.KwArgs{
				"transition": xfadeType(transition.Type),
				"duration":   ffTime(transition.Duration),
				"offset":     ffTime(p.xfadeOffset(sceneIndex, transition)),
			},
		)
	}

	streams := []*ffmpeg.Stream{visual}
	if audio := p.buildAudio(); audio != nil {
		streams = append(streams, audio)
	}

	return ffmpeg.Output(streams, p.Output, ffmpeg.KwArgs{
		"vcodec":   "libx264",
		"pix_fmt":  "yuv420p",
		"movflags": "+faststart",
		"shortest": "",
	}).OverWriteOutput(), nil
}

func (p *Plan) xfadeOffset(sceneIndex int, transition *BoundaryTransition) time.Duration {
	durationBeforeScene := time.Duration(0)

	for index := 0; index < sceneIndex; index++ {
		durationBeforeScene += p.Scenes[index].Duration

		if index < sceneIndex-1 {
			if previous := boundaryFor(p.Transitions, index); previous != nil && previous.Type != "cut" {
				durationBeforeScene -= previous.Duration
			}
		}
	}

	return durationBeforeScene - transition.Duration
}

func (p *Plan) buildAudio() *ffmpeg.Stream {
	tracks := make([]*ffmpeg.Stream, 0)
	foregroundTracks := make([]*ffmpeg.Stream, 0)
	for _, scene := range p.Scenes {
		for _, layer := range scene.Audio {
			track := audioLayerTrack(scene, layer)
			if layer.SoundEffect {
				tracks = append(tracks, track)
			} else {
				foregroundTracks = append(foregroundTracks, track)
			}
		}
	}

	if p.Music != nil {
		music := p.musicTrack()
		if d := p.Music.Ducking; d != nil && d.Enabled && len(foregroundTracks) > 0 {
			foreground := mixAudio(foregroundTracks, p.Duration).ASplit()
			music = ffmpeg.Filter([]*ffmpeg.Stream{music, foreground.Get("1").Filter("apad", nil, ffmpeg.KwArgs{"whole_dur": ffTime(p.Duration)})}, "sidechaincompress", nil, ffmpeg.KwArgs{
				"threshold": 0.1, "ratio": 1 + d.Amount*19,
				"attack": float64(d.Attack) / float64(time.Millisecond), "release": float64(d.Release) / float64(time.Millisecond),
			})

			// The sidechain also feeds the audible mix; split it before compression.
			tracks = append(tracks, foreground.Get("0"), music)
		} else {
			tracks = append(tracks, foregroundTracks...)
			tracks = append(tracks, music)
		}
	} else {
		tracks = append(tracks, foregroundTracks...)
	}

	switch len(tracks) {
	case 0:
		return nil
	case 1:
		return tracks[0]
	default:
		return mixAudio(tracks, p.Duration)
	}
}

func mixAudio(tracks []*ffmpeg.Stream, duration time.Duration) *ffmpeg.Stream {
	if len(tracks) == 1 {
		return tracks[0]
	}

	return ffmpeg.Filter(tracks, "amix", nil, ffmpeg.KwArgs{
		"inputs": len(tracks), "duration": "longest", "dropout_transition": 0,
	}).Filter("atrim", nil, ffmpeg.KwArgs{"duration": ffTime(duration)})
}

func (p *Plan) musicTrack() *ffmpeg.Stream {
	music := ffmpeg.Input(p.Music.Path, ffmpeg.KwArgs{"stream_loop": -1}).Audio().
		Filter("atrim", nil, ffmpeg.KwArgs{"duration": ffTime(p.Duration)}).
		Filter("asetpts", ffmpeg.Args{"PTS-STARTPTS"})
	if p.Music.Normalize {
		music = music.Filter("loudnorm", nil, ffmpeg.KwArgs{"I": -16, "TP": -1.5, "LRA": 11})
	}

	music = music.Filter("volume", nil, ffmpeg.KwArgs{"volume": p.Music.Volume})
	if p.Music.FadeIn > 0 {
		music = music.Filter("afade", nil, ffmpeg.KwArgs{"t": "in", "st": 0, "d": ffTime(p.Music.FadeIn)})
	}

	if p.Music.FadeOut > 0 {
		music = music.Filter("afade", nil, ffmpeg.KwArgs{
			"t":  "out",
			"st": ffTime(p.Duration - p.Music.FadeOut),
			"d":  ffTime(p.Music.FadeOut),
		})
	}

	return music
}

func audioLayerTrack(scene ScenePlan, layer AudioLayerPlan) *ffmpeg.Stream {
	inputArgs := ffmpeg.KwArgs{}
	if layer.SourceOffset > 0 {
		inputArgs["ss"] = ffTime(layer.SourceOffset)
	}

	duration := mediaDuration(layer.Duration, scene.Duration-layer.Offset)
	input := ffmpeg.Input(layer.Path, inputArgs).Audio().
		Filter("atrim", nil, ffmpeg.KwArgs{"duration": ffTime(duration)}).
		Filter("asetpts", ffmpeg.Args{"PTS-STARTPTS"})

	if layer.FadeIn > 0 {
		input = input.Filter("afade", nil, ffmpeg.KwArgs{"t": "in", "st": 0, "d": ffTime(layer.FadeIn)})
	}

	if layer.FadeOut > 0 {
		input = input.Filter("afade", nil, ffmpeg.KwArgs{
			"t": "out", "st": ffTime(duration - layer.FadeOut), "d": ffTime(layer.FadeOut),
		})
	}

	return input.Filter("adelay", nil, ffmpeg.KwArgs{"delays": (scene.Start + layer.Offset).Milliseconds(), "all": 1}).
		Filter("volume", nil, ffmpeg.KwArgs{"volume": layer.Volume})
}

func mediaDuration(duration, sceneDuration time.Duration) time.Duration {
	if duration > 0 {
		return duration
	}

	return sceneDuration
}

func circleStream(plan *Plan, layer LayerPlan, duration time.Duration) *ffmpeg.Stream {
	color := withOpacity(layer.Color, layer.Opacity)
	rgb, alphaText, _ := strings.Cut(color, "@")
	alpha, _ := strconv.ParseFloat(alphaText, 64)

	input := fmt.Sprintf(
		"color=c=%s:s=%dx%d:r=%d:d=%s",
		rgb,
		layer.Width,
		layer.Height,
		plan.Video.FPS,
		ffTime(duration),
	)
	alphaExpression := fmt.Sprintf(
		"if(lte((X-W/2)*(X-W/2)+(Y-H/2)*(Y-H/2),(min(W,H)/2)*(min(W,H)/2)),%d,0)",
		int(alpha*255+0.5),
	)

	return ffmpeg.Input(input, ffmpeg.KwArgs{"f": "lavfi"}).
		Filter("format", ffmpeg.Args{"rgba"}).
		Filter("geq", nil, ffmpeg.KwArgs{
			"r": "r(X,Y)",
			"g": "g(X,Y)",
			"b": "b(X,Y)",
			"a": alphaExpression,
		})
}

func (p *Plan) usesASS() bool {
	for _, scene := range p.Scenes {
		if scene.ASS != "" {
			return true
		}
	}

	return false
}

func (p *Plan) writeASS() error {
	for _, scene := range p.Scenes {
		if scene.ASS == "" {
			continue
		}
		if err := os.WriteFile(scene.ASSPath, []byte(scene.ASS), 0o644); err != nil {
			return fmt.Errorf("write ASS file for scene %q: %w", scene.ID, err)
		}
	}

	return nil
}

func requireFilter(name string) error {
	output, err := exec.Command("ffmpeg", "-hide_banner", "-filters").Output()
	if err != nil {
		return fmt.Errorf("inspect ffmpeg filters: %w", err)
	}
	if !strings.Contains(string(output), " "+name+" ") {
		return fmt.Errorf("the installed ffmpeg does not provide the required %q filter; install an ffmpeg build with libass enabled", name)
	}

	return nil
}

func boundaryFor(transitions []BoundaryTransition, scene int) *BoundaryTransition {
	for index := range transitions {
		if transitions[index].FromScene == scene {
			return &transitions[index]
		}
	}

	return nil
}

func ffTime(duration time.Duration) string {
	return strconv.FormatFloat(duration.Seconds(), 'f', 6, 64)
}

func withOpacity(color string, opacity float64) string {
	if before, after, ok := strings.Cut(color, "@"); ok {
		alpha, err := strconv.ParseFloat(after, 64)
		if err == nil {
			return before + "@" + strconv.FormatFloat(alpha*opacity, 'f', -1, 64)
		}
	}

	return color + "@" + strconv.FormatFloat(opacity, 'f', -1, 64)
}
