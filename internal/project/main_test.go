package project_test

import (
	"os"
	"testing"
)

// Tests measure text with the bundled fonts and never download any.
func TestMain(m *testing.M) {
	cache, err := os.MkdirTemp("", "yaml2video-test-cache-")
	if err != nil {
		panic(err)
	}
	os.Setenv("YAML2VIDEO_CACHE_DIR", cache)
	os.Setenv("YAML2VIDEO_SYSTEM_FONTS", "off")
	os.Setenv("YAML2VIDEO_OFFLINE", "1")
	code := m.Run()
	os.RemoveAll(cache)
	os.Exit(code)
}
