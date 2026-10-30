package controller

import (
	"log"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type OutputArtifact struct {
	controller *Controller
	dir        string
	key        string
	File       *os.File
	Info       os.FileInfo
}

// ListRenders returns completed, unexpired jobs with an available output file.
func (c *Controller) ListRenders() ([]JobMetadata, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	jobsDir := filepath.Join(c.dataDir, "jobs")
	entries, err := os.ReadDir(jobsDir)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	renders := make([]JobMetadata, 0)
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(jobsDir, entry.Name())
		metadata, err := readMetadata(dir)
		if err != nil || metadata.ID == "" || jobKey(metadata.ID) != entry.Name() || !metadata.Ready || expired(metadata, now) {
			continue
		}
		info, err := os.Stat(filepath.Join(dir, "output.mp4"))
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		renders = append(renders, metadata)
	}

	sort.Slice(renders, func(i, j int) bool {
		if renders[i].CreatedAt.Equal(renders[j].CreatedAt) {
			return renders[i].ID < renders[j].ID
		}
		return renders[i].CreatedAt.After(renders[j].CreatedAt)
	})
	return renders, nil
}

// GetRender returns completed, unexpired jobs with an available output file.
func (c *Controller) GetRender(id string) (*JobMetadata, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	jobsDir := filepath.Join(c.dataDir, "jobs")
	entries, err := os.ReadDir(jobsDir)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		dir := filepath.Join(jobsDir, entry.Name())
		metadata, err := readMetadata(dir)
		if err != nil || metadata.ID == "" || jobKey(metadata.ID) != entry.Name() || expired(metadata, now) {
			continue
		}

		info, err := os.Stat(filepath.Join(dir, "output.mp4"))
		if err != nil || !info.Mode().IsRegular() {
			continue
		}

		if metadata.ID == id {
			return &metadata, nil
		}
	}

	return nil, nil
}

func (c *Controller) OpenOutput(id string) (*OutputArtifact, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	dir := c.jobDir(id)
	metadata, err := readMetadata(dir)
	if err != nil || metadata.ID != id || !metadata.Ready || expired(metadata, time.Now()) {
		return nil, os.ErrNotExist
	}

	file, err := os.Open(filepath.Join(dir, "output.mp4"))
	if err != nil {
		return nil, err
	}

	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, os.ErrNotExist
	}

	key := jobKey(id)
	c.active[key]++
	return &OutputArtifact{
		controller: c,
		dir:        dir,
		key:        key,
		File:       file,
		Info:       info,
	}, nil
}

func (a *OutputArtifact) Close() {
	defer a.File.Close()
	c := a.controller

	c.mu.Lock()
	defer c.mu.Unlock()
	c.release(a.key)
}

func (a *OutputArtifact) MarkFreeable() {
	c := a.controller
	c.mu.Lock()
	defer c.mu.Unlock()

	metadata, err := readMetadata(a.dir)
	if err == nil && metadata.FreeableAt == nil {
		now := time.Now().UTC()
		metadata.FreeableAt = &now
		if err := writeMetadata(a.dir, metadata); err != nil {
			log.Printf("mark render freeable: %v", err)
		}
	}
}
