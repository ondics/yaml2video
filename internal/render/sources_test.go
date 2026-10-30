package render

import (
	"strings"
	"testing"
)

func TestInlineSources(t *testing.T) {
	args := []string{"ffmpeg",
		"-f", "lavfi", "-i", "color=c=black:s=64x64:r=30:d=1",
		"-framerate", "30", "-loop", "1", "-t", "0.999999", "-i", "assets/card.png",
		"-i", "assets/music.mp3",
		"-f", "lavfi", "-i", "color=c=red:s=8x8:r=30:d=0.5,format=yuva444p",
		"-filter_complex", "[1]format=rgba[s0];[0][s0]overlay[s1];[s1][3]overlay[s2];[2:a]volume=0.5[s3]",
		"-map", "[s2]", "-map", "[s3]", "out.mkv", "-y"}
	got := strings.Join(inlineSources(args), " ")
	want := "ffmpeg -i assets/music.mp3 -filter_complex " +
		"color=c=black:s=64x64:r=30:d=1[ysrc0];" +
		"movie=filename='assets/card.png',loop=loop=-1:size=1:start=0,setpts=N/30/TB,trim=duration=0.999999[ysrc1];" +
		"color=c=red:s=8x8:r=30:d=0.5,format=yuva444p[ysrc3];" +
		"[ysrc1]format=rgba[s0];[ysrc0][s0]overlay[s1];[s1][ysrc3]overlay[s2];[0:a]volume=0.5[s3] " +
		"-map [s2] -map [s3] out.mkv -y"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}

	// Commands without a filter graph, or with an input used twice, stay as they are.
	plain := []string{"ffmpeg", "-f", "lavfi", "-i", "color=c=black", "out.mkv"}
	if strings.Join(inlineSources(plain), " ") != strings.Join(plain, " ") {
		t.Error("a command without a filter graph changed")
	}
	twice := []string{"ffmpeg", "-f", "lavfi", "-i", "color", "-filter_complex", "[0]null[a];[0]null[b]", "x"}
	if strings.Join(inlineSources(twice), " ") != strings.Join(twice, " ") {
		t.Error("an input used twice must stay an input")
	}
}
