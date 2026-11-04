package render

import (
	"context"
	"fmt"
	"maps"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	ffmpeg "github.com/u2takey/ffmpeg-go"
)

const (
	ffmpegFilterASS = "ass"
	// Overlays pass the main stream on when the overlaid one ends. ("endall"
	// ends them together, but drops the last frame when both end at once.)
	eofActionPass = "pass"
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
	if err := p.writePieces(); err != nil {
		return err
	}

	commands, err := p.Commands()
	if err != nil {
		return err
	}

	// The loudness analysis only feeds the audio of the last command, so it
	// runs while the scenes render. It sets the plan's gains: the plan is
	// not read again until it ends, and it always ends before this returns.
	measureCtx, cancelMeasure := context.WithCancel(ctx)
	measured := make(chan error, 1)
	go func() { measured <- p.measureAudio(measureCtx) }()
	waited := false
	waitMeasure := func() error {
		waited = true
		return <-measured
	}
	defer func() {
		cancelMeasure()
		if !waited {
			<-measured
		}
	}()

	// Scenes, then transitions, run several at once; the last command
	// assembles the output (or, unsegmented, composes and encodes it).
	scenes := min(len(p.Scenes), len(commands))
	var blends []float64
	seg, segmented := p.segments()
	if segmented {
		for _, blend := range seg.blends {
			if blend > 0 {
				blends = append(blends, float64(blend)/float64(p.Video.FPS))
			}
		}
	}
	transitions := scenes + len(blends)
	weights := make([]float64, len(commands))
	for i := range weights {
		switch {
		case i < scenes:
			weights[i] = p.Scenes[i].Duration.Seconds()
		case i < transitions:
			weights[i] = blends[i-scenes]
		case segmented:
			weights[i] = p.Duration.Seconds() / 20 // copies the video, encodes the audio
		default:
			weights[i] = p.Duration.Seconds()
		}
	}

	progress := newRenderProgress(weights, callback)
	if err := renderParallel(ctx, "scene", commands, 0, scenes, progress); err != nil {
		return err
	}
	if err := renderParallel(ctx, "final", commands, scenes, transitions, progress); err != nil {
		return err
	}
	if err := waitMeasure(); err != nil {
		return err
	}
	// The last command again, with the measured gains.
	if commands, err = p.Commands(); err != nil {
		return err
	}
	for i := transitions; i < len(commands); i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		progress.report("final", i, 0, false)
		if err := runFFmpeg(ctx, commands[i][1:], func(seconds float64) {
			progress.report("final", i, seconds, false)
		}); err != nil {
			return fmt.Errorf("render final command %d: %w", i+1, err)
		}
		progress.report("final", i, 0, true)
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	progress.report("done", len(commands), 0, false)
	return nil
}

// SceneJobsEnv sets how many FFmpeg processes render scenes (and then
// transitions) at once.
const SceneJobsEnv = "YAML2VIDEO_SCENE_JOBS"

// SceneJobs is how many scenes render at once: SceneJobsEnv, or one per CPU
// core up to 4 (DefaultSceneJobs). Scenes are independent, and a single
// FFmpeg process leaves cores idle (its filter graph runs on one thread, with
// only some filters spreading work); each process holds its own frames in
// memory. A value that is not a positive whole number is an error, and the
// default applies.
func SceneJobs() (int, error) {
	value := strings.TrimSpace(os.Getenv(SceneJobsEnv))
	if value == "" {
		return DefaultSceneJobs(), nil
	}
	jobs, err := strconv.Atoi(value)
	if err != nil || jobs < 1 {
		return DefaultSceneJobs(), fmt.Errorf("%s=%q is not a positive whole number; using %d", SceneJobsEnv, value, DefaultSceneJobs())
	}
	return jobs, nil
}

// DefaultSceneJobs is one scene job per CPU core, up to 4.
func DefaultSceneJobs() int {
	return min(runtime.NumCPU(), 4)
}

// renderParallel runs commands [from, to), SceneJobs at a time, and reports
// their progress on the calling goroutine. The first failure stops the rest.
func renderParallel(ctx context.Context, stage string, commands [][]string, from, to int, progress *renderProgress) error {
	if from >= to {
		return ctx.Err()
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type event struct {
		command  int
		seconds  float64
		finished bool
		err      error
	}
	events := make(chan event)
	queue := make(chan int)
	go func() {
		defer close(queue)
		for i := from; i < to; i++ {
			select {
			case queue <- i:
			case <-ctx.Done():
				return
			}
		}
	}()
	var workers sync.WaitGroup
	jobs, _ := SceneJobs()
	for range min(jobs, to-from) {
		workers.Go(func() {
			for i := range queue {
				events <- event{command: i}
				err := runFFmpeg(ctx, commands[i][1:], func(seconds float64) {
					events <- event{command: i, seconds: seconds}
				})
				events <- event{command: i, finished: err == nil, err: err}
			}
		})
	}
	go func() {
		workers.Wait()
		close(events)
	}()

	var failure error
	for e := range events {
		if e.err != nil {
			if failure == nil {
				failure = fmt.Errorf("render %s command %d: %w", stage, e.command+1, e.err)
				cancel()
			}
			continue
		}
		if failure == nil {
			progress.report(stage, e.command, e.seconds, e.finished)
		}
	}
	if failure != nil {
		return failure
	}
	return ctx.Err()
}

func (p *Plan) createWorkDirectories() error {
	for _, directory := range []string{"scenes", "ass"} {
		if err := os.MkdirAll(filepath.Join(p.WorkDir, directory), 0o755); err != nil {
			return err
		}
	}

	return nil
}

// streams returns the render's commands: one per scene, then (for a
// segmented render) one per blended transition, then the one that assembles
// the output.
func (p *Plan) streams() ([]*ffmpeg.Stream, error) {
	if err := p.validateRenderPlan(); err != nil {
		return nil, err
	}
	streams := make([]*ffmpeg.Stream, 0, len(p.Scenes)+1)
	if len(p.Scenes) == 0 {
		return streams, nil
	}

	segments, ok := p.segments()
	if !ok {
		for _, scene := range p.Scenes {
			streams = append(streams, buildScene(p, scene, 0, nil))
		}
		final, err := p.buildFinal()
		if err != nil {
			return nil, err
		}
		return append(streams, final), nil
	}

	for i, scene := range p.Scenes {
		streams = append(streams, buildScene(p, scene, i, &segments))
	}
	for i, blend := range segments.blends {
		if blend > 0 {
			streams = append(streams, p.buildTransition(i, segments))
		}
	}
	return append(streams, p.buildAssembly(segments)), nil
}

// A segmented render encodes the output in pieces and joins them without
// encoding again: each scene command encodes the scene's own frames, each
// transition command blends the frames two scenes share, and the assembly
// concatenates the pieces and adds the audio. Before, every scene was saved
// losslessly and the whole video decoded, blended, and encoded in one more
// pass.
//
// segments places the scenes on the output's frames: scene i's first frame is
// output frame starts[i], and its last blends[i] frames blend into scene i+1
// (0 for a cut). Scene i's own frames are output frames
// [starts[i]+blends[i-1], starts[i+1]).
type segments struct {
	starts, blends []int
	total          int
}

// segments returns where the scenes lie on the output's frames, or false if a
// scene is too short for the blends at its ends; the render then composes the
// output in one pass (buildFinal).
func (p *Plan) segments() (segments, bool) {
	fps := float64(p.Video.FPS)
	n := len(p.Scenes)
	seg := segments{starts: make([]int, n), blends: make([]int, n)}
	start := time.Duration(0)
	for i, scene := range p.Scenes {
		seg.starts[i] = int(math.Round(start.Seconds() * fps))
		start += scene.Duration
		if t := boundaryFor(p.Transitions, i); i < n-1 && t != nil && t.xfade() != "" {
			seg.blends[i] = int(math.Round(t.Duration.Seconds() * fps))
			if seg.blends[i] < 1 {
				return seg, false
			}
			start -= t.Duration
		}
	}
	seg.total = int(math.Round(start.Seconds() * fps))
	for i := range p.Scenes {
		if seg.head(i) > seg.end(i)-seg.starts[i] {
			return seg, false
		}
	}
	return seg, true
}

// head is how many of scene i's first frames blend with scene i-1.
func (seg segments) head(i int) int {
	if i == 0 {
		return 0
	}
	return seg.blends[i-1]
}

// end is the output frame after scene i's own frames: where scene i+1 starts.
func (seg segments) end(i int) int {
	if i+1 < len(seg.starts) {
		return seg.starts[i+1]
	}
	return seg.total
}

// piece is an encoded piece of the output and its length in frames.
type piece struct {
	file   string
	frames int
}

// pieces returns the encoded pieces of the output, in order.
func (p *Plan) pieces(seg segments) []piece {
	var pieces []piece
	for i, scene := range p.Scenes {
		if body := seg.end(i) - seg.starts[i] - seg.head(i); body > 0 {
			pieces = append(pieces, piece{scenePart(scene, "body"), body})
		}
		if seg.blends[i] > 0 {
			pieces = append(pieces, piece{p.transitionFile(i), seg.blends[i]})
		}
	}
	return pieces
}

// scenePart is the file of part of a scene: its blended head or tail, kept
// losslessly for the transitions, or its encoded body.
func scenePart(scene ScenePlan, part string) string {
	base := strings.TrimSuffix(scene.Output, filepath.Ext(scene.Output)) + "-" + part
	if part == "body" {
		return base + ".ts"
	}
	return base + ".mkv"
}

func (p *Plan) transitionFile(i int) string {
	return filepath.Join(p.WorkDir, "scenes", fmt.Sprintf("transition-%04d.ts", i))
}

func (p *Plan) piecesList() string {
	return filepath.Join(p.WorkDir, "scenes", "pieces.txt")
}

// writePieces writes the concat list of a segmented render's pieces. Each
// piece's exact duration is given, so joining them does not depend on how the
// concat demuxer estimates an MPEG-TS file's length from its timestamps.
func (p *Plan) writePieces() error {
	seg, ok := p.segments()
	if !ok || len(p.Scenes) == 0 {
		return nil
	}
	var list strings.Builder
	for _, piece := range p.pieces(seg) {
		abs, err := filepath.Abs(piece.file)
		if err != nil {
			return err
		}
		fmt.Fprintf(&list, "file '%s'\nduration %s\n", strings.ReplaceAll(abs, "'", `'\''`), num6(float64(piece.frames)/float64(p.Video.FPS)))
	}
	return os.WriteFile(p.piecesList(), []byte(list.String()), 0o644)
}

// finalVideoArgs are the output's video encoding settings.
func (p *Plan) finalVideoArgs() ffmpeg.KwArgs {
	// Graphics-heavy frames (flat color, fine text) band and smear at x264's
	// default quality; a low CRF and adaptive quantization keep them clean.
	crf := p.Video.CRF
	if crf == 0 {
		crf = defaultCRF
	}
	preset := p.Video.Preset
	if preset == "" {
		preset = defaultPreset
	}
	return ffmpeg.KwArgs{
		"vcodec":      "libx264",
		"crf":         crf,
		"preset":      preset,
		"x264-params": "aq-mode=3",
		"pix_fmt":     "yuv420p",
	}
}

// losslessArgs encode the frames transitions blend. x264's lossless mode
// encodes and decodes in about half the CPU time of ffv1 at the same size.
var losslessArgs = ffmpeg.KwArgs{"an": "", "vcodec": "libx264", "qp": 0, "preset": "ultrafast", "pix_fmt": "yuv420p"}

// videoRange converts frames to limited-range yuv420p and tags them with the
// BT.601 matrix that converted their RGB sources (images, colors, subtitles).
// Filters tag frames inconsistently (full range, or the RGB "gbr" matrix
// after an RGB step), and pieces joined without encoding again must agree.
func videoRange(s *ffmpeg.Stream) *ffmpeg.Stream {
	return s.Filter("scale", nil, ffmpeg.KwArgs{"out_range": "tv"}).
		Filter("format", ffmpeg.Args{"yuv420p"}).
		Filter("setparams", nil, ffmpeg.KwArgs{"range": "tv", "colorspace": "smpte170m"})
}

// frames selects frames [from, to) of a stream and numbers them from first
// on the output's timeline.
func frames(s *ffmpeg.Stream, fps, from, to, first int) *ffmpeg.Stream {
	return s.Filter("trim", nil, ffmpeg.KwArgs{"start_frame": from, "end_frame": to}).
		Filter("setpts", ffmpeg.Args{fmt.Sprintf("(N+%d)/%d/TB", first, fps)})
}

// encodePiece encodes output frames [first, first+count) as a piece, after
// what lies on the output's own timeline.
func (p *Plan) encodePiece(s *ffmpeg.Stream, first, count int, file string) *ffmpeg.Stream {
	args := p.finalVideoArgs()
	args["an"] = ""
	return videoRange(p.timeline(s, first, count)).
		Filter("setpts", ffmpeg.Args{fmt.Sprintf("N/%d/TB", p.Video.FPS)}).
		Output(file, args)
}

// timeline applies what lies on the output's own timeline (the transitions'
// effect layers, then the video's tracks) to output frames [first,
// first+count) that carry the output's timestamps.
func (p *Plan) timeline(s *ffmpeg.Stream, first, count int) *ffmpeg.Stream {
	space := frameSpace(p.Video)
	space.first, space.frames = first, count
	ordinal := 0
	for _, transition := range p.Transitions {
		s = composite(s, p, []TrackPlan{{Clips: transition.Effects}}, space, false, &ordinal)
	}
	return composite(s, p, p.Tracks, space, false, &ordinal)
}

// buildTransition blends the frames scenes i and i+1 share.
func (p *Plan) buildTransition(i int, seg segments) *ffmpeg.Stream {
	transition := boundaryFor(p.Transitions, i)
	fps, count, first := p.Video.FPS, seg.blends[i], seg.starts[i+1]
	args := ffmpeg.KwArgs{
		"transition": transition.xfade(),
		"duration":   num6(float64(count) / float64(fps)),
		"offset":     0,
	}
	if transition.Expr != "" {
		args["expr"] = transition.Expr
	}
	tail := ffmpeg.Input(scenePart(p.Scenes[i], "tail"))
	head := ffmpeg.Input(scenePart(p.Scenes[i+1], "head"))
	blend := frames(ffmpeg.Filter([]*ffmpeg.Stream{tail, head}, "xfade", nil, args), fps, 0, count, first)
	return p.encodePiece(blend, first, count, p.transitionFile(i)).OverWriteOutput()
}

// buildAssembly joins the encoded pieces and adds the audio, cut to the
// video's length. (-shortest would cut the copied video at the audio's last
// packet, a few frames early.)
func (p *Plan) buildAssembly(seg segments) *ffmpeg.Stream {
	streams := []*ffmpeg.Stream{ffmpeg.Input(p.piecesList(), ffmpeg.KwArgs{"f": "concat", "safe": 0}).Video()}
	if audio := p.buildAudio(); audio != nil {
		streams = append(streams, audio)
	}
	return ffmpeg.Output(streams, p.Output, ffmpeg.KwArgs{
		"vcodec":   "copy",
		"movflags": "+faststart",
		"t":        num6(float64(seg.total) / float64(p.Video.FPS)),
	}).OverWriteOutput()
}

// buildScene renders a scene: into one lossless file (seg nil), or into the
// pieces of a segmented render.
func buildScene(plan *Plan, scene ScenePlan, index int, seg *segments) *ffmpeg.Stream {
	base := sceneBackgroundStream(plan, scene)

	for _, layer := range scene.Layers {
		base = applyVisualLayer(base, plan, scene, layer)
	}
	if scene.ASS != "" {
		base = base.Filter(ffmpegFilterASS, nil, plan.assArgs(scene.ASSPath))
	}
	base = applyTracks(base, plan, scene.Tracks)

	if seg == nil {
		args := maps.Clone(losslessArgs)
		args["t"] = ffTime(scene.Duration)
		return base.Output(scene.Output, args).OverWriteOutput()
	}

	fps, first := plan.Video.FPS, seg.starts[index]
	head, body, tail := seg.head(index), seg.end(index)-first, seg.blends[index]
	// Rounding the scene's ends to frames may need up to two frames more.
	base = videoRange(base).Filter("tpad", nil, ffmpeg.KwArgs{"stop_mode": "clone", "stop": 2})
	var parts []func(*ffmpeg.Stream) *ffmpeg.Stream
	if head > 0 {
		parts = append(parts, func(s *ffmpeg.Stream) *ffmpeg.Stream {
			return frames(s, fps, 0, head, 0).Output(scenePart(scene, "head"), losslessArgs)
		})
	}
	if body > head {
		parts = append(parts, func(s *ffmpeg.Stream) *ffmpeg.Stream {
			return plan.encodePiece(frames(s, fps, head, body, first+head), first+head, body-head, scenePart(scene, "body"))
		})
	}
	if tail > 0 {
		parts = append(parts, func(s *ffmpeg.Stream) *ffmpeg.Stream {
			return frames(s, fps, body, body+tail, 0).Output(scenePart(scene, "tail"), losslessArgs)
		})
	}
	if len(parts) == 1 {
		return parts[0](base).OverWriteOutput()
	}
	split := base.Split()
	outputs := make([]*ffmpeg.Stream, len(parts))
	for i, part := range parts {
		outputs[i] = part(split.Get(strconv.Itoa(i)))
	}
	return ffmpeg.MergeOutputs(outputs...).OverWriteOutput()
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
		input := mediaLayerStream(plan, source, layer, 0)
		input, x, y := animateMedia(input, frameSpace(plan.Video), layer, 0)
		args := ffmpeg.KwArgs{"x": x, "y": y}
		if enable := layer.enableExpr(); enable != "" {
			args["enable"] = enable
		}
		return base.Overlay(input, eofActionPass, args)
	case "rectangle":
		return drawRectangle(base, layer)
	case "circle":
		circle := circleStream(plan, layer, scene.Duration)
		return base.Overlay(circle, eofActionPass, ffmpeg.KwArgs{"x": layer.X, "y": layer.Y})
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

// mediaLayerStream is a media layer's prepared stream, without its first
// skip frames (it starts that far into its owner's timeline).
func mediaLayerStream(plan *Plan, scene ScenePlan, layer LayerPlan, skip int) *ffmpeg.Stream {
	input := mediaInput(plan, scene, layer)
	effects := layer.Effects
	if layer.Kind != "image" {
		input = skipFrames(fitLayerStream(input, layer), skip)
		input = applyMediaEffects(input, plan, scene, layer, effects, skip)
		return finishMediaLayer(input, layer)
	}

	// A still image: fit it and apply what doesn't change over time to its
	// first frame only, then repeat that frame. Scaling and filtering every
	// identical frame again costs as much as the rest of a scene. (The trim
	// follows the fit: ffmpeg-go merges identical nodes, and layers showing
	// the same image would share it.)
	input = fitLayerStream(input, layer).Filter("trim", nil, ffmpeg.KwArgs{"end_frame": 1})
	static := 0
	for static < len(effects) && !effects[static].animated() {
		static++
	}
	input = applyMediaEffects(input, plan, scene, layer, effects[:static], 0)
	animated := static < len(effects) || layer.Animation.Has("width", "height")
	if !animated {
		input = finishMediaLayer(input, layer)
	}
	input = input.Filter("loop", nil, ffmpeg.KwArgs{"loop": -1, "size": 1, "start": 0}).
		Filter("settb", ffmpeg.Args{"AVTB"}).
		Filter("setpts", ffmpeg.Args{fmt.Sprintf("N/%d/TB", plan.Video.FPS)}).
		Filter("trim", nil, ffmpeg.KwArgs{"duration": ffTime(scene.Duration)})
	input = skipFrames(input, skip)
	if !animated {
		return input
	}
	input = applyMediaEffects(input, plan, scene, layer, effects[static:], skip)
	return finishMediaLayer(input, layer)
}

// finishMediaLayer applies a media layer's opacity and shape mask.
func finishMediaLayer(input *ffmpeg.Stream, layer LayerPlan) *ffmpeg.Stream {
	if layer.Opacity != 1 {
		input = input.
			Filter("format", ffmpeg.Args{"rgba"}).
			ColorChannelMixer(ffmpeg.KwArgs{"aa": layer.Opacity})
	}
	if masked(layer) {
		input = maskShape(input, layer)
	}
	return input
}

func mediaInput(plan *Plan, scene ScenePlan, layer LayerPlan) *ffmpeg.Stream {
	var input *ffmpeg.Stream
	if layer.Kind == "image" {
		input = mediaInputStream(layer.Path, ffmpeg.KwArgs{
			"loop":      1,
			"framerate": plan.Video.FPS,
			"t":         ffTime(scene.Duration),
		})
	} else {
		inputArgs := ffmpeg.KwArgs{}
		if layer.SourceOffset > 0 {
			inputArgs["ss"] = ffTime(layer.SourceOffset)
		}

		input = mediaInputStream(layer.Path, inputArgs).
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
			base = blurPanel(base, x, y, width, height, glassBlur, "", false)
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

	streams[0] = videoRange(streams[0])
	args := p.finalVideoArgs()
	args["movflags"] = "+faststart"
	args["shortest"] = ""
	return ffmpeg.Output(streams, p.Output, args).OverWriteOutput(), nil
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
	return mediaInputStream(p.Music.Path, ffmpeg.KwArgs{"stream_loop": -1}).Audio().
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
		input := mediaInputStream(layer.Path, inputArgs).Audio()
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

	// geq is slow per pixel: draw the circle once and repeat that frame.
	return ffmpeg.Input(input, ffmpeg.KwArgs{"f": "lavfi"}).
		Filter("format", ffmpeg.Args{"rgba"}).
		Filter("trim", nil, ffmpeg.KwArgs{"end_frame": 1}).
		Filter("geq", nil, ffmpeg.KwArgs{
			"r": "r(X,Y)",
			"g": "g(X,Y)",
			"b": "b(X,Y)",
			"a": alphaExpression,
		}).
		Filter("loop", nil, ffmpeg.KwArgs{"loop": -1, "size": 1, "start": 0}).
		Filter("setpts", ffmpeg.Args{fmt.Sprintf("N/%d/TB", plan.Video.FPS)}).
		Filter("trim", nil, ffmpeg.KwArgs{"duration": ffTime(duration)})
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
			s = space{Rect: *track.Canvas, fps: p.Video.FPS}
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
	add := func(face *fontFace) {
		if face != nil && face.Bundled && !seen[face.Path] {
			seen[face.Path] = true
			files = append(files, face.Path)
		}
	}
	for _, scene := range p.Scenes {
		for _, l := range scene.Layers {
			add(l.face)
			add(l.boldFace)
		}
		for _, subtitle := range scene.Subtitles {
			add(subtitle.face)
			add(subtitle.boldFace)
		}
	}
	for _, track := range p.allTracks() {
		for _, clip := range track.Clips {
			add(clip.Layer.face)
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
