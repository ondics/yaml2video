# yaml2video v2 User Guide

## Purpose

Version 2 separates the content of a short video from its visual design:

- `video.yaml` describes what the video says, shows, and plays;
- `template.yaml` describes the output format, layout, style, effects, and defaults;
- the renderer combines both documents into a render plan.

The public specification is written in English. The content can be localized; the examples in this directory are written in German for office workers.

The public documents do not contain FFmpeg filters, codec flags, or renderer-specific layer graphs.

## Files and schemas

The two documents are validated with JSON Schema Draft 2020-12:

- [`video.schema.json`](video.schema.json) validates `video.yaml`;
- [`template.schema.json`](template.schema.json) validates `template.yaml`;
- [YAML format and rendering reference](video2yaml-spec.md) explains fields, composition, and renderer limits.

Place the schema reference at the top of each YAML file:

```yaml
$schema: ./video.schema.json
```

Use `./template.schema.json` in a template file.

## Writing a video document

A video document has five practical parts:

1. schema and version;
2. template selection;
3. metadata;
4. concrete media assets;
5. localized content, timing, and optional audio events.

Minimal structure:

```yaml
$schema: ./video.schema.json
version: 2
template: office-mobility-simple
template_file: template.yaml

metadata:
  title: "Kurze Bewegungspause"
  language: de-DE

content:
  slides:
    - image: assets/schulterkreisen.png
      text: "Schultern kreisen"
```

The selected template supplies the missing layout and timing defaults. `template` is a required identifier and must match the selected template's `template_id`; it is not a file path or an automatic lookup key. The optional `template_file` supplies a local template YAML path for the CLI.

With `template_file` set, render or preview without `-t`:

```sh
yaml2video examples/simple/video.yaml
yaml2video -n examples/simple/video.yaml
```

An explicit `-t template.yaml` overrides `template_file`. If neither is supplied, rendering errors. Relative `template_file` paths resolve beside the video YAML by default. When `--work-dir` is explicitly supplied, they resolve from that directory instead; a relative `--work-dir` itself resolves from the current working directory. Absolute `template_file` paths are unaffected. Relative explicit `-t` paths always resolve from the current working directory.

`--work-dir` still controls intermediate files (by default, `.yaml2video` beside the video YAML); it does not change the CLI's media base directory. The API's `LoadBytes` continues to use the explicitly supplied template bytes and does not look up `template_file` on the server.

## Metadata and assets

`metadata.title` and `metadata.language` are required. Use a BCP-47-like value such as `de-DE` or `en-US`.

```yaml
metadata:
  title: "60 Sekunden Schreibtisch-Pause"
  language: de-DE
  audience: "Menschen mit sitzender Bildschirmarbeit"
  description: "Vier kurze Übungen für die nächste Arbeitspause."
  tags: [Büro, Bewegung, Gesundheit]
  target_duration_seconds: 19

assets:
  logo: assets/gesund-am-arbeitsplatz-logo.png
  music:
    path: assets/leichter-beat.mp3
    volume: 0.35
    fade_in: 0.5s
    fade_out: 1s
```

`target_duration_seconds` is informational and may be checked by a strict renderer. Music is mixed over the complete effective video duration and is looped or trimmed by the renderer.

Relative media paths in the CLI are resolved beside the video document, even when `--work-dir` is supplied. The API uses its supplied media base directory.

## Content structure

The standard content model has three semantic areas:

- `intro`: opening title and optional image;
- `slides`: ordered image-and-text items;
- `outro`: closing message and required call to action.

The requirements below apply to the template's classic layouts. An item can pick another named layout with `layout:` and fill a custom layout's `$(NAME)` placeholders with `variables:`; see [named and custom layouts](#named-and-custom-layouts).

### Intro

An intro must contain at least a `title` or an `image`:

```yaml
content:
  intro:
    title: "Beweg dich kurz!"
    subtitle: "Drei einfache Übungen am Arbeitsplatz"
    image: assets/intro-schreibtisch.png
    duration: 3s
```

### Slides

Each slide requires an `image` and a `text` value:

```yaml
content:
  slides:
    - id: schultern
      image: assets/schulterkreisen.png
      label: "SCHULTERN"
      text: "Schultern hochziehen und lösen"
      tip: "Fünf Wiederholungen"
      alt_text: "Eine Person hebt und senkt beide Schultern."
      duration: 3s
```

Slides are rendered in YAML order. Available fields are `id`, `image`, `text`, `tip`, `label`, `alt_text`, `duration`, `audio`, and `sound_effects`.

### Outro

An outro requires a `cta` value:

```yaml
content:
  outro:
    title: "Kleine Bewegung. Großer Unterschied."
    text: "Mach die nächste Pause aktiv."
    cta: "Folge für weitere Büro-Tipps"
    duration: 3s
```

## Audio per section

The video can use one background music track plus one foreground track per intro, slide, or outro. A section track starts when its section starts and is trimmed or faded to fit the section.

```yaml
content:
  slides:
    - image: assets/handgelenke.png
      text: "Handgelenke kreisen"
      duration: 3s
      audio:
        path: assets/handgelenke-hinweis.wav
        volume: 0.8
        fade_out: 0.2s
```

Short sound effects are positioned relative to the start of their containing section. `0s` means exactly at the section start:

```yaml
sound_effects:
  - path: assets/soft-pop.wav
    at: 0s
    volume: 0.45
```

The same `sound_effects` block can be placed inside an intro, slide, or outro. `at` must not be later than the containing section duration. Sound effects are mixed with the music and foreground audio; they do not activate music ducking.

## Duration syntax

Durations are positive numbers in seconds or strings ending in `s`:

```yaml
duration: 3
duration: 3.5s
```

Sound-effect offsets may additionally be zero:

```yaml
at: 0s
```

Values such as `3m` and `500ms` are not part of the v2 core syntax.

## Writing a template document

A template has six practical parts:

1. schema and version;
2. template identifier;
3. output format;
4. defaults;
5. section composition;
6. layouts for `intro`, `slide`, and `outro`.

Template files own layout, typography, colors, effects, transitions, audio defaults, and output format. A content author normally changes only `video.yaml`.

## Composition and transitions

The standard composition uses these section roles:

```yaml
composition:
  sections:
    - id: intro
      role: intro
      source: content.intro
    - id: exercises
      role: slide
      source: content.slides
      repeat: true
      duration_from: item.duration
    - id: outro
      role: outro
      source: content.outro
```

Transitions are semantic names. The first profile supports `cut`, `fade`, `dissolve`, directional `wipe` and `slide`, `circle-open`, `circle-close`, `radial`, `pixelize`, `zoom-in`, `cover`, `reveal`, and `blur`:

```yaml
defaults:
  timing:
    intro_duration: 3s
    slide_duration: 3s
    outro_duration: 3s
    transition:
      type: dissolve
      duration: 0.45s
```

A section can override the default with `transition_out`. A transition overlaps adjacent sections and therefore reduces the effective total duration.

## The effect language

Effects are declared on a template layout element using an ordered `effects` list. The renderer maps them to FFmpeg operations without exposing FFmpeg syntax in YAML.

### Text effects

```yaml
text:
  placement:
    anchor: center
  style:
    font_size: 56
    weight: bold
    color: "#FFFFFF"
  effects:
    - type: text_shadow
      color: "#000000"
      opacity: 0.65
      offset_x: 2
      offset_y: 2
      blur: 3
    - type: text_outline
      color: "#12352A"
      width: 2
```

Supported text effects are `text_shadow`, `text_glow`, `text_outline`, `glass_panel`, and `fade`.

`glass_panel` is the recommended way to make text readable over a screenshot:

```yaml
effects:
  - type: glass_panel
    color: "#FFFFFF"
    opacity: 0.72
    blur: 12
    padding: [24, 14]
```

The renderer creates a blurred background region, adds a translucent tint, and places the text above it. This effect belongs to the text element, not to `video.yaml`.

### Image effects

```yaml
image:
  placement:
    anchor: center
  fit: cover
  effects:
    - type: pan_zoom
      from_scale: 1
      to_scale: 1.08
      from_anchor: center
      to_anchor: top-right
    - type: color_filter
      preset: warm
      intensity: 0.25
    - type: vignette
      color: "#000000"
      opacity: 0.2
```

Supported image effects are `pan_zoom`, `color_filter`, `vignette`, `blur`, and `fade`.

Effects are applied in list order. The renderer must reject an effect that is not suitable for the selected element type.

## Fonts

List families with `yaml2video fonts`, `make fonts` in Docker, or [`GET /fonts`](server-api.md#get-fonts). Both use the same underlying catalogue: the CLI displays a readable table with font family, source, cached, and offline columns; the HTTP API returns JSON with `family`, `sources`, `cached`, and `available_offline`. Listing can fetch Google Fonts metadata online but never downloads font files. With `YAML2VIDEO_OFFLINE=1`, cached Google Fonts metadata and downloaded instances remain listed. Metadata-only families have `available_offline: false`; a cached family may still need another weight or optical size downloaded for a particular render.

Use `yaml2video fonts --offline-only` or `make fonts ARGS="--offline-only"` to filter the table to offline-available families. The flag only filters results; use `YAML2VIDEO_OFFLINE=1` to prevent metadata network requests too.

The Docker image installs DejaVu Sans, DejaVu Sans Mono, and DejaVu Serif. Go and Go Mono (regular and bold) are bundled in the executable on every platform.

Choose font families in the template with `defaults.typography.font_family` or an element's `style.font_family`. Fonts resolve from project files (`fonts/` next to the video YAML or `defaults.typography.fonts`), installed system fonts, or Google Fonts downloads, which are cached. See the [font reference](reference.md#fonts) for configuration and offline controls.

A missing font is a hard compilation error, even for a dry run. The error names the requested family and suggests available project, system, or Google Fonts families and the bundled `Go` and `Go Mono` families. Supply or install the intended font, allow its Google Fonts download, or explicitly select an available family. Only the names `Go` and `Go Mono` select the bundled fonts; the renderer never automatically substitutes them for another family.

## Layout bindings

Layout element names bind to content fields with the same name:

```text
layout.slide.image  -> content.slides[].image
layout.slide.text   -> content.slides[].text
layout.slide.tip    -> content.slides[].tip
layout.outro.cta    -> content.outro.cta
```

An optional element is omitted when its content value is absent. The `logo` binds to `assets.logo`. A slide `progress` element is generated from the current slide number and total number of slides.

## Named and custom layouts

`intro`, `slide`, and `outro` are only the defaults. A template can add as many named layouts as it needs. Each is classic (the fixed elements above) or custom: an ordered list of `text`, `image`, `rect`, `circle`, `progress`, `audio`, and `effect` elements, with no predefined title or tip. Custom elements read the content through `$(NAME)` variables:

```yaml
# template.yaml
layout:
  quote:
    - type: rect
      width: 900
      height: 900
      corner_radius: 48
      color: "#16323A"
      placement: { anchor: center }
    - type: text
      text: $(QUOTE)
      placement: { anchor: center }
      style: { font_size: 58, weight: bold, align: center }
      max_width_ratio: 0.72
      max_lines: 5
```

```yaml
# video.yaml
content:
  slides:
    - layout: quote
      variables:
        QUOTE: "Eine kurze Pause reicht schon."
```

A slide without `layout:` uses its section's `layout:`, or else `slide`. See [layouts](layouts.md) for every element field, variable rules, and aliases.

## Designing short social videos

For office-mobility videos, start with:

- intro: 2–4 seconds;
- each exercise slide: 2–4 seconds;
- outro: 2–4 seconds;
- one movement and one actionable tip per slide;
- vertical `9:16` output for mobile-first publishing;
- one restrained transition style;
- music at low volume, with foreground audio or speech ducking it when needed.

Keep German text short enough for the template's maximum line count. The most attractive result usually comes from a small number of consistent effects rather than a different effect on every slide.

## What does not belong in video.yaml

Do not put these fields in a normal video document:

- font families or font sizes;
- text coordinates;
- image placement, crop, or visual effects;
- colors and background styling;
- FFmpeg filter names;
- codec or container settings;
- internal layer lists.

Those values belong in `template.yaml` or renderer configuration. Content-specific audio paths, durations, and sound-effect timings do belong in `video.yaml` because they describe the concrete content.

## Validation checklist

Before rendering, check:

1. `video.yaml` uses `version: 2` and references the video schema.
2. `template` matches the selected template's `template_id`; the CLI has either `template_file` or an explicit `-t` path.
3. Required metadata and at least one slide are present.
4. Every slide with a classic layout has an image and primary text; every slide with a custom layout supplies its variables.
5. Every audio and image path exists and is readable.
6. Sound-effect offsets fit into their containing sections.
7. The template contains the three default layouts, and every named layout the video selects.
8. Effect types are suitable for their layout elements.
9. Durations and transitions produce a positive effective duration.
10. The result is suitable for the target social platform.

See the included examples for complete documents:

- [`examples/simple/video.yaml`](../examples/simple/video.yaml)
- [`examples/normal/video.yaml`](../examples/normal/video.yaml)
- [`examples/complex/video.yaml`](../examples/complex/video.yaml)
- [`examples/simple/template.yaml`](../examples/simple/template.yaml)
- [`examples/normal/template.yaml`](../examples/normal/template.yaml)
- [`examples/complex/template.yaml`](../examples/complex/template.yaml)
- [`examples/showcase/`](../examples/showcase/): scene templates that carry timing, motion, sound, and transitions, so the video only picks scenes and fills variables
