package controller

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ondics/yaml2video/internal/render"
)

const testOutput = "fake mp4 artifact for controller tests"

func testController(t *testing.T) *Controller {
	t.Helper()
	c, err := New(Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}

	c.load = func([]byte, []byte, string) (*render.Plan, error) {
		return &render.Plan{}, nil
	}

	c.render = func(_ context.Context, p *render.Plan, _ func(render.Progress)) error {
		return os.WriteFile(p.Output, []byte(testOutput), 0o600)
	}
	return c
}

func renderOutput(t *testing.T, c *Controller, id string) error {
	t.Helper()
	job, err := c.PrepareRender(id, []byte("video yaml"), []byte("template yaml"))
	if err != nil {
		return err
	}
	defer job.Close()
	return job.Run(context.Background(), func(render.Progress) {})
}

func readOutput(t *testing.T, c *Controller, id string) string {
	t.Helper()
	artifact, err := c.OpenOutput(id)
	if err != nil {
		t.Fatal(err)
	}
	defer artifact.Close()

	data, err := io.ReadAll(artifact.File)
	if err != nil {
		t.Fatal(err)
	}

	if !artifact.Info.Mode().IsRegular() || artifact.Info.Size() != int64(len(data)) {
		t.Fatalf("output %q has unexpected file info: %v", id, artifact.Info)
	}

	artifact.MarkFreeable()
	return string(data)
}

func metadataFor(t *testing.T, c *Controller, id string) JobMetadata {
	t.Helper()
	m, err := readMetadata(c.jobDir(id))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func awaitSignal(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for completion")
	}
}

func seedJob(t *testing.T, s *Controller, m JobMetadata) {
	t.Helper()
	dir := s.jobDir(m.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeMetadata(dir, m); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "output.mp4"), []byte(testOutput), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestJobIdentityPathSafety(t *testing.T) {
	s := testController(t)
	seen := make(map[string]string)
	for _, id := range []string{".", "..", "../escape", "/absolute/path", "nested/movie", `..\windows\path`, "hash#fragment", "name%2Fslash", "普通话", "Movie", "movie"} {
		sum := sha256.Sum256([]byte(id))
		want := hex.EncodeToString(sum[:])
		key := jobKey(id)
		if key != want || len(key) != 64 {
			t.Errorf("jobKey(%q) = %q, want %q", id, key, want)
		}

		if previous, ok := seen[key]; ok {
			t.Errorf("identities %q and %q share directory", previous, id)
		}

		seen[key] = id
		dir := s.jobDir(id)

		if filepath.Dir(dir) != filepath.Join(s.dataDir, "jobs") || filepath.Base(dir) != want {
			t.Errorf("unsafe jobDir(%q) = %q", id, dir)
		}

		if err := renderOutput(t, s, id); err != nil {
			t.Fatalf("render %q: %v", id, err)
		}

		if metadataFor(t, s, id).ID != id {
			t.Errorf("identity %q changed in metadata", id)
		}

		if got := readOutput(t, s, id); got != testOutput {
			t.Errorf("output %q = %q, want %q", id, got, testOutput)
		}
	}
}

func TestExpiredBoundaries(t *testing.T) {
	now := time.Date(2026, 10, 12, 12, 0, 0, 0, time.UTC)
	justDownloaded := now
	for _, tc := range []struct {
		name          string
		age           time.Duration
		downloadedAge *time.Duration
		want          bool
	}{
		{name: "new"},
		{name: "before seven days", age: 7*24*time.Hour - time.Nanosecond},
		{name: "at seven days", age: 7 * 24 * time.Hour, want: true},
		{name: "after seven days", age: 7*24*time.Hour + time.Nanosecond, want: true},
		{name: "before downloaded TTL", age: time.Hour, downloadedAge: durationPointer(59*time.Minute - time.Nanosecond)},
		{name: "at downloaded TTL", age: time.Hour, downloadedAge: durationPointer(59 * time.Minute), want: true},
		{name: "after downloaded TTL", age: time.Hour, downloadedAge: durationPointer(59*time.Minute + time.Nanosecond), want: true},
		{name: "download cannot extend seven days", age: 7 * 24 * time.Hour, downloadedAge: durationPointer(0), want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := JobMetadata{CreatedAt: now.Add(-tc.age)}
			if tc.downloadedAge != nil {
				downloaded := justDownloaded.Add(-*tc.downloadedAge)
				m.FreeableAt = &downloaded
			}
			if got := expired(m, now); got != tc.want {
				t.Errorf("expired = %v, want %v: %+v", got, tc.want, m)
			}
		})
	}
}

func durationPointer(d time.Duration) *time.Duration { return new(d) }

func TestCleanupTTLAndActiveExclusions(t *testing.T) {
	s := testController(t)
	now := time.Now().UTC()
	downloadedBefore := now.Add(-59*time.Minute + time.Nanosecond)
	downloadedAt := now.Add(-59 * time.Minute)
	cases := []struct {
		m       JobMetadata
		active  bool
		removed bool
	}{
		{m: JobMetadata{ID: "recent", CreatedAt: now, Ready: true}},
		{m: JobMetadata{ID: "seven days minus one", CreatedAt: now.Add(-7*24*time.Hour + time.Nanosecond), Ready: true}},
		{m: JobMetadata{ID: "seven days", CreatedAt: now.Add(-7 * 24 * time.Hour), Ready: true}, removed: true},
		{m: JobMetadata{ID: "downloaded before TTL", CreatedAt: now.Add(-time.Hour), FreeableAt: &downloadedBefore, Ready: true}},
		{m: JobMetadata{ID: "downloaded at TTL", CreatedAt: now.Add(-time.Hour), FreeableAt: &downloadedAt, Ready: true}, removed: true},
		{m: JobMetadata{ID: "active render", CreatedAt: now.Add(-8 * 24 * time.Hour)}, active: true},
		{m: JobMetadata{ID: "active download", CreatedAt: now.Add(-time.Hour), FreeableAt: &downloadedAt, Ready: true}, active: true},
	}

	for _, tc := range cases {
		seedJob(t, s, tc.m)
		if tc.active {
			s.active[jobKey(tc.m.ID)] = 1
		}
	}

	if err := s.cleanup(now); err != nil {
		t.Fatal(err)
	}

	for _, tc := range cases {
		_, err := os.Stat(s.jobDir(tc.m.ID))
		if tc.removed {
			if !os.IsNotExist(err) {
				t.Errorf("%q should be removed: %v", tc.m.ID, err)
			}
		} else if err != nil {
			t.Errorf("%q should survive: %v", tc.m.ID, err)
		}
	}

	s.mu.Lock()
	clear(s.active)
	s.mu.Unlock()

	if err := s.cleanup(now); err != nil {
		t.Fatal(err)
	}

	for _, tc := range cases {
		if tc.active {
			if _, err := os.Stat(s.jobDir(tc.m.ID)); !os.IsNotExist(err) {
				t.Errorf("released %q retained: %v", tc.m.ID, err)
			}
		}
	}
}

func TestCleanupMalformedMetadata(t *testing.T) {
	s := testController(t)
	now := time.Now()
	for _, tc := range []struct {
		id           string
		corrupt, old bool
	}{
		{id: "recent missing"}, {id: "recent corrupt", corrupt: true},
		{id: "old missing", old: true}, {id: "old corrupt", corrupt: true, old: true},
	} {
		dir := s.jobDir(tc.id)
		if err := os.Mkdir(dir, 0o755); err != nil {
			t.Fatal(err)
		}

		if tc.corrupt {
			if err := os.WriteFile(filepath.Join(dir, "metadata.json"), []byte("not JSON"), 0o600); err != nil {
				t.Fatal(err)
			}
		}

		stamp := now
		if tc.old {
			stamp = now.Add(-7 * 24 * time.Hour)
		}
		if err := os.Chtimes(dir, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}

	nonJob := filepath.Join(s.dataDir, "jobs", "not-a-directory")
	if err := os.WriteFile(nonJob, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := s.cleanup(now); err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"recent missing", "recent corrupt"} {
		if _, err := os.Stat(s.jobDir(id)); err != nil {
			t.Errorf("recent malformed %q removed: %v", id, err)
		}
	}

	for _, id := range []string{"old missing", "old corrupt"} {
		if _, err := os.Stat(s.jobDir(id)); !os.IsNotExist(err) {
			t.Errorf("old malformed %q retained: %v", id, err)
		}
	}

	if _, err := os.Stat(nonJob); err != nil {
		t.Errorf("non-job file removed: %v", err)
	}
}

func TestRestartPersistenceAndStartupCleanup(t *testing.T) {
	s := testController(t)
	if err := renderOutput(t, s, "persisted/slash"); err != nil {
		t.Fatalf("render: %v", err)
	}

	if got := readOutput(t, s, "persisted/slash"); got != testOutput {
		t.Fatalf("output = %q, want %q", got, testOutput)
	}

	original := metadataFor(t, s, "persisted/slash")
	now := time.Now().UTC()
	expiredDownload := now.Add(-60 * time.Minute)
	recentDownload := now.Add(-58 * time.Minute)
	seedJob(t, s, JobMetadata{ID: "old", CreatedAt: now.Add(-8 * 24 * time.Hour), Ready: true})
	seedJob(t, s, JobMetadata{ID: "download expired", CreatedAt: now.Add(-time.Hour), FreeableAt: &expiredDownload, Ready: true})
	seedJob(t, s, JobMetadata{ID: "download recent", CreatedAt: now.Add(-time.Hour), FreeableAt: &recentDownload, Ready: true})

	restarted, err := New(Config{DataDir: s.dataDir})
	if err != nil {
		t.Fatal(err)
	}

	for _, id := range []string{"old", "download expired"} {
		if _, err := os.Stat(restarted.jobDir(id)); !os.IsNotExist(err) {
			t.Errorf("startup retained expired %q: %v", id, err)
		}
		if artifact, err := restarted.OpenOutput(id); !os.IsNotExist(err) {
			if artifact != nil {
				artifact.Close()
			}
			t.Errorf("expired %q: OpenOutput error = %v, want not exist", id, err)
		}
	}

	restored := metadataFor(t, restarted, "persisted/slash")
	if restored.ID != original.ID || !restored.Ready || !restored.CreatedAt.Equal(original.CreatedAt) || restored.FreeableAt == nil || original.FreeableAt == nil || !restored.FreeableAt.Equal(*original.FreeableAt) {
		t.Fatalf("metadata changed after restart: before=%+v after=%+v", original, restored)
	}

	if readOutput(t, restarted, "persisted/slash") != testOutput {
		t.Fatal("persisted output changed")
	}

	if m := metadataFor(t, restarted, "persisted/slash"); !m.FreeableAt.Equal(*original.FreeableAt) {
		t.Fatal("restart download extended retention")
	}

	if err := restarted.cleanup(now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(restarted.jobDir("download recent")); !os.IsNotExist(err) {
		t.Fatalf("persisted downloaded TTL not enforced: %v", err)
	}
}

func TestExpiredIdentityCanBeReused(t *testing.T) {
	s := testController(t)
	seedJob(t, s, JobMetadata{ID: "reuse", CreatedAt: time.Now().Add(-8 * 24 * time.Hour), Ready: true})
	if err := renderOutput(t, s, "reuse"); err != nil {
		t.Fatalf("expired identity: %v", err)
	}
	m := metadataFor(t, s, "reuse")
	if !m.Ready || expired(m, time.Now()) || m.FreeableAt != nil {
		t.Fatalf("reused metadata = %+v", m)
	}
}

func TestOutputRejectsInvalidStorage(t *testing.T) {
	for _, kind := range []string{"missing", "not ready", "expired", "wrong identity", "missing output", "directory output", "corrupt metadata"} {
		t.Run(kind, func(t *testing.T) {
			s := testController(t)
			m := JobMetadata{ID: "movie", CreatedAt: time.Now(), Ready: true}
			if kind != "missing" {
				seedJob(t, s, m)
			}

			switch kind {
			case "not ready":
				m.Ready = false
			case "expired":
				m.CreatedAt = time.Now().Add(-8 * 24 * time.Hour)
			case "wrong identity":
				m.ID = "other"
			case "missing output", "directory output":
				path := filepath.Join(s.jobDir("movie"), "output.mp4")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if kind == "directory output" {
					if err := os.Mkdir(path, 0o755); err != nil {
						t.Fatal(err)
					}
				}
			case "corrupt metadata":
				if err := os.WriteFile(filepath.Join(s.jobDir("movie"), "metadata.json"), []byte("{"), 0o600); err != nil {
					t.Fatal(err)
				}
			}

			if kind == "not ready" || kind == "expired" || kind == "wrong identity" {
				if err := writeMetadata(s.jobDir("movie"), m); err != nil {
					t.Fatal(err)
				}
			}

			if artifact, err := s.OpenOutput("movie"); !os.IsNotExist(err) {
				if artifact != nil {
					artifact.Close()
				}
				t.Errorf("OpenOutput error = %v, want not exist", err)
			}

			if len(s.active) != 0 {
				t.Errorf("invalid output leaked reservation: %v", s.active)
			}
		})
	}
}

func TestNewControllerStorageAndCleanupCancellation(t *testing.T) {
	s := testController(t)
	for _, child := range []string{"jobs", "media"} {
		info, err := os.Stat(filepath.Join(s.dataDir, child))
		if err != nil || !info.IsDir() {
			t.Fatalf("storage %q not initialized: %v", child, err)
		}
	}

	blocked := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocked, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := New(Config{DataDir: blocked}); err == nil {
		t.Fatal("file data directory accepted")
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); s.RunCleanup(ctx) }()

	cancel()
	awaitSignal(t, done)
}
