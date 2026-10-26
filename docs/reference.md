# YAML Format and Rendering Reference

This document describes the public YAML format and its behavior in yaml2video. The [video schema](video.schema.json) and [template schema](template.schema.json) define the exact allowed fields and constraints; the [user guide](user-guide.md) provides authoring examples.

## Documents and validation

A video supplies content and media; a template supplies design, composition, and output dimensions. Both require `$schema` and `version: 2`. Unsupported versions and unknown properties are rejected.

The CLI takes a video file and selects a local template YAML file from optional `video.template_file` or an explicit `-t` override:

```sh
yaml2video -n video.yaml                  # validate and preview using template_file
yaml2video video.yaml                     # render using template_file
yaml2video -t template.yaml video.yaml     # override template_file
```

If neither `-t` nor `video.template_file` is supplied, rendering errors. `video.template` remains required and must equal the selected `template.template_id`. It is an identity check, not automatic template lookup. The API's `LoadBytes` still uses explicitly supplied template bytes; it does not look up `template_file` on the server. `$schema` is an editor reference; yaml2video validates against its bundled schemas rather than downloading that URL.

Schema validation checks structure. Composition and render-plan validation additionally check bindings, media paths, timing, and supported effect combinations. Passing schema validation alone does not guarantee a successful render.

## Video document

| Field | Requirement and purpose |
| --- | --- |
| `template` | Required template identifier; must match the selected template's `template_id`. |
| `template_file` | Optional local template YAML path for the CLI; overridden by `-t`. |
| `metadata` | Required `title` and `language`; optional `audience`, `description`, unique `tags`, and informational `target_duration_seconds`. |
| `assets` | Optional `logo` path and background `music` track. |
| `content` | Required nonempty `slides` array; optional `intro` and `outro`. |

Content fields:

| Item | Required with a classic layout | Optional |
| --- | --- | --- |
| `intro` | At least `title` or `image` | `title`, `image`, `subtitle`, `duration`, `audio`, `sound_effects`, `layout`, `variables`, `transition_out`, `transition_in` |
| slide | `image`, `text` | `id`, `label`, `tip`, `alt_text`, `duration`, `audio`, `sound_effects`, `layout`, `variables`, `transition_out`, `transition_in` |
| `outro` | `cta` | `image`, `title`, `text`, `duration`, `audio`, `sound_effects`, `layout`, `variables`, `transition_out`, `transition_in` |

`layout` selects one of the template's named layouts for that item. `variables` supplies values for a custom layout's `$(NAME)` bindings (strings, numbers, or booleans). With a custom layout, an item needs only what its layout's variables refer to. See [layouts](layouts.md).

The video cannot specify layout layers, typography, visual effects, codecs, or FFmpeg filters (outside [advanced mode](advanced.md)). Slide `alt_text` is retained in the render plan and shown in CLI dry-run output; it is not burned into the video.

## Template document

Besides `$schema` and `version`, every template requires:

| Field | Purpose |
| --- | --- |
| `template_id`, `description` | Identity and human-readable purpose. |
| `format` | `aspect_ratio`, `width`, `height`, and `fps`. Ratios: `9:16`, `1:1`, `16:9`, `4:5`. Optional `crf` (0–51, default 16) sets the final H.264 quality; lower is better and larger. |
| `defaults` | Required `theme` and `timing`; optional `typography`, `media`, and `audio`. |
| `composition` | Ordered `sections` array, with at least two entries. |
| `layout` | Required `intro`, `slide`, and `outro` default layouts, even when optional content is absent, plus any number of named layouts. Each is classic, custom, or the name of another layout. See [layouts](layouts.md). |

- `theme`: required `background`, `text_color`, `accent_color`; optional `muted_color`, `panel_color`.
- `timing`: required `intro_duration`, `slide_duration`, `outro_duration`; optional `transition`.
- `typography`: optional `font_family`, `fonts`, `title_size`, `body_size`, `tip_size`. See [fonts](#fonts).
- `media`: optional `default_fit` and `default_position`; explicit element placement remains required by the schema.
- `audio`: optional `default_volume`, `fade_in`, `fade_out`, `normalize`, and `ducking`.

## Composition and bindings

Each section requires a unique `id`, a `role`, and its matching `source`:

| Role | Source | Behavior |
| --- | --- | --- |
| `intro` | `content.intro` | One scene if intro content exists; otherwise skipped. |
| `slide` | `content.slides` | Requires `repeat: true`; one scene per slide, in array order. |
| `outro` | `content.outro` | One scene if outro content exists; otherwise skipped. |

Composition must include a slide section. Optional section fields are `default_duration`, `duration_from: item.duration`, `transition_out`, `transition_in`, and `layout` (the default layout for the section's scenes). Intro and outro cannot repeat. Generated scene IDs must also be unique.

The renderer processes sections in order, picks each scene's layout (the item's `layout`, else the section's, else the role default), binds content to it, resolves defaults, and builds the scene timeline. Custom layouts bind through `$(NAME)` variables ([layouts](layouts.md)). Classic layouts bind as follows:

- Text elements bind to content fields with the same name; absent content or layout elements are omitted. `visible: false` hides an element.
- `logo` binds to `assets.logo` when the layout includes it.
- Slide `index` is generated as `current / total`; `progress` displays the slide's position in the sequence.
- Intro and outro images render as full-canvas backgrounds; their schemas do not expose a separate image layout element.

Content-level timing and audio values override template defaults. Scene duration uses the **first available** value:

```text
item.duration → layout.duration (custom layouts) → section.default_duration → defaults.timing.<role>_duration
```

The current renderer honors `item.duration` whether or not `duration_from` is explicitly present.

## Timing and transitions

Durations are positive seconds, written as numbers (`3.5`) or strings (`3.5s`). Minutes and millisecond suffixes are unsupported. Sound-effect offsets may be zero.

Each scene uses its content item's `transition_out`, else its custom layout's, else its section's, falling back to `defaults.timing.transition`; `transition_in` resolves the same way without the fallback (see [scene templates](layouts.md#scene-templates)). Both are optional: without a transition, scenes cut. For repeated slides, this also applies between slides. `transition_out` may also be a list, which rotates across the section's scenes. A section's `transition_in` sets the transition into each of its scenes. When a boundary has both an out and an in transition, the out transition's blend is used, and each supplies its own side effects (see [advanced mode](advanced.md#transitions)). A transition given on only one side covers both. The last rendered scene has no outgoing transition; an explicit `transition_out` on the final composition section, or `transition_in` on the first, is rejected.

Built-in transition types:

```text
cut, fade, dissolve, fade-black, fade-white, fade-grays, fade-fast, fade-slow, distance,
wipe-left, wipe-right, wipe-up, wipe-down, wipe-tl, wipe-tr, wipe-bl, wipe-br,
slide-left, slide-right, slide-up, slide-down,
smooth-left, smooth-right, smooth-up, smooth-down,
cover (= cover-left), cover-left, cover-right, cover-up, cover-down,
reveal (= reveal-left), reveal-left, reveal-right, reveal-up, reveal-down,
circle-open, circle-close, circle-crop, rect-crop, radial, pixelize, zoom-in, blur,
horz-open, horz-close, vert-open, vert-close, diag-tl, diag-tr, diag-bl, diag-br,
slice-left, slice-right, slice-up, slice-down, squeeze-h, squeeze-v,
wind-left, wind-right, wind-up, wind-down
```

A transition type may also name a custom transition defined in `advanced.transitions`. Built-in blends require a `duration`, overlap adjacent scenes, and must be strictly shorter than both. A `cut` has no overlap; any `duration` on it is ignored.

```text
next scene start = current scene end - outgoing overlap
effective duration = sum(scene durations) - sum(overlaps)
```

## Layout and visual effects

Every classic layout has a `background`, either a color or an image object. Its required elements are intro `title`, slide `image` and `text`, and outro `cta`; additional elements are defined in the template schema. Custom layouts list their elements freely and stack them in order ([layouts](layouts.md)); the placement, text, media, and effect rules below apply to their elements too.

Elements require `placement.anchor`, optionally with pixel offsets `offset_x` and `offset_y`. Anchors are `top-left`, `top-center`, `top-right`, `center-left`, `center`, `center-right`, `bottom-left`, `bottom-center`, and `bottom-right`.

Text supports font family, size, weight, color, alignment, maximum width, and maximum line count. Text exceeding `max_lines` is rejected, not truncated. `weight` is a name (`thin`, `extralight`, `light`, `normal`/`regular`, `medium`, `semibold`, `bold`, `extrabold`, `black`: 100–900) or a number from 1 to 1000, as in CSS; a font without that weight uses its nearest one. The schema also permits `line_spacing`, but the current renderer rejects nonzero line spacing. Media supports `cover`/`contain`, maximum width/height ratios, and `rectangle`, `rounded-rectangle`, or `circle` shapes.

### Image backgrounds and fit alignment

`defaults.theme.background` and any classic or custom layout's `background` accept either an existing color string or an image object:

```yaml
background:
  path: assets/photo.jpg
  fit: cover
  left: 0.5
  top: 0
```

The image fills the canvas behind all layout elements. Paths resolve relative to the video YAML and support the same `$(VARIABLE)` bindings as image elements. `fit` defaults to `defaults.media.default_fit`, then `cover`. An optional `color` sets the underlay visible through transparency or `contain` padding (default: black). Custom layouts without a background inherit the theme's background, including images.

Image backgrounds, classic media elements, and custom/track image clips accept `left`, `right`, `top`, and `bottom` next to `fit`. These position the image **inside its fitted box**, without moving or resizing the box:

- Values range from **0 to 1** (0–100%). `left: 0` aligns left, `left: 0.5` centers, and `left: 1` aligns right. `right` measures from the opposite side (`right: 0` aligns right).
- `top` and `bottom` work the same way vertically: `top: 0` keeps the top edge; `bottom: 0` keeps the bottom edge.
- With `cover`, the fractions select which part of the scaled image remains after cropping. With `contain`, they distribute the unused space around the image.
- Specify at most one of `left`/`right` and one of `top`/`bottom`. Omitted axes retain existing behavior: centered cropping for `cover`, placement-anchor alignment for `contain` (centered for backgrounds).

### Trimming image and video layers

In scene-based YAML (`scenes[].layers[]`), both `type: image` and `type: video` accept `trim`. Values are fractions of the original source dimensions removed from each edge, before scaling and fitting:

- `trim: 0.1`: remove 10% from every edge.
- `trim: [0.1, 0.2]`: remove 10% from the top and bottom, 20% from the left and right.
- `trim: [0.1, 0.2, 0.05, 0.15]`: remove fractions in **top, bottom, left, right** order.

Values must be nonnegative, and each pair of opposite edges must total less than 1. Omitting `trim` leaves the source unchanged. The remaining region is fitted into the layer's dimensions using `cover` or `contain`.

```yaml
scenes:
  - id: cropped-photo
    duration: 3
    layers:
      - type: image
        path: assets/photo.png
        trim: [0.1, 0.1, 0.2, 0.05]
        width: 600
        height: 400
        fit: contain
        transform: center
```

### Fonts

Text is measured with the font file that draws it, shaped (kerning and ligatures included) the way libass lays it out, so wrapping, text boxes, highlights, and carets fit the actual glyphs. A `font_family` resolves, in this order, to:

1. a font file in `fonts/` next to the video YAML, or in the files and directories listed in `defaults.typography.fonts` (relative to the video YAML);
2. an installed font;
3. a [Google Fonts](https://fonts.google.com) family, downloaded on first use and cached;
4. a bundled Go font (Go Mono for families whose names look monospaced, Go otherwise), with a warning.

Variable fonts get exactly the weight a text asks for and, when they have an optical size axis, the optical size of the text's font size, as browsers choose it: a 120 px headline in Bricolage Grotesque gets its display cut, a 28 px caption its text cut. libass can't set a font's axes, so each weight and size becomes a static instance: downloaded as one from Google Fonts, or made from the variable font file for project and installed fonts (cached in the cache directory).

Fonts that aren't installed, and all instances, reach FFmpeg through the render's work directory under family names of their own, so libass draws exactly the font that measured the text, and a render looks the same on any machine that resolves its fonts the same way. Families that aren't on Google Fonts (for example commercial or Fontshare fonts) belong in `fonts/`.

| Environment variable | Effect |
| --- | --- |
| `YAML2VIDEO_OFFLINE=1` | Never download fonts. |
| `YAML2VIDEO_SYSTEM_FONTS=off` | Ignore installed fonts: only project, downloaded, and bundled fonts. |
| `YAML2VIDEO_CACHE_DIR` | Where downloaded fonts, font instances, Google's family list, and the installed-font index are kept (default: the user cache directory). |

An element's `effects` array specifies semantic effects, not arbitrary filter expressions:

| Effect | Element | Parameters |
| --- | --- | --- |
| `text_shadow` | Text | `color`, `opacity`, `offset_x`, `offset_y`, `blur` |
| `text_glow` | Text | `color`, `opacity`, `blur` |
| `text_outline` | Text | `color`, `opacity`, `width` |
| `glass_panel` | Text | `color`, `opacity`, `blur`, `padding` |
| `pan_zoom` | Image | `from_scale`, `to_scale`, `from_anchor`, `to_anchor` |
| `color_filter` | Image | `preset`, `intensity` |
| `vignette` | Image | `color`, `opacity` |
| `blur` | Image | `radius` |
| `fade` | Text or image | `fade_in`, `fade_out` |

Image filters follow array order. Text effects are implemented through ASS styling and compositing, not a general-purpose sequential filter graph. Color presets are `grayscale`, `sepia`, `warm`, `cool`, and `high-contrast`. A glass panel tints the text area, adds padding, and optionally blurs the background behind it.

Current restrictions include black-only vignettes, at most one glass panel per text element, and no text fade combined with a glass panel. Effects attached to the wrong element type and fades longer than the scene are rejected. Schema-valid colors may still be unsupported by the text renderer.

## Audio

Music, foreground `audio`, and `sound_effects` use a required `path` and optional `volume`, `fade_in`, and `fade_out`. Sound effects additionally require `at`, relative to their scene's start.

- Track settings override `defaults.audio`; volume is between `0` and `1`, falling back to `1`. Unspecified fades are absent.
- Background music loops or trims to the complete effective timeline. `normalize` controls music loudness normalization and defaults to enabled.
- Foreground audio begins with its scene. Sound effects start at `at` and mix with foreground audio and music.
- Sound effects must start before the scene ends. Audio fades must fit within the remaining scene duration, or the full video duration for music.
- `ducking.enabled: true` reduces music while foreground audio is active; sound effects do not trigger ducking. Defaults are `amount: 0.65`, `attack: 0.15s`, and `release: 0.4s`. The FFmpeg backend accepts attack from `0.00001s` to `2s` and release from `0.00001s` to `9s`.

## Paths, output, and dry-run limits

The v2 loader accepts local files only and rejects URI paths. Relative `video.template_file` paths resolve from the video YAML's directory by default, or from `--work-dir` when that option is explicitly supplied. A relative `--work-dir` resolves from the current working directory; absolute `template_file` paths are unaffected. An explicit `-t` overrides `template_file`, and relative `-t` paths resolve from the current working directory regardless of `--work-dir`.

Relative media paths always resolve from the video YAML's directory in the CLI, independently of `--work-dir`. The API instead supplies a media base directory; see [server storage and media provisioning](server-api.md).

Media paths are checked for readability and regular-file status during loading. Decodability is left to FFmpeg; these checks do not guarantee a supported media format.

The CLI writes `output.mp4` next to the video YAML unless `-o` overrides it. Intermediate files go into `.yaml2video` beside the YAML unless `--work-dir` overrides it. Dimensions and frame rate come from the template; codecs and filter graphs are implementation details, not YAML fields.

`-n` reports output settings, effective duration, scene start/end times, text/media bindings, effects, audio events, and generated FFmpeg commands without encoding. It does not verify media decoding or every FFmpeg capability; fonts are resolved (and downloaded if needed) because text layout depends on them. Actual text rendering requires an FFmpeg build with the `ass` filter.

Unknown fields are not an extension mechanism: new format fields require schema and renderer support. Named output profiles and automatic template discovery are not current features.

## Advanced mode

Templates and videos may add an optional `advanced` block with element keyframes, keyframe and effect presets, tracks of timed clips and effect layers, and a master loudness stage. Without it, rendering is unchanged. See [advanced mode](advanced.md).
