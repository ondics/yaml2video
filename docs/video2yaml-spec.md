# Video2YAML v2 Specification

## 1. Scope

This document is the normative, human-readable description of:

- [`video.schema.json`](video.schema.json);
- [`template.schema.json`](template.schema.json).

The format is renderer-independent. It describes content, composition, logical layout, semantic effects, and audio events. It does not define FFmpeg commands, codecs, filter graphs, or a programming-language implementation.

The v2 core profile targets short social videos composed from ordered images, visible text, optional logos, background music, optional section audio, sound effects, transitions, and a final call to action.

## 2. Conformance

A conforming `video.yaml` document MUST:

- validate against `video.schema.json`;
- use `version: 2`;
- reference a non-empty template identifier;
- contain required metadata and at least one slide;
- use only properties defined by the schema.

A conforming `template.yaml` document MUST:

- validate against `template.schema.json`;
- use `version: 2`;
- contain a valid `template_id`;
- define output format, defaults, composition, and all three standard layouts;
- use only properties defined by the schema.

Schema validation does not replace semantic renderer validation. The renderer MUST additionally validate template lookup, content bindings, media availability, timing relationships, effect applicability, and output capabilities.

## 3. Common conventions

### 3.1 YAML and JSON Schema

YAML is the authoring format. JSON Schema is the formal validation language applied after YAML has been parsed into a JSON-compatible data model.

Each document begins with a `$schema` reference:

```yaml
$schema: ./video.schema.json
```

The current schema `$id` values are placeholders until the project publishes canonical schema URLs.

### 3.2 Unknown properties

The core schemas use `additionalProperties: false` at defined object levels. Unknown fields MUST be rejected rather than silently ignored.

### 3.3 Paths

Paths are non-empty strings. They may identify local files or renderer-supported URIs. The renderer resolves relative paths from the video document's location unless configured otherwise. Schemas do not verify that a path exists.

### 3.4 Durations and offsets

A duration is either a positive number representing seconds or a string such as `3s` or `3.5s`. A sound-effect `at` offset may additionally be zero, for example `0s`.

Zero and negative durations, minutes, and milliseconds are outside the v2 core syntax.

## 4. Video document

### 4.1 Top-level fields

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `$schema` | string | yes | Schema reference for editors and validators. |
| `version` | integer, constant `2` | yes | Public document version. |
| `template` | string | yes | Template identifier to resolve. |
| `metadata` | object | yes | Editorial and language metadata. |
| `assets` | object | no | Concrete logo and background music assets. |
| `content` | object | yes | Intro, slides, and outro content. |

### 4.2 Metadata

`metadata.title` and `metadata.language` are required. Optional fields are `audience`, `description`, unique `tags`, and the informational `target_duration_seconds`.

### 4.3 Assets and audio

The optional `assets` object supports:

```yaml
assets:
  logo: assets/logo.png
  music:
    path: assets/music.mp3
    volume: 0.3
    fade_in: 0.5s
    fade_out: 1s
```

`assets.music` is the background music track. Its volume is between `0` and `1`; fade values are positive durations. The renderer loops or trims it to the effective video duration.

Intro, slide, and outro objects may each contain one foreground `audio` track:

```yaml
audio:
  path: assets/slide-instruction.wav
  volume: 0.8
  fade_out: 0.2s
```

Each of those objects may also contain `sound_effects`:

```yaml
sound_effects:
  - path: assets/pop.wav
    at: 0s
    volume: 0.45
```

`at` is relative to the start of the containing section. A renderer MUST reject an event outside that section. Sound effects are mixed over music and foreground audio; they do not activate music ducking.

### 4.4 Content

`content.slides` is required and contains at least one item.

The optional `content.intro` may contain `image`, `title`, `subtitle`, `duration`, `audio`, and `sound_effects`. It MUST contain at least `title` or `image`.

Each slide requires `image` and `text` and may contain:

| Field | Rule |
| --- | --- |
| `id` | Optional identifier matching `^[A-Za-z0-9_-]+$`. |
| `image` | Required media path. |
| `text` | Required primary message. |
| `tip` | Optional practical instruction. |
| `label` | Optional short category label. |
| `alt_text` | Optional accessible image description. |
| `duration` | Optional positive duration. |
| `audio` | Optional foreground audio track. |
| `sound_effects` | Optional section-relative audio events. |

The optional `content.outro` supports `image`, `title`, `text`, `cta`, `duration`, `audio`, and `sound_effects`. Its `cta` is required.

## 5. Template document

### 5.1 Top-level fields

| Field | Type | Required | Meaning |
| --- | --- | --- | --- |
| `$schema` | string | yes | Schema reference. |
| `version` | integer, constant `2` | yes | Public document version. |
| `template_id` | identifier | yes | Name used by `video.template`. |
| `description` | non-empty string | yes | Human-readable purpose. |
| `format` | object | yes | Output canvas and frame rate. |
| `defaults` | object | yes | Theme, timing, media, and audio defaults. |
| `composition` | object | yes | Ordered section sequence. |
| `layout` | object | yes | Logical layouts for intro, slide, and outro. |

### 5.2 Format and defaults

`format` requires `aspect_ratio`, `width`, `height`, and `fps`. Supported aspect ratios are `9:16`, `1:1`, `16:9`, and `4:5`.

`defaults.theme` requires `background`, `text_color`, and `accent_color`. Optional values are `muted_color` and `panel_color`.

`defaults.typography` may define `font_family`, `title_size`, `body_size`, and `tip_size`.

`defaults.timing` requires intro, slide, and outro durations plus a default transition.

`defaults.media` may define a default `cover` or `contain` fit and default placement.

`defaults.audio` may define:

| Field | Meaning |
| --- | --- |
| `default_volume` | Default volume between `0` and `1`. |
| `fade_in` | Default positive fade-in duration. |
| `fade_out` | Default positive fade-out duration. |
| `normalize` | Whether the renderer should normalize audio loudness. |
| `ducking` | Optional reduction of background music while foreground audio plays. |

`ducking.amount` is between `0` and `1`; `attack` and `release` are positive durations.

### 5.3 Composition

`composition.sections` is an ordered array. Each section requires `id`, `role`, and `source`:

- `intro` MUST use `content.intro`;
- `slide` MUST use `content.slides` and `repeat: true`;
- `outro` MUST use `content.outro`.

Optional fields are `duration_from: item.duration`, `default_duration`, and `transition_out`. A renderer MUST reject duplicate section IDs and a transition on the final section.

### 5.4 Transitions

Transitions have `type` and a positive `duration`. The core vocabulary is:

```text
cut, fade, dissolve,
wipe-left, wipe-right, wipe-up, wipe-down,
slide-left, slide-right, slide-up, slide-down,
circle-open, circle-close, radial, pixelize,
zoom-in, cover, reveal, blur
```

These names are semantic. The renderer maps them to its implementation. FFmpeg-based renderers may map many of them to `xfade` transitions, subject to compatible input stream parameters.

## 6. Layout and effect language

The template requires `layout.intro`, `layout.slide`, and `layout.outro`. Element names bind to content fields with the same name. Text and media elements require a semantic `placement`.

The slide layout may additionally contain a generated `progress` element. It displays the current slide position and total slide count.

### 6.1 Text and media effects

An element may contain an ordered `effects` array. The first effect is applied first. The core effect types are:

| Type | Intended element | Parameters |
| --- | --- | --- |
| `text_shadow` | text | `color`, `opacity`, `offset_x`, `offset_y`, `blur` |
| `text_glow` | text | `color`, `opacity`, `blur` |
| `text_outline` | text | `color`, `opacity`, `width` |
| `glass_panel` | text | `color`, `opacity`, `blur`, `padding` |
| `pan_zoom` | image | `from_scale`, `to_scale`, `from_anchor`, `to_anchor` |
| `color_filter` | image | `preset`, `intensity` |
| `vignette` | image | `color`, `opacity` |
| `blur` | image | `radius` |
| `fade` | text or image | `fade_in`, `fade_out` |

The `color_filter` presets are `grayscale`, `sepia`, `warm`, `cool`, and `high-contrast`.

`glass_panel` creates a translucent tinted panel behind the associated text. When `blur` is greater than zero, the renderer blurs the background region behind the panel before compositing it.

The schemas validate the effect vocabulary and parameter types. The renderer additionally validates whether an effect is appropriate for the element to which it is attached.

### 6.2 Placement and style

Placement anchors are:

```text
top-left, top-center, top-right
center-left, center, center-right
bottom-left, bottom-center, bottom-right
```

Text styles may define font family, font size, weight, color, alignment, and line spacing. Media elements may define `cover` or `contain`, shape, and maximum width/height ratios.

## 7. Composition semantics

The renderer applies values in this order:

```text
renderer capability default
  < template default
  < content-level override
```

For a repeated slide section:

```text
slide.duration
  < section.default_duration
  < defaults.timing.slide_duration
```

The first available value has the highest precedence. The renderer sequences foreground audio and sound effects against the resulting timeline.

## 8. Timeline and output boundary

The first section starts at time zero. An outgoing transition overlaps the following section:

```text
effective duration = sum(section durations) - sum(outgoing transition durations)
```

The public format does not prescribe codecs, containers, pixel formats, command-line arguments, or arbitrary FFmpeg filter graphs. A renderer MUST expose a dry-run or validation mode that reports the resolved format, effective duration, ordered sections, bindings, effects, audio events, warnings, and errors.

## 9. Versioning

The renderer MUST reject unsupported major versions rather than guessing their meaning. Backward-compatible additions may be introduced in a minor revision. New capabilities should first be documented as namespaced renderer extensions and promoted into the core schema only after their semantics are stable.
