# Changelog

All notable changes to the project are documented here.

## [v2] - Unreleased

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

### Documentation

- Added [`docs/user-guide.md`](docs/user-guide.md) for authoring `video.yaml` and `template.yaml` files.
- Added [`docs/video2yaml-spec.md`](docs/video2yaml-spec.md) as the normative human-readable format specification.
- Extended [`docs/renderer-spec.md`](docs/renderer-spec.md) with timeline, effect, audio, capability, and dry-run requirements.
- Documented the separation between semantic YAML effects and renderer-specific FFmpeg implementation details.

### Compatibility and scope

- The v2 format remains renderer-independent and does not expose FFmpeg filter graphs, codec arguments, or container settings.
- The initial profile targets short, attractive social-media videos based on images, text, transitions, and audio.
- The renderer must perform semantic validation in addition to JSON Schema validation, including media availability, timing, effect applicability, and FFmpeg capability checks.
