package controller

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/ondics/yaml2video/internal/render"
)

var ErrIdentityExists = errors.New("render identity already exists")

type InvalidPlanError struct{ error }

type RenderJob struct {
	controller *Controller
	dir        string
	key        string
	metadata   JobMetadata
	plan       *render.Plan
	ready      bool
}

func (c *Controller) PrepareRender(id string, video, template []byte) (*RenderJob, error) {
	return c.prepareRender(id, func() (*render.Plan, error) {
		return c.load(video, template, filepath.Join(c.dataDir, "media"))
	})
}

// PrepareLocalRender loads files relative to the server's working directory.
// dir overrides the base for relative template_file references only.
func (c *Controller) PrepareLocalRender(id, videoPath, templatePath, dir string) (*RenderJob, error) {
	videoPath, err := filepath.Abs(videoPath)
	if err != nil {
		return nil, InvalidPlanError{err}
	}
	return c.prepareRender(id, func() (*render.Plan, error) {
		return render.LoadWithTemplateBase(videoPath, templatePath, dir)
	})
}

func (c *Controller) prepareRender(id string, load func() (*render.Plan, error)) (*RenderJob, error) {
	job, err := c.reserveRender(id)
	if err != nil {
		return nil, err
	}

	prepared := false
	defer func() {
		if !prepared {
			job.Close()
		}
	}()

	plan, err := load()
	if err != nil {
		return nil, InvalidPlanError{err}
	}
	plan.WorkDir = filepath.Join(job.dir, "work")
	plan.Output = filepath.Join(job.dir, "output.mp4")

	for i := range plan.Scenes {
		plan.Scenes[i].ASSPath = filepath.Join(plan.WorkDir, "ass", fmt.Sprintf("scene-%04d.ass", i))
		plan.Scenes[i].Output = filepath.Join(plan.WorkDir, "scenes", fmt.Sprintf("scene-%04d.mkv", i))
	}

	job.plan = plan
	prepared = true
	return job, nil
}

func (c *Controller) reserveRender(id string) (*RenderJob, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	dir, key := c.jobDir(id), jobKey(id)
	if c.active[key] > 0 {
		return nil, ErrIdentityExists
	}

	if _, err := os.Stat(dir); err == nil {
		metadata, readErr := readMetadata(dir)
		if readErr != nil || !expired(metadata, time.Now()) {
			return nil, ErrIdentityExists
		}
		if err := os.RemoveAll(dir); err != nil {
			return nil, errors.New("cannot remove expired render")
		}
	} else if !os.IsNotExist(err) {
		return nil, errors.New("cannot inspect render storage")
	}

	if err := os.Mkdir(dir, 0o755); err != nil {
		return nil, errors.New("cannot create render storage")
	}

	metadata := JobMetadata{ID: id, CreatedAt: time.Now().UTC()}
	if err := writeMetadata(dir, metadata); err != nil {
		os.RemoveAll(dir)
		return nil, errors.New("cannot save render metadata")
	}

	c.active[key]++
	return &RenderJob{
		controller: c,
		dir:        dir,
		key:        key,
		metadata:   metadata,
	}, nil
}

// Run renders the video and persists its completed state before returning.
func (j *RenderJob) Run(ctx context.Context, emit func(render.Progress)) error {
	if err := j.controller.render(ctx, j.plan, emit); err != nil {
		return err
	}

	if err := ctx.Err(); err != nil {
		return err
	}

	j.metadata.Ready = true
	if err := writeMetadata(j.dir, j.metadata); err != nil {
		return errors.New("cannot save completed render")
	}

	j.ready = true
	return nil
}

// Close must only run after the renderer has stopped using the job's files.
func (j *RenderJob) Close() {
	c := j.controller
	c.mu.Lock()
	defer c.mu.Unlock()

	if !j.ready {
		if err := os.RemoveAll(j.dir); err != nil {
			log.Printf("remove failed render: %v", err)
		}
	}

	c.release(j.key)
}

// release requires the controller lock to be held.
func (c *Controller) release(key string) {
	c.active[key]--
	if c.active[key] == 0 {
		delete(c.active, key)
	}
}
