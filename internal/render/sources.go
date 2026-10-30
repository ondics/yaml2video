package render

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// FFmpeg's scheduler reads every input in its own thread through a small
// queue. A graph with many generated inputs (colors, shapes, folder canvases)
// and looped still images, some of them shifted to start later, can deadlock:
// input threads block on full queues while the graph waits for a frame from
// another input. It happens at random, depending on thread timing.
//
// ffmpeg-go can't express source filters without inputs, so inlineSources
// rewrites the compiled command instead: generated lavfi inputs and looped
// still images become source chains inside -filter_complex, which the graph
// pulls exactly when it needs them. Other inputs (video, audio) stay.

var inputRefRe = regexp.MustCompile(`\[(\d+)(:[^\]]*)?\]`)

type inputSpec struct {
	opts   []string // options before -i, in pairs
	source string
}

// inlineSources moves generated and still-image inputs of an FFmpeg command
// into its filter graph, renumbering the inputs that remain.
func inlineSources(args []string) []string {
	fc := slices.Index(args, "-filter_complex")
	if fc < 0 || fc+1 >= len(args) {
		return args
	}
	var inputs []inputSpec
	var pending []string
	for i := 1; i < fc; i++ {
		if args[i] == "-i" && i+1 < fc {
			inputs = append(inputs, inputSpec{opts: pending, source: args[i+1]})
			pending = nil
			i++
			continue
		}
		pending = append(pending, args[i])
	}
	if len(pending) > 0 {
		return args // options after the last input: an unexpected shape
	}
	graph := args[fc+1]

	uses := map[string]int{}
	for _, m := range inputRefRe.FindAllStringSubmatch(graph, -1) {
		uses[m[1]]++
	}
	var kept []inputSpec
	index := map[string]string{} // old input index → new reference
	var sources []string
	for n, in := range inputs {
		old := fmt.Sprint(n)
		chain, ok := inlineSource(in)
		if ok && uses[old] == 1 {
			label := "ysrc" + old
			sources = append(sources, chain+"["+label+"]")
			index[old] = label
			continue
		}
		index[old] = fmt.Sprint(len(kept))
		kept = append(kept, in)
	}
	if len(sources) == 0 {
		return args
	}

	graph = inputRefRe.ReplaceAllStringFunc(graph, func(ref string) string {
		m := inputRefRe.FindStringSubmatch(ref)
		to := index[m[1]]
		if strings.HasPrefix(to, "ysrc") {
			return "[" + to + "]"
		}
		return "[" + to + m[2] + "]"
	})
	out := []string{args[0]}
	for _, in := range kept {
		out = append(out, in.opts...)
		out = append(out, "-i", in.source)
	}
	out = append(out, "-filter_complex", strings.Join(sources, ";")+";"+graph)
	return append(out, args[fc+2:]...)
}

// inlineSource returns the filter chain that replaces an input, if it is a
// generated lavfi source or a still image looped with -loop 1.
func inlineSource(in inputSpec) (string, bool) {
	opts := map[string]string{}
	for k := 0; k+1 < len(in.opts); k += 2 {
		opts[strings.TrimPrefix(in.opts[k], "-")] = in.opts[k+1]
	}
	if len(opts)*2 != len(in.opts) {
		return "", false
	}
	if len(opts) == 1 && opts["f"] == "lavfi" {
		return in.source, true
	}
	still := map[string]bool{".png": true, ".jpg": true, ".jpeg": true, ".webp": true, ".bmp": true}
	if len(opts) == 3 && opts["loop"] == "1" && opts["framerate"] != "" && opts["t"] != "" &&
		still[strings.ToLower(filepath.Ext(in.source))] && !strings.ContainsAny(in.source, `'\:,;[]`) {
		// One decoded frame, repeated at the frame rate for the duration.
		return fmt.Sprintf("movie=filename='%s',loop=loop=-1:size=1:start=0,setpts=N/%s/TB,trim=duration=%s",
			in.source, opts["framerate"], opts["t"]), true
	}
	return "", false
}
