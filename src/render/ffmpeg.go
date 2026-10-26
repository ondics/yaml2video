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
		args := inlineSources(stream.Compile().Args)
		command := append([]string{args[0], "-nostdin", "-progress", "pipe:1", "-nostats"}, args[1:]...)
		commands = append(commands, command)
	}

	return commands, nil
}

// Render writes intermediate scene assets and executes the plan's FFmpeg commands.
func (p *Plan) Render(ctx context.Context) error {
	return p.RenderWithProgress(ctx, nil)
}

// RenderWithProgress renders the plan and calls callback synchronously on the
// calling goroutine. A nil callback is allowed. Callbacks should return promptly
// so progress can be drained; cancellation returns an error wrapping ctx.Err().
func (p *Plan) RenderWithProgress(ctx context.Context, callback func(Progress)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if p.usesASS() {
		if err := requireFilter(ctx, ffmpegFilterASS); err != nil {
			return err
		}
	}
	if err := p.createWorkDirectories(); err != nil {
		return err
	}
	if err := p.writeASS(); err != nil {
		return err
	}
	if err := p.writeFonts(); err != nil {
		return err
	}
	if err := p.measureAudio(ctx); err != nil {
		return err
	}

	commands, err := p.Commands()
	if err != nil {
		return err
	}

	weights := make([]float64, len(commands))
	for i := range weights {
		if i < len(p.Scenes) {
			weights[i] = p.Scenes[i].Duration.Seconds()
		} else {
			weights[i] = p.Duration.Seconds()
		}
	}

	progress := newRenderProgress(weights, callback)
	for i, args := range commands {
		if err := ctx.Err(); err != nil {
			return err
		}
		stage := "scene"
		if i == len(p.Scenes) {
			stage = "final"
		}

		progress.report(stage, i, 0)
		if err := runFFmpeg(ctx, args[1:], func(seconds float64) {
			progress.report(stage, i, seconds)
		}); err != nil {
			return fmt.Errorf("render %s command %d: %w", stage, i+1, err)
		}
		progress.report(stage, i+1, 0)
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	progress.report("done", len(commands), 0)
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
		base = base.Filter(ffmpegFilterASS, nil, plan.assArgs(scene.ASSPath))
	}
	base = applyTracks(base, plan, scene.Tracks)

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
		source := scene
		if layer.ShowOnly != nil {
			// A modified variant decodes the same file as its original; a distinct
			// length keeps ffmpeg-go from merging the two input chains.
			source.Duration += time.Duration(layer.ShowOnly.Start.Microseconds()%997+1) * time.Microsecond
		}
		input := mediaLayerStream(plan, source, layer)
		input, x, y := animateMedia(input, frameSpace(plan.Video), layer, 0)
		args := ffmpeg.KwArgs{"x": x, "y": y}
		if enable := layer.enableExpr(); enable != "" {
			args["enable"] = enable
		}
		return base.Overlay(input, mediaEOFAction(layer), args)
	case "rectangle":
		return drawRectangle(base, layer)
	case "circle":
		circle := circleStream(plan, layer, scene.Duration)
		return base.Overlay(circle, eofActionEndAll, ffmpeg.KwArgs{"x": layer.X, "y": layer.Y})
	case "text":
		if layer.ShowOnly != nil || len(layer.HideDuring) > 0 {
			// Modified elements show their panels only while they are visible.
			return textClipPanel(base, ClipPlan{Duration: scene.Duration, Layer: layer}, false)
		}
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
	if masked(layer) {
		input = maskAlpha(input, shapeMask(layer))
	}

	return input
}

func mediaInput(plan *Plan, scene ScenePlan, layer LayerPlan) *ffmpeg.Stream {
	var input *ffmpeg.Stream
	if layer.Kind == "image" {
		input = ffmpeg.Input(layer.Path, ffmpeg.KwArgs{
			"loop":      1,
			"framerate": plan.Video.FPS,
			"t":         ffTime(scene.Duration),
		})
	} else {
		inputArgs := ffmpeg.KwArgs{}
		if layer.SourceOffset > 0 {
			inputArgs["ss"] = ffTime(layer.SourceOffset)
		}

		input = ffmpeg.Input(layer.Path, inputArgs).
			Video().
			Filter("trim", nil, ffmpeg.KwArgs{
				"duration": ffTime(mediaDuration(layer.Duration, scene.Duration)),
			}).
			Filter("setpts", ffmpeg.Args{"PTS-STARTPTS"})
	}

	if layer.Trim != nil {
		input = trimMediaFrame(input, *layer.Trim)
	}

	return input
}

func trimMediaFrame(input *ffmpeg.Stream, trim TrimPlan) *ffmpeg.Stream {
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
		x, y := "(iw-ow)/2", "(ih-oh)/2"
		if layer.FitX != nil {
			x = fmt.Sprintf("(iw-ow)*%g", *layer.FitX)
		}
		if layer.FitY != nil {
			y = fmt.Sprintf("(ih-oh)*%g", *layer.FitY)
		}
		return input.
			Filter("scale", ffmpeg.Args{width, height, "force_original_aspect_ratio=increase"}).
			Filter("crop", ffmpeg.Args{width, height, x, y})
	}

	padX, padY := containPadding(layer.Anchor)
	if layer.FitX != nil {
		padX = fmt.Sprintf("(ow-iw)*%g", *layer.FitX)
	}
	if layer.FitY != nil {
		padY = fmt.Sprintf("(oh-ih)*%g", *layer.FitY)
	}
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

	cut := false
	for sceneIndex := 1; sceneIndex < len(p.Scenes); sceneIndex++ {
		transition := boundaryFor(p.Transitions, sceneIndex-1)
		nextScene := ffmpeg.Input(p.Scenes[sceneIndex].Output)

		if transition == nil || transition.xfade() == "" {
			visual = ffmpeg.Concat([]*ffmpeg.Stream{visual, nextScene}, ffmpeg.KwArgs{"v": 1, "a": 0})
			cut = true
			continue
		}
		if cut {
			// concat switches to the microsecond timebase; xfade needs both inputs to match.
			nextScene = nextScene.Filter("settb", ffmpeg.Args{"AVTB"})
		}

		args := ffmpeg.KwArgs{
			"transition": transition.xfade(),
			"duration":   ffTime(transition.Duration),
			"offset":     ffTime(p.xfadeOffset(sceneIndex, transition)),
		}
		if transition.Expr != "" {
			args["expr"] = transition.Expr
		}
		visual = ffmpeg.Filter([]*ffmpeg.Stream{visual, nextScene}, "xfade", nil, args)
	}

	for _, transition := range p.Transitions {
		for _, clip := range transition.Effects {
			visual = applyEffectClip(visual, frameSpace(p.Video), clip, false)
		}
	}
	visual = applyTracks(visual, p, p.Tracks)

	streams := []*ffmpeg.Stream{visual}
	if audio := p.buildAudio(); audio != nil {
		streams = append(streams, audio)
	}

	// Graphics-heavy frames (flat color, fine text) band and smear at x264's
	// default quality; a low CRF and adaptive quantization keep them clean.
	crf := p.Video.CRF
	if crf == 0 {
		crf = defaultCRF
	}
	return ffmpeg.Output(streams, p.Output, ffmpeg.KwArgs{
		"vcodec":      "libx264",
		"crf":         crf,
		"preset":      "slow",
		"x264-params": "aq-mode=3",
		"pix_fmt":     "yuv420p",
		"movflags":    "+faststart",
		"shortest":    "",
	}).OverWriteOutput(), nil
}

func (p *Plan) xfadeOffset(sceneIndex int, transition *BoundaryTransition) time.Duration {
	durationBeforeScene := time.Duration(0)

	for index := range sceneIndex {
		durationBeforeScene += p.Scenes[index].Duration

		if index < sceneIndex-1 {
			if previous := boundaryFor(p.Transitions, index); previous != nil && previous.xfade() != "" {
				durationBeforeScene -= previous.Duration
			}
		}
	}

	return durationBeforeScene - transition.Duration
}

// buildAudio is the final audio: the mix through the master stage or, without
// one, a limiter that keeps overlapping tracks from clipping.
func (p *Plan) buildAudio() *ffmpeg.Stream {
	mix := p.mixAudioTracks()
	switch {
	case mix == nil:
		return nil
	case p.Master != nil:
		return p.masterStage(mix)
	default:
		return mix.Filter("alimiter", nil, ffmpeg.KwArgs{"limit": 0.98, "attack": 5, "release": 80, "level": 0})
	}
}

// mixAudioTracks mixes music, foreground audio, and sound effects, before the master stage.
func (p *Plan) mixAudioTracks() *ffmpeg.Stream {
	tracks := make([]*ffmpeg.Stream, 0)
	foregroundTracks := make([]*ffmpeg.Stream, 0)
	// Video-attached clips are placed on a single scene spanning the timeline.
	scenes := p.Scenes
	if len(p.Audio) > 0 {
		scenes = append(append([]ScenePlan{}, scenes...), ScenePlan{Duration: p.Duration, Audio: p.Audio})
	}
	sources := audioSources(scenes)
	for _, scene := range scenes {
		for _, layer := range scene.Audio {
			key := audioInputKey(layer)
			track := audioLayerTrack(scene, layer, sources[key][0])
			sources[key] = sources[key][1:]
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

	// Tracks add up at their own volumes. amix's default normalization divides
	// by the number of inputs still playing, so every sound effect that ended
	// would turn the rest of the mix up.
	return ffmpeg.Filter(tracks, "amix", nil, ffmpeg.KwArgs{
		"inputs": len(tracks), "duration": "longest", "dropout_transition": 0, "normalize": 0,
	}).Filter("atrim", nil, ffmpeg.KwArgs{"duration": ffTime(duration)})
}

// musicSource is the music looped or trimmed to the timeline.
func (p *Plan) musicSource() *ffmpeg.Stream {
	return ffmpeg.Input(p.Music.Path, ffmpeg.KwArgs{"stream_loop": -1}).Audio().
		Filter("atrim", nil, ffmpeg.KwArgs{"duration": ffTime(p.Duration)}).
		Filter("asetpts", ffmpeg.Args{"PTS-STARTPTS"})
}

func (p *Plan) musicTrack() *ffmpeg.Stream {
	music := p.musicSource()
	switch {
	case p.Music.Normalize && p.Music.GainDB != nil:
		music = music.Filter("volume", nil, ffmpeg.KwArgs{"volume": fmt.Sprintf("%.2fdB", *p.Music.GainDB)})
	case p.Music.Normalize:
		music = music.Filter("loudnorm", nil, ffmpeg.KwArgs{"I": musicLoudness, "TP": musicTruePeak, "LRA": 11})
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

func audioInputKey(layer AudioLayerPlan) string {
	return layer.Path + "\x00" + ffTime(layer.SourceOffset)
}

// audioSources opens each distinct audio input once and splits it per use.
// ffmpeg-go merges nodes with identical content, so repeating a sound (the
// same effect in several scenes) would otherwise give one input node several
// unsplit outputs, which FFmpeg's filter graph cannot express.
func audioSources(scenes []ScenePlan) map[string][]*ffmpeg.Stream {
	counts := map[string]int{}
	var order []AudioLayerPlan
	for _, scene := range scenes {
		for _, layer := range scene.Audio {
			key := audioInputKey(layer)
			if counts[key] == 0 {
				order = append(order, layer)
			}
			counts[key]++
		}
	}

	sources := map[string][]*ffmpeg.Stream{}
	for _, layer := range order {
		key := audioInputKey(layer)
		inputArgs := ffmpeg.KwArgs{}
		if layer.SourceOffset > 0 {
			inputArgs["ss"] = ffTime(layer.SourceOffset)
		}
		input := ffmpeg.Input(layer.Path, inputArgs).Audio()
		if counts[key] == 1 {
			sources[key] = []*ffmpeg.Stream{input}
			continue
		}
		split := input.ASplit()
		for i := range counts[key] {
			sources[key] = append(sources[key], split.Get(strconv.Itoa(i)))
		}
	}
	return sources
}

func audioLayerTrack(scene ScenePlan, layer AudioLayerPlan, source *ffmpeg.Stream) *ffmpeg.Stream {
	duration := mediaDuration(layer.Duration, scene.Duration-layer.Offset)
	input := source.
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

	if layer.VolumeAnimation != nil {
		input = input.Filter("volume", nil, ffmpeg.KwArgs{"volume": layer.VolumeAnimation.Expr("volume", "t", 1), "eval": "frame"})
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
	for _, track := range p.allTracks() {
		if len(textRuns(track.Clips)) > 0 {
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
	for _, track := range p.allTracks() {
		// A folder's text is drawn on its canvas, in the canvas's coordinates.
		video, s := p.Video, frameSpace(p.Video)
		if track.Canvas != nil {
			s = space{*track.Canvas, p.Video.FPS}
			video.Width, video.Height = track.Canvas.W, track.Canvas.H
		}
		for run, clips := range textRuns(track.Clips) {
			local := make([]ClipPlan, len(clips))
			for i, clip := range clips {
				clip.Layer = s.local(clip.Layer)
				local[i] = clip
			}
			doc := textRunDocument(video, local)
			if err := os.WriteFile(filepath.Join(p.WorkDir, "ass", runASSName(track, run)), []byte(doc), 0o644); err != nil {
				return fmt.Errorf("write ASS file for track %q: %w", track.ID, err)
			}
		}
	}

	return nil
}

// bundledFonts lists the font files libass must load from the render's fonts
// directory: fonts the text uses that aren't installed.
func (p *Plan) bundledFonts() []string {
	seen := map[string]bool{}
	var files []string
	add := func(l LayerPlan) {
		if l.face != nil && l.face.Bundled && !seen[l.face.Path] {
			seen[l.face.Path] = true
			files = append(files, l.face.Path)
		}
	}
	for _, scene := range p.Scenes {
		for _, l := range scene.Layers {
			add(l)
		}
	}
	for _, track := range p.allTracks() {
		for _, clip := range track.Clips {
			add(clip.Layer)
		}
	}
	return files
}

// assArgs are the arguments of an ass filter drawing filename.
func (p *Plan) assArgs(filename string) ffmpeg.KwArgs {
	args := ffmpeg.KwArgs{"filename": filename}
	if len(p.bundledFonts()) > 0 {
		args["fontsdir"] = filepath.Join(p.WorkDir, "fonts")
	}
	return args
}

// writeFonts copies the bundled fonts into the render's fonts directory.
func (p *Plan) writeFonts() error {
	// Fonts from earlier renders could shadow this one's by name.
	dir := filepath.Join(p.WorkDir, "fonts")
	if err := os.RemoveAll(dir); err != nil {
		return err
	}
	files := p.bundledFonts()
	if len(files) == 0 {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for i, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			return fmt.Errorf("font %s: %w", file, err)
		}
		// Numbered, so fonts from different folders can't collide.
		name := fmt.Sprintf("%02d-%s", i, filepath.Base(file))
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// allTracks lists every track of the plan, including the children of folders.
func (p *Plan) allTracks() []TrackPlan {
	var tracks []TrackPlan
	var add func([]TrackPlan)
	add = func(list []TrackPlan) {
		for _, track := range list {
			tracks = append(tracks, track)
			for _, clip := range track.Clips {
				if clip.Folder != nil {
					add([]TrackPlan{*clip.Folder})
				}
			}
		}
	}
	add(p.Tracks)
	for _, scene := range p.Scenes {
		add(scene.Tracks)
	}
	return tracks
}

func requireFilter(ctx context.Context, name string) error {
	cmd := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-filters")
	stderr := &stderrTail{}
	cmd.Stderr = stderr

	output, err := cmd.Output()
	if ctx.Err() != nil {
		return fmt.Errorf("inspect ffmpeg filters: %w", ctx.Err())
	}
	if err != nil {
		return fmt.Errorf("inspect ffmpeg filters: %w: %s", err, stderr.data)
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
