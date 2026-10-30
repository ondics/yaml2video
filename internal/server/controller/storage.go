package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/ondics/yaml2video/internal/render"
)

const retention = 7 * 24 * time.Hour
const freeableRetention = 59 * time.Minute
const cleanupInterval = time.Minute

// Config controls server storage. An empty DataDir uses the platform default.
type Config struct{ DataDir string }

func defaultDataDir() string {
	switch runtime.GOOS {
	case "windows":
		base := os.Getenv("ProgramData")
		if base == "" {
			base = `C:\ProgramData`
		}
		return filepath.Join(base, "yaml2video")
	case "darwin":
		return "/Library/Application Support/yaml2video"
	default:
		return "/var/lib/yaml2video"
	}
}

type JobMetadata struct {
	ID         string     `json:"id"`
	CreatedAt  time.Time  `json:"created_at"`
	FreeableAt *time.Time `json:"freeable_at,omitempty"`
	Ready      bool       `json:"ready"`
}

type Controller struct {
	dataDir string
	mu      sync.Mutex
	active  map[string]int
	load    func([]byte, []byte, string) (*render.Plan, error)
	render  func(context.Context, *render.Plan, func(render.Progress)) error
}

func New(config Config) (*Controller, error) {
	if config.DataDir == "" {
		config.DataDir = defaultDataDir()
	}

	dir, err := filepath.Abs(config.DataDir)
	if err != nil {
		return nil, err
	}

	for _, child := range []string{"jobs", "media"} {
		if err := os.MkdirAll(filepath.Join(dir, child), 0o755); err != nil {
			return nil, fmt.Errorf("initialize server data directory %s (override with YAML2VIDEO_DATA_DIR): %w", dir, err)
		}
	}

	s := &Controller{
		dataDir: dir,
		active:  make(map[string]int),
		load:    render.FromBytes,
		render: func(ctx context.Context, p *render.Plan, progress func(render.Progress)) error {
			return p.RenderWithProgress(ctx, progress)
		},
	}

	if err := s.cleanup(time.Now()); err != nil {
		return nil, err
	}

	return s, nil
}

func jobKey(id string) string                 { sum := sha256.Sum256([]byte(id)); return hex.EncodeToString(sum[:]) }
func (s *Controller) jobDir(id string) string { return filepath.Join(s.dataDir, "jobs", jobKey(id)) }

func writeMetadata(dir string, metadata JobMetadata) error {
	data, err := json.Marshal(metadata)
	if err != nil {
		return err
	}

	temp, err := os.CreateTemp(dir, ".metadata-*")
	if err != nil {
		return err
	}
	name := temp.Name()
	defer os.Remove(name)

	if _, err := temp.Write(data); err != nil {
		temp.Close()
		return err
	}

	if err := temp.Close(); err != nil {
		return err
	}

	return os.Rename(name, filepath.Join(dir, "metadata.json"))
}

func readMetadata(dir string) (JobMetadata, error) {
	var metadata JobMetadata
	data, err := os.ReadFile(filepath.Join(dir, "metadata.json"))
	if err != nil {
		return metadata, err
	}
	err = json.Unmarshal(data, &metadata)
	return metadata, err
}

func (m JobMetadata) ExpiresAt() time.Time {
	expiresAt := m.CreatedAt.Add(retention)
	if m.FreeableAt != nil {
		freeableExpiry := m.FreeableAt.Add(freeableRetention)
		if freeableExpiry.Before(expiresAt) {
			expiresAt = freeableExpiry
		}
	}
	return expiresAt
}

func expired(m JobMetadata, now time.Time) bool {
	return !now.Before(m.ExpiresAt())
}

func (s *Controller) cleanup(now time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	entries, err := os.ReadDir(filepath.Join(s.dataDir, "jobs"))
	if err != nil {
		return err
	}

	for _, entry := range entries {
		if !entry.IsDir() || s.active[entry.Name()] > 0 {
			continue
		}

		dir := filepath.Join(s.dataDir, "jobs", entry.Name())
		metadata, err := readMetadata(dir)

		if err != nil {
			// A crash before metadata was committed must not leave an immortal cache.
			info, statErr := entry.Info()
			if statErr != nil {
				return statErr
			}
			if now.Before(info.ModTime().Add(retention)) {
				continue
			}
		} else if !expired(metadata, now) {
			continue
		}

		if err := os.RemoveAll(dir); err != nil {
			return err
		}
	}
	return nil
}

// RunCleanup removes expired jobs every minute until ctx is canceled.
func (s *Controller) RunCleanup(ctx context.Context) {
	ticker := time.NewTicker(cleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			if err := s.cleanup(now); err != nil {
				log.Printf("clean render cache: %v", err)
			}
		}
	}
}
