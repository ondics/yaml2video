# Renderer Specification

## Status

This document defines the minimum renderer behavior for version 2 of the public YAML format. It is independent of a programming language, multimedia library, or command-line tool.

The renderer may use FFmpeg, but FFmpeg filters and command-line arguments are implementation details. Semantic effects are translated by the renderer into FFmpeg filters or an equivalent implementation.

## Inputs

The renderer accepts:

1. one validated `video.yaml` document;
2. one validated `template.yaml` document whose `template_id` matches `video.template`;
3. a media resolver for local paths and renderer-supported URIs.

Both documents are validated against their JSON Schemas before composition. Schema validation is structural; renderer validation additionally checks media availability, bindings, timing, effect applicability, and supported output capabilities.

## Composition model

The renderer MUST compose the documents in this order:

1. Resolve the template by `video.template`.
2. Validate that the template and video versions are compatible.
3. Resolve concrete assets from `video.assets` and content items from `video.content`.
4. Execute `template.composition.sections` in order.
5. For each section, select the corresponding layout from `template.layout`.
6. Bind content values to logical layout elements.
7. Apply template defaults, then content-level timing and audio overrides.
8. Resolve semantic visual effects and audio events.
9. Validate the resulting render plan.
10. Pass the render plan to the selected renderer.

The renderer MUST NOT expose `layers`, FFmpeg filter names, codec arguments, or pixel coordinates in the public content document.

## Binding rules

The v2 profile defines these standard bindings:

| Section role | Source | Available content values |
| --- | --- | --- |
| `intro` | `content.intro` | `image`, `title`, `subtitle`, `duration`, `audio`, `sound_effects` |
| `slide` | `content.slides` | `image`, `label`, `text`, `tip`, `alt_text`, `duration`, `id`, `audio`, `sound_effects` |
| `outro` | `content.outro` | `image`, `title`, `text`, `cta`, `duration`, `audio`, `sound_effects` |

For a repeated `slide` section, one rendered scene is created for every item in `content.slides`.

Logical element names in a layout map directly to content values with the same name. An optional element with no content value is omitted. The `logo` element binds to `assets.logo`. The `index` element is generated as `current item / total items`. The optional `progress` element receives the current item and total item count.

## Defaults and overrides

Defaults are applied by semantic field, not by replacing an entire top-level object:

```text
renderer capability default
  < template default
  < content-level override
```

The video document may override content-specific timing and audio settings. It must not repeat layout, typography, effects, or output-format settings for normal use.

For a slide section, `duration_from: item.duration` means:

```text
slide.duration if present
otherwise section.default_duration if present
otherwise template.defaults.timing.slide_duration
```

## Timeline and transitions

The effective timeline is the ordered sum of section durations. An outgoing transition overlaps the following section:

```text
effective duration = sum(section durations) - sum(outgoing transition durations)
```

The renderer MUST reject zero or negative durations, transitions longer than either adjacent section, a transition on the final section, and a sound effect whose `at` offset is outside its containing section.

The template transition vocabulary is semantic and includes `cut`, `fade`, `dissolve`, directional `wipe` and `slide`, `circle-open`, `circle-close`, `radial`, `pixelize`, `zoom-in`, `cover`, `reveal`, and `blur`. The renderer maps these names to the available implementation. FFmpeg's `xfade` family requires compatible input streams, including matching dimensions, frame rate, pixel format, and time base.

## Visual behavior

The renderer MUST support:

- a solid background color per section;
- an image or logo with `cover` or `contain` fitting;
- text with placement anchor, maximum width, maximum line count, alignment, color, weight, and font size;
- optional text elements that disappear when their content value is absent;
- the semantic effect language defined below;
- optional slide progress bars;
- the template's configured section transitions.

### Effect language

Effects are ordered. The renderer MUST apply them in the order in which they occur in an element's `effects` array.

| Effect | Intended element | Meaning |
| --- | --- | --- |
| `text_shadow` | text | Offset shadow with color, opacity, and blur. |
| `text_glow` | text | Blurred colored text mask behind the text. |
| `text_outline` | text | Colored text stroke. |
| `glass_panel` | text | Translucent tinted panel with optional background blur and padding. |
| `pan_zoom` | image | Camera movement between scale and anchor values. |
| `color_filter` | image | Named grayscale, sepia, warm, cool, or high-contrast treatment. |
| `vignette` | image | Dark or colored edge treatment. |
| `blur` | image | Blur with a semantic radius. |
| `fade` | text or image | Element fade-in and/or fade-out. |

`glass_panel` is attached to a text element. It covers the text bounds, optionally blurs the background behind that area, applies a translucent tint, and adds padding. The renderer MUST reject an effect that is not meaningful for its element type; for example, `pan_zoom` is valid for an image element but not for text.

The public schema deliberately does not expose arbitrary FFmpeg expressions or `filter_complex` fragments. A renderer may report a capability error when its FFmpeg build cannot provide an effect, rather than silently ignoring it.

## Audio behavior

If `video.assets.music` is present, the renderer uses it as background music for the complete effective video duration, looping or trimming it as necessary.

The v2 core profile also supports:

- one foreground audio track per intro, slide, or outro, beginning with that section;
- zero or more `sound_effects` per section, positioned relative to the section start;
- per-track volume and fade-in/fade-out values;
- template-level audio normalization;
- optional music ducking while foreground audio is active.

The renderer sequences section audio tracks according to the resolved timeline. Sound effects are mixed over section audio and background music. If ducking is enabled, background music is reduced while foreground audio is present. Sound effects do not activate ducking.

## Media and paths

Paths are resolved relative to the video document unless renderer configuration specifies another base directory. The renderer MUST report missing, unreadable, or unsupported media before final rendering. Remote URIs are optional capabilities and must be documented by the implementation.

## Accessibility

`alt_text` is retained in the render plan for accessibility metadata and diagnostics. A renderer may use it to generate an external asset manifest or accessibility report. The core profile does not require it to be burned into the video.

## Output contract

The renderer MUST expose:

- the resolved output format;
- the effective duration;
- the ordered section list and their start/end times;
- resolved effects and audio events;
- warnings and errors;
- the final media output or a dry-run render plan.

The renderer SHOULD provide validation or dry-run mode that performs schema, binding, timing, media, effect, and capability checks without invoking the final encoder.

The output container, video codec, audio codec, pixel format, and platform-specific metadata are renderer configuration, not content fields. Named output profiles such as `social-vertical`, `social-square`, and `social-landscape` are recommended.

The renderer SHOULD check installed FFmpeg capabilities before rendering. Text rendering may depend on the installed font libraries, and advanced transitions or audio processing may not be available in every FFmpeg build.

## Versioning and extensions

The public documents use integer major versions. A renderer MUST reject an unsupported major version rather than guessing its meaning.

Backward-compatible additions may be introduced in a minor specification revision. New capabilities should first be documented as namespaced renderer extensions and promoted into the core schema only after their semantics are stable.

An extension MUST be namespaced and documented. It must not change the meaning of existing fields.
