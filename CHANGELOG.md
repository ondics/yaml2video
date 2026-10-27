# Changelog

All notable changes to the project are documented here.

## 2026-10-27 V1.0.0

### Added

- Named layouts (`docs/layouts.md`):
  - `layout:` holds any number of named layouts; `intro`, `slide`, and `outro` remain the role defaults, and a layout entry may be the name of another layout;
  - content items choose a layout with `layout:`, and template sections set a default for their scenes with `layout:`;
  - custom layouts are ordered element lists (`text`, `image`, `rect`, `circle`, `progress`, `audio`, `effect`) with no predefined fields; elements stack in list order and can be timed, keyframed, named with `id`, and marked `optional`;
  - `$(NAME)` variables (also `${NAME}` and `$(NAME|fallback)`) read the item's new `variables:` map, its fields, and generated values such as `$(index)` and `$(logo)`.
- Optional advanced mode (`advanced` in templates and videos); documents without it render unchanged:
  - keyframes with nine easings for position, scale, rotation, opacity, and audio volume;
  - template keyframes for standard layout elements (`animate`);
  - named keyframe and effect presets, shared between templates and videos;
  - tracks of image, shape, text, progress, audio, and effect clips, attached to the video, a role, a layout, or one scene, optionally delayed by a track `offset`, with `$(NAME)` variables;
  - `element` clips that animate or modify (`set`) a scene's layout elements in place, for a time window;
  - stackable effect layers: blur, color, color filters, vignette, glow, RGB split, grain, tint, shake, and keyframed camera moves;
  - a master bus that loudness-normalizes the final mix;
  - custom transitions (`advanced.transitions`): a built-in blend, a custom per-pixel expression, or a cut, with effect layers across the boundary, on the out side, or on the in side; plus per-scene overrides (`scene_transitions`).
- Transitions:
  - 41 more built-in xfade transitions (fade-black, smooth-*, slice-*, wind-*, squeeze-*, diag-*, cover-/reveal-* in all directions, and more);
  - `transition_out` lists rotate across a section's scenes, and the new `transition_in` sets transitions into a section's scenes; a transition given on one side covers both;
  - `defaults.timing.transition` is optional, and without any transition scenes cut; a transition's `duration` is optional in the schema (built-in blends still need one, custom transitions bring their own).
- Added the layouts and advanced examples, `docs/layouts.md`, and `docs/advanced.md`.
- Added the showcase example: a promo for yaml2video built from scene templates. The advanced example now keeps nearly everything in its template; its video only overrides a transition, disables a track, and adds one scene-specific track.
- Folder clips (`type: folder`): groups of clips, nested freely, in layouts and tracks. Children keep their own timing (from the folder's start), keyframes, and effects; the folder's keyframes, `opacity`, and `effects` transform the composited group around a `pivot`. Element clips reach children at any depth. Each folder draws on a canvas sized to what its children can reach, which may extend past the frame.
- Text reveal: `reveal: { chars_start, chars_end, lines_start, lines_end }` on text, and the same four keyframe properties, as counts of characters and lines or as percentages (`"100%"`). Hidden characters and lines keep their space, so animating `chars_end` types a text without moving it.
- Scene templates: custom layouts can set `duration`, `transition_out`, `transition_in`, and `tracks` for every scene that uses them; content items can override `transition_out` and `transition_in`.
- Text style `letter_spacing` and `word_spacing`.
- `width` and `height` keyframes for rect, circle, and image clips: they resize around the center without stretching corner radii or resampling images.
- Fonts are resolved to files and text is measured with them (HarfBuzz shaping, as libass draws it) instead of estimated: from `fonts/` next to the video or `defaults.typography.fonts`, installed fonts, any Google Fonts family (downloaded and cached on first use), or a bundled Go font. Fonts that aren't installed are handed to FFmpeg with the render. `YAML2VIDEO_OFFLINE`, `YAML2VIDEO_SYSTEM_FONTS=off`, and `YAML2VIDEO_CACHE_DIR` control this.
- Text `weight` takes any CSS weight, by name (`thin` to `black`) or number (1–1000). Variable fonts are drawn at exactly that weight and at the optical size of the text: Google Fonts serves the instance, and project or installed variable fonts are instanced into static fonts, since libass can't set axes itself.
- `cover` and `caret` place rect, circle, and image clips on a text's measured glyphs: a highlight around the characters a text shows, or a cursor that follows a text as it types.
- Text `match` (show only the characters a regular expression covers, keeping the others' space) and `replace` (rewrite the text before layout), so one text can be layered into syntax colors or a cursor grid.
- Template `format.crf` sets the quality of the final encode.

### Changed

- Video items no longer require `image`/`text` (slides), `title` or `image` (intro), or `cta` (outro) in the schema; the requirement now comes from the selected layout and is checked when loading.
- Track text renders at its position in the track rather than above all of the track's other clips.
- Keyframed text is sampled every frame instead of every 50 ms, so motion and reveals follow their curves exactly.
- Text wraps at measured widths instead of an estimated 0.4 em per character, and ASS documents turn kerning on, as the measurement does.
- The final encode uses CRF 16 (was x264's default 23), the slow preset, and adaptive quantization, which removes banding and smearing on flat graphics and small text.
- Text lines that fit keep their spacing, including indentation; leading and repeated spaces render as hard spaces.
- A layer whose scale keyframes reach 0 is invisible instead of a one-pixel sliver.
- Loudness normalization of the master and of music is two-pass: the render measures first and applies one fixed gain (plus a peak limiter on the master), instead of loudnorm's dynamic mode, which pumped through quiet passages and fades.
- Advanced text no longer lets libass re-wrap lines (`WrapStyle: 2`); a line scaled past the frame by keyframes stays one line.
- Grain effects no longer add noise to the alpha channel.

### Fixed

- Reusing the same audio file several times in one video no longer fails to build the FFmpeg graph.
- `allOf`, `oneOf`, and `anyOf` schema failures now report the underlying error of the closest alternative; `if`/`then`/`else` is supported.
- A `cut` followed by a blended transition no longer fails with an xfade timebase mismatch.
- Explicit line breaks in text (`"line one\nline two"`) are kept instead of being collapsed into spaces; each line is wrapped on its own and counts toward `max_lines`.
- Scene renders no longer deadlock at random. Generated sources (colors, shapes, folder canvases) and looped still images are now source filters inside the filter graph instead of separate FFmpeg inputs, whose reader threads could block each other on full queues. FFmpeg also runs with `-nostdin`.
- Image, shape, and folder clips end on the same frame as text: their generated sources were a few microseconds longer than the clip, one extra frame when the clip ended on a frame boundary.
- The `glow` effect blends in RGB; it tinted whole frames purple when screen-blending YUV chroma.
- Element clips with `set` no longer multiply each other's variants: a variant from an earlier clip is only split again where its window overlaps a later clip's. Eight sequential `set` clips on one element now add eight layers instead of 255.

## Prerelease

### Added

- Introduced a two-document video format:
  - `video.yaml` contains localized video content, media assets, timing, and audio events.
  - `template.yaml` contains output format, composition, layout, typography, styling, effects, and defaults.
- Added JSON Schema Draft 2020-12 validation for both document types:
  - `docs/video.schema.json`
  - `docs/template.schema.json`
- Added three complete video examples for simple, standard, and complex office-mobility videos.
- Added three matching templates for vertical social-media videos.
- Added the semantic effect language for:
  - text shadows, glows, and outlines;
  - translucent and blurred glass panels;
  - image pan-and-zoom;
  - color filters, vignette, blur, and fade effects;
  - slide progress indicators.
- Added semantic slide transitions including fade, dissolve, wipe, slide, radial, pixelize, zoom, cover, reveal, and blur variants.
- Added section-level audio support for intro, slide, and outro content.
- Added multiple sound effects per section with relative start offsets.
- Added template-level audio normalization and optional background-music ducking for foreground audio.

### Breaking changes

- Old schemas and templates are no longer compatible with v2.
