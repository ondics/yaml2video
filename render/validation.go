package render

import (
	"fmt"
	"time"
)

var transitions = map[string]string{
	"fade": "fade", "dissolve": "dissolve", "wipe-left": "wipeleft", "wipe-right": "wiperight",
	"wipe-up": "wipeup", "wipe-down": "wipedown", "slide-left": "slideleft",
	"slide-right": "slideright", "slide-up": "slideup", "slide-down": "slidedown",
	"circle-open": "circleopen", "circle-close": "circleclose", "radial": "radial",
	"pixelize": "pixelize", "zoom-in": "zoomin", "cover": "coverleft", "reveal": "revealleft", "blur": "hblur",
}

func xfadeType(kind string) string { return transitions[kind] }

func (p *Plan) validateRenderPlan() error {
	if p.Duration <= 0 {
		return fmt.Errorf("plan duration must be positive")
	}

	for i, scene := range p.Scenes {
		if scene.Duration <= 0 {
			return fmt.Errorf("scene %d: duration must be positive", i)
		}

		for j, layer := range scene.Layers {
			if err := validateEffects(layer, scene.Duration); err != nil {
				return fmt.Errorf("scene %d layer %d: %w", i, j, err)
			}
		}

		for j, audio := range scene.Audio {
			remaining := scene.Duration - audio.Offset
			duration := mediaDuration(audio.Duration, remaining)
			if audio.Offset < 0 || remaining <= 0 || duration <= 0 || duration > remaining || audio.SourceOffset < 0 {
				return fmt.Errorf("scene %d audio %d: offset and duration must fit within the scene", i, j)
			}
			if err := validateFades(audio.FadeIn, audio.FadeOut, duration); err != nil {
				return fmt.Errorf("scene %d audio %d: %w", i, j, err)
			}
		}
	}

	for _, tr := range p.Transitions {
		if tr.FromScene < 0 || tr.FromScene >= len(p.Scenes)-1 {
			return fmt.Errorf("transition: invalid scene index %d", tr.FromScene)
		}

		if tr.Type == "cut" {
			if tr.Duration != 0 {
				return fmt.Errorf("cut transition must have zero duration")
			}
			continue
		}

		if _, ok := transitions[tr.Type]; !ok {
			return fmt.Errorf("unsupported scene transition %q", tr.Type)
		}

		if tr.Duration <= 0 || tr.Duration > p.Scenes[tr.FromScene].Duration || tr.Duration > p.Scenes[tr.FromScene+1].Duration {
			return fmt.Errorf("transition %q must fit within both scenes", tr.Type)
		}
	}

	if p.Music != nil {
		if err := validateFades(p.Music.FadeIn, p.Music.FadeOut, p.Duration); err != nil {
			return fmt.Errorf("music: %w", err)
		}

		if d := p.Music.Ducking; d != nil && d.Enabled {
			if d.Amount < 0 || d.Amount > 1 || d.Attack < 0 || d.Release < 0 {
				return fmt.Errorf("music ducking: invalid amount, attack, or release")
			}
			// FFmpeg sidechaincompress accepts attack 0.01-2000 ms and release 0.01-9000 ms.
			if d.Attack < 10*time.Microsecond || d.Attack > 2*time.Second || d.Release < 10*time.Microsecond || d.Release > 9*time.Second {
				return fmt.Errorf("music ducking: attack must be 0.01ms–2s and release 0.01ms–9s")
			}
		}
	}
	return nil
}

func validateFades(in, out, duration time.Duration) error {
	if in < 0 || out < 0 || in > duration || out > duration {
		return fmt.Errorf("fade durations must fit within track duration")
	}
	return nil
}
