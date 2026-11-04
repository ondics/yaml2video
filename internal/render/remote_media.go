package render

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"time"

	ffmpeg "github.com/u2takey/ffmpeg-go"
)

const remoteMediaTimeout = 15 * time.Second
const maxRemoteMediaBytes = 64 << 20

func mediaURL(source string) (*url.URL, error) {
	u, err := url.Parse(source)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.Opaque != "" {
		return nil, fmt.Errorf("unsupported or invalid media URI %q", source)
	}
	return u, nil
}

// mediaPath probes downloaded bytes rather than trusting an extension or a
// server's Content-Type. The original signed URL remains the FFmpeg source.
func mediaPath(base string, value any, kind string) (string, error) {
	source, err := path(base, value)
	if err != nil || source == "" {
		return source, err
	}
	if _, err := mediaURL(source); err != nil {
		return source, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), remoteMediaTimeout)
	defer cancel()
	if err := validateRemoteMedia(ctx, source, kind); err != nil {
		return "", fmt.Errorf("media %s: %w", source, err)
	}
	return source, nil
}

func validateRemoteMedia(ctx context.Context, source, kind string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
	if err != nil {
		return err
	}
	client := &http.Client{
		Timeout: remoteMediaTimeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			_, err := mediaURL(req.URL.String())
			return err
		},
	}
	response, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("cannot load medium: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("cannot load medium (HTTP %d)", response.StatusCode)
	}
	file, err := os.CreateTemp("", "yaml2video-media-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	defer file.Close()
	n, err := io.Copy(file, io.LimitReader(response.Body, maxRemoteMediaBytes+1))
	if err != nil {
		return fmt.Errorf("cannot load medium: %w", err)
	}
	if n > maxRemoteMediaBytes {
		return fmt.Errorf("medium exceeds %d byte limit", maxRemoteMediaBytes)
	}
	if err := file.Close(); err != nil {
		return err
	}
	// Restrict protocols even for the local probe so it cannot fetch nested
	// network URLs from a downloaded playlist.
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-protocol_whitelist", "file", "-show_entries", "stream=codec_type,codec_name", "-of", "json", file.Name())
	output, err := cmd.Output()
	if ctx.Err() != nil {
		return fmt.Errorf("media validation timed out: %w", ctx.Err())
	}
	if err != nil {
		return fmt.Errorf("no supported %s format (ffprobe): %w", kind, err)
	}
	var probe struct {
		Streams []struct {
			Type  string `json:"codec_type"`
			Codec string `json:"codec_name"`
		} `json:"streams"`
	}
	if err := json.Unmarshal(output, &probe); err != nil {
		return fmt.Errorf("probe medium: %w", err)
	}
	for _, stream := range probe.Streams {
		if kind == "audio" && stream.Type == "audio" && stream.Codec != "" && stream.Codec != "unknown" {
			return nil
		}
		if kind == "image" && stream.Type == "video" {
			switch stream.Codec {
			case "png", "mjpeg", "webp", "bmp", "gif", "tiff", "jpeg2000":
				return nil
			}
		}
	}
	return fmt.Errorf("no supported %s format", kind)
}

// Apply the timeout to each remote input, including loudness analysis passes.
func mediaInputStream(source string, options ffmpeg.KwArgs) *ffmpeg.Stream {
	if _, err := mediaURL(source); err == nil {
		options["rw_timeout"] = remoteMediaTimeout.Microseconds()
	}
	return ffmpeg.Input(source, options)
}
