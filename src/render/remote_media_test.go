package render

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

const remoteMediaQuery = "token=a%2Bb%2Fc%3D&expires=1700000000&part=one&part=two"

// The endpoints deliberately have no extension or trustworthy Content-Type.
// Validation must inspect the downloaded media, not the URL or HTTP headers.
func remoteMediaServer(t *testing.T) *httptest.Server {
	t.Helper()
	var pngData bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := range 64 {
		for x := range 64 {
			img.SetRGBA(x, y, color.RGBA{R: 220, G: 40, B: 80, A: 255})
		}
	}
	if err := png.Encode(&pngData, img); err != nil {
		t.Fatal(err)
	}

	// One second of mono, 16-bit PCM at 8 kHz, including a complete RIFF header.
	const samples = 8000
	wav := make([]byte, 44+samples*2)
	copy(wav, "RIFF")
	binary.LittleEndian.PutUint32(wav[4:], uint32(len(wav)-8))
	copy(wav[8:], "WAVEfmt ")
	binary.LittleEndian.PutUint32(wav[16:], 16)
	binary.LittleEndian.PutUint16(wav[20:], 1)
	binary.LittleEndian.PutUint16(wav[22:], 1)
	binary.LittleEndian.PutUint32(wav[24:], samples)
	binary.LittleEndian.PutUint32(wav[28:], samples*2)
	binary.LittleEndian.PutUint16(wav[32:], 2)
	binary.LittleEndian.PutUint16(wav[34:], 16)
	copy(wav[36:], "data")
	binary.LittleEndian.PutUint32(wav[40:], samples*2)
	for i := range samples {
		sample := int16(8000 * math.Sin(2*math.Pi*440*float64(i)/samples))
		binary.LittleEndian.PutUint16(wav[44+i*2:], uint16(sample))
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != remoteMediaQuery {
			http.Error(w, "signed query changed", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		switch r.URL.Path {
		case "/image", "/background":
			_, _ = w.Write(pngData.Bytes())
		case "/audio":
			_, _ = w.Write(wav)
		case "/invalid":
			_, _ = w.Write([]byte("not an image or an audio stream"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func requireRemoteMediaTool(t *testing.T, name string) {
	t.Helper()
	if _, err := exec.LookPath(name); err != nil {
		t.Skipf("%s not installed", name)
	}
}

func TestRemoteMediaPathURLs(t *testing.T) {
	base := t.TempDir()
	for _, source := range []string{
		"http://example.invalid/image?" + remoteMediaQuery,
		"https://example.invalid/image?" + remoteMediaQuery,
	} {
		t.Run(source, func(t *testing.T) {
			got, err := path(base, source)
			if err != nil {
				t.Fatal(err)
			}
			if got != source {
				t.Fatalf("path = %q, want unchanged URL %q", got, source)
			}
		})
	}

	for _, source := range []string{
		"ftp://example.invalid/image",
		"file:///tmp/image.png",
		"data:image/png;base64,aGVsbG8=",
		"mailto:media@example.invalid",
		"http://",
		"https:///image",
		"http://example.invalid:bad/image",
		"https://example.invalid/%zz",
		"https://bad host/image",
	} {
		t.Run(source, func(t *testing.T) {
			if got, err := path(base, source); err == nil {
				t.Fatalf("accepted invalid media URI %q as %q", source, got)
			}
		})
	}

	local := filepath.Join(base, "local.png")
	if err := os.WriteFile(local, []byte("local path validation remains filesystem-only"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"local.png", local} {
		if got, err := path(base, source); err != nil || got != local {
			t.Errorf("path(%q) = %q, %v; want %q", source, got, err, local)
		}
	}
}

func TestRemoteMediaPathValidation(t *testing.T) {
	requireRemoteMediaTool(t, "ffprobe")
	server := remoteMediaServer(t)
	base := t.TempDir()
	for _, tc := range []struct {
		name, endpoint, kind string
		wantError            bool
	}{
		{"signed extensionless PNG", "/image", "image", false},
		{"signed extensionless WAV", "/audio", "audio", false},
		{"audio is not an image", "/audio", "image", true},
		{"image is not audio", "/image", "audio", true},
		{"missing image", "/missing", "image", true},
		{"missing audio", "/missing", "audio", true},
		{"invalid image bytes", "/invalid", "image", true},
		{"invalid audio bytes", "/invalid", "audio", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := server.URL + tc.endpoint + "?" + remoteMediaQuery
			got, err := mediaPath(base, source, tc.kind)
			if tc.wantError {
				if err == nil {
					t.Fatalf("mediaPath(%q, %q) accepted invalid media as %q", source, tc.kind, got)
				}
				if tc.endpoint == "/missing" && !strings.Contains(err.Error(), "404") {
					t.Errorf("HTTP error does not identify status 404: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != source {
				t.Fatalf("mediaPath = %q, want full original URL %q (not the probe's temporary file)", got, source)
			}
		})
	}
}

func TestRemoteMediaValidationDeadline(t *testing.T) {
	for _, flushHeaders := range []bool{false, true} {
		name := "waiting for headers"
		if flushHeaders {
			name = "waiting for body"
		}
		t.Run(name, func(t *testing.T) {
			started := make(chan struct{}, 1)
			release := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				started <- struct{}{}
				if flushHeaders {
					w.WriteHeader(http.StatusOK)
					w.(http.Flusher).Flush()
				}
				select {
				case <-r.Context().Done():
				case <-release:
				}
			}))
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer func() {
				cancel()
				close(release)
				server.Close()
			}()
			result := make(chan error, 1)
			go func() {
				result <- validateRemoteMedia(ctx, server.URL, "image")
			}()
			select {
			case err := <-result:
				if !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("validation error = %v, want context deadline exceeded", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("validation ignored the short context deadline")
			}
			select {
			case <-started:
			default:
				t.Fatal("validation timed out before reaching the hanging HTTP handler")
			}
		})
	}
}

const remoteMediaTemplate = `$schema: x
version: 2
template_id: remote-media
description: HTTP media regression fixture
format: { aspect_ratio: "1:1", width: 64, height: 64, fps: 10 }
defaults:
  theme: { background: "#000000", text_color: "#FFFFFF", accent_color: "#D7FF3E" }
  timing: { intro_duration: 0.4s, slide_duration: 0.4s, outro_duration: 0.4s }
  audio: { normalize: false }
composition:
  sections:
    - { id: i, role: intro, source: content.intro }
    - { id: s, role: slide, source: content.slides, repeat: true }
layout:
  intro: card
  slide: card
  outro: card
  card:
    background: { path: %q, fit: cover }
    elements:
      - { type: image, id: photo, path: "$(BILD)", placement: { anchor: center } }
`

func remoteMediaPlan(t *testing.T, server *httptest.Server) *Plan {
	t.Helper()
	imageURL := server.URL + "/image?" + remoteMediaQuery
	backgroundURL := server.URL + "/background?" + remoteMediaQuery
	audioURL := server.URL + "/audio?" + remoteMediaQuery
	video := fmt.Sprintf(`$schema: x
version: 2
template: remote-media
metadata: { title: remote media, language: en-US }
assets:
  music: { path: %q }
content:
  slides:
    - { id: one, variables: { BILD: %q } }
`, audioURL, imageURL)
	plan, err := FromBytes([]byte(video), []byte(fmt.Sprintf(remoteMediaTemplate, backgroundURL)), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return plan
}

func TestRemoteMediaFromBytesAndCommands(t *testing.T) {
	requireRemoteMediaTool(t, "ffprobe")
	server := remoteMediaServer(t)
	plan := remoteMediaPlan(t, server)
	if len(plan.Scenes) != 1 {
		t.Fatalf("scenes = %d, want 1", len(plan.Scenes))
	}
	scene := plan.Scenes[0]
	backgroundURL := server.URL + "/background?" + remoteMediaQuery
	if len(scene.Layers) != 1 || scene.Layers[0].Path != backgroundURL {
		t.Fatalf("background URL not preserved: %+v", scene.Layers)
	}
	track := layoutTrack(scene)
	if track == nil {
		t.Fatal("custom layout track missing")
	}
	photo := clipByID(track, "photo")
	imageURL := server.URL + "/image?" + remoteMediaQuery
	if photo == nil || photo.Layer.Path != imageURL {
		t.Fatalf("$(BILD) image URL not preserved: %+v", photo)
	}
	audioURL := server.URL + "/audio?" + remoteMediaQuery
	if plan.Music == nil || plan.Music.Path != audioURL {
		t.Fatalf("music URL not preserved: %+v", plan.Music)
	}
	commands, err := plan.Commands()
	if err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{backgroundURL, imageURL, audioURL} {
		found := false
		for _, command := range commands {
			// Check exact input arguments, not substrings in shell-escaped output.
			// rw_timeout is an input option and must precede this input's -i.
			inputStart := 1
			for i := 1; i+1 < len(command); i++ {
				if command[i] != "-i" {
					continue
				}
				if command[i+1] == source {
					found = true
					timeout := false
					for j := inputStart; j+1 < i; j++ {
						if command[j] == "-rw_timeout" && command[j+1] == "15000000" {
							timeout = true
						}
					}
					if !timeout {
						t.Errorf("URL %q lacks input rw_timeout 15000000: %v", source, command)
					}
				}
				inputStart = i + 2
				i++
			}
		}
		if !found {
			t.Errorf("commands do not preserve URL as an input %q: %v", source, commands)
		}
	}
}

func TestRemoteMediaRender(t *testing.T) {
	if testing.Short() {
		t.Skip("executes FFmpeg")
	}
	requireRemoteMediaTool(t, "ffprobe")
	requireRemoteMediaTool(t, "ffmpeg")
	server := remoteMediaServer(t)
	plan := remoteMediaPlan(t, server)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := plan.Render(ctx); err != nil {
		t.Fatalf("render remote image and audio: %v", err)
	}
	output, err := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_streams", "-of", "json", plan.Output).Output()
	if err != nil {
		t.Fatalf("probe rendered video: %v", err)
	}
	var probe struct {
		Streams []struct {
			CodecType string `json:"codec_type"`
			Width     int    `json:"width"`
			Height    int    `json:"height"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(output, &probe); err != nil {
		t.Fatal(err)
	}
	video, audio := false, false
	for _, stream := range probe.Streams {
		switch stream.CodecType {
		case "video":
			video = stream.Width == 64 && stream.Height == 64
		case "audio":
			audio = true
		}
	}
	if !video || !audio {
		t.Fatalf("render must contain 64x64 video and audio streams: %s", output)
	}
}
