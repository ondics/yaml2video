# Layouts: Named, Classic, and Custom

A template's `layout:` block holds any number of named layouts. Three names are special: `intro`, `slide`, and `outro` are the default layouts for their roles. Every other key is a named layout that a section or a single content item can choose. The [layouts example](../examples/layouts/) shows everything on this page.

```yaml
layout:
    intro: { ... }            # default for the intro
    slide: { ... }            # default for every slide
    outro: quote              # a name: the outro uses the "quote" layout
    slide-image-low: { ... }  # named classic slide layout
    stat: [ ... ]             # named custom layout
    quote: [ ... ]
```

Layout names match `^[A-Za-z0-9][A-Za-z0-9_-]*$`.

## Choosing a layout

A scene uses the first layout found among:

1. the content item's `layout:` (in the video),
2. its section's `layout:` (in the template's `composition`),
3. the role default (`intro`, `slide`, or `outro`).

```yaml
# video.yaml
content:
    slides:
        - image: assets/schultern.png       # default "slide" layout
          text: "Schultern kreisen"
        - layout: stat                       # a named custom layout
          variables: { NUMBER: "8", UNIT: "Stunden", CAPTION: "…" }
```

A layout entry may also be just the name of another layout (an alias), so `slide: stat` makes `stat` the default slide layout.

## Three kinds of layout

**Classic** layouts are the role's fixed elements (`title`, `subtitle`, `logo`, `image`, `label`, `text`, `tip`, `index`, `progress`, `cta`), filled from the content item's fields of the same name, as described in the [reference](reference.md#layout-and-visual-effects). A named classic layout is a slide layout and can only be used by slides. A classic slide needs `image` and `text`, a classic intro needs `title` or `image`, and a classic outro needs `cta`.

**Custom** layouts are a list of elements with nothing predefined: no default title, text, image, or tip. Write either the list directly or an object with a `background` and `elements`:

```yaml
stat:
    background: "#7DE2A8"           # optional; defaults to the theme background
    elements:
        - type: text
          id: number
          text: $(NUMBER)
          placement: { anchor: center, offset_y: -180 }
          style: { font_size: 300, weight: bold, color: "#101820", align: center }
        - type: rect
          width: 160
          height: 10
          color: "#101820"
          placement: { anchor: center, offset_y: 150 }
        - type: progress
          placement: { anchor: bottom-center, offset_y: -120 }
```

Any role can use a custom layout. Both classic and custom layouts can use an image background, e.g. `background: { path: assets/photo.jpg, fit: cover, left: 0.5, top: 0 }`. This crops the image to the canvas, centered horizontally and aligned at the top; it does not move the background box. See [image backgrounds and fit alignment](reference.md#image-backgrounds-and-fit-alignment).

**Aliases** are a layout name instead of a definition.

## Scene templates

A custom layout object can also carry the defaults of every scene that uses it, so a template describes whole scenes (look, timing, motion, and transitions) and a video only stitches them together and fills in variables:

```yaml
layout:
    stat:
        background: "#7DE2A8"
        duration: 3s                                  # unless the item sets one
        transition_out: { type: slide-left, duration: 0.4s }
        transition_in: { type: fade, duration: 0.3s }
        elements: [ ... ]
        tracks:                                       # attached to this layout
            - id: count-up
              clips:
                  - { type: element, target: number, start: 1s, set: { text: "$(NUMBER)+" } }
```

```yaml
# video.yaml: pick scenes, fill variables, override a thing or two
content:
    slides:
        - { layout: stat, variables: { NUMBER: "8" } }
        - layout: stat
          variables: { NUMBER: "3" }
          duration: 2s                                # overrides the layout
          transition_out: { type: iris }              # overrides the layout
```

| Setting | First found among |
| --- | --- |
| Duration | the item's `duration`, the layout's `duration`, the section's `default_duration`, `defaults.timing.<role>_duration` |
| Out transition | the video's `scene_transitions`, the item's `transition_out`, the layout's, the section's, `defaults.timing.transition` |
| In transition | the video's `scene_transitions`, the item's `transition_in`, the layout's, the section's |

Layout tracks are ordinary [tracks](advanced.md#tracks-and-clips) without `attach`: they repeat in every scene that uses the layout and render below the template's `advanced.tracks`. A layout's transitions are ignored where there is no neighboring scene; an item's `transition_out` on the last scene, or `transition_in` on the first, is an error.

## Custom elements

Elements render in list order, so later elements stack on top of earlier ones, text included. Every element accepts:

| Field | Meaning |
| --- | --- |
| `type` | `text`, `image`, `rect`, `circle`, `progress`, `audio`, `effect`, or `folder` |
| `id` | Names the element so advanced element clips can target it |
| `start`, `duration` | When the element shows within the scene (defaults: the whole scene) |
| `keyframes` | Animation, as in [advanced mode](advanced.md#keyframes) (presets are allowed) |
| `optional` | Skip the element when one of its variables has no value |

| Type | Fields |
| --- | --- |
| `text` | `text`, `placement`, `style`, `effects`, `max_width_ratio`, `max_lines`, `reveal`, `match`, `replace` |
| `image` | `path`, `placement`, `max_width_ratio`, `max_height_ratio`, `fit`, `left`/`right`, `top`/`bottom`, `shape`, `effects`, `opacity` |
| `rect`, `circle` | `width`, `height` (pixels), `color`, `placement`, `corner_radius` (rect), `opacity` |
| `progress` | `placement`, `width_ratio`, `thickness`, `color`, `background_color` (slides only) |
| `audio` | `path`, `volume`, `fade_in`, `fade_out`, `ducks` |
| `effect` | `effects` (an effect-layer stack applied to the elements below it), `opacity` |
| `folder` | `clips` (any elements, folders included), `pivot`, `opacity`, `effects`; see [folders](advanced.md#folders) |

Text style also takes `letter_spacing` and `word_spacing`, extra pixels after every character and after every space. A line of text that fits keeps its spaces, so indented code stays indented. `reveal` shows part of a text; see [text reveal](advanced.md#text-reveal).

## Variables

`text`, `path`, and shape `color` values may contain `$(NAME)` (or `${NAME}`). A content item supplies values in `variables:`:

```yaml
- layout: quote
  variables:
      QUOTE: "Eine kurze Pause reicht schon."
      AUTHOR: "Team Gesundheit"
      ACCENT: "#1C3B44"
```

A name is looked up in this order:

1. the item's `variables`;
2. the item's own text fields: `$(title)`, `$(text)`, `$(tip)`, `$(image)`, `$(cta)`, …;
3. generated values: `$(index)` and `$(total)` for slides, `$(logo)` for `assets.logo`, `$(scene)`, `$(role)`, `$(layout)`, and `$(metadata.title)` and the other metadata fields.

| Syntax | Result |
| --- | --- |
| `$(NAME)` | The value. An unknown name is an error, unless the element is `optional: true`, in which case the element is skipped. |
| `$(NAME\|fallback)` | The value, or `fallback` when it is missing. |
| `$$` | A literal `$`. |

Paths from variables resolve relative to the video YAML, like any content path.

## Targeting layout elements (advanced mode)

Advanced tracks can change layout elements in place. An `element` clip names its `target`: a custom element's `id` (also inside a folder, at any depth), or a classic element's name (`title`, `image`, `text`, `tip`, …). Attach the track to a role, to a layout name (every scene using that layout), or to a scene:

```yaml
advanced:
    tracks:
        - id: count-up
          attach: stat                 # every scene that uses the stat layout
          clips:
              - type: element
                target: number
                start: 1s
                duration: 1s
                set: { text: "$(NUMBER)+", style: { color: "#FF3FD0" } }
                keyframes:
                    - { at: 0s, scale: 1.3, ease: back-out }
                    - { at: 0.5s, scale: 1 }
```

- `keyframes` animate the element, relative to the clip's start. They replace the element's own keyframes for the properties they set, and hold their first and last values outside the clip like any keyframes.
- `set` modifies the element while the clip runs. Any property the element accepts can change (`text`, `path`, `color`, `style`, `effects`, `placement`, …), and `visible: false` hides the element. The element shows its modified form during the clip and its original form outside it. With `set`, the keyframes animate the modified form.

`set` is checked against the element's schema, so unknown properties are errors. Progress bars cannot be targeted. A target that matches no element in any attached scene is an error; scenes without that element are skipped.
