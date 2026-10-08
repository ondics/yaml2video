# YAML to Video

`yaml2video` renders v2 video content with a separate v2 layout template into an MP4 using FFmpeg. The video document owns text, images, timing, and audio; the template owns output dimensions, layout, styling, effects, and composition.

## Requirements

- Go 1.27.1 or later to build from source.
- FFmpeg on `PATH` to render. Text requires an FFmpeg build with the `ass` filter (libass enabled). Some effects and transitions require additional FFmpeg filters.

## Build

```sh
go build .
```

## Usage

Create `video.yaml` and `template.yaml` using the [v2 user guide](docs/user-guide.md) or the [simple example](examples/simple/). Media paths in the video file are relative to that file. Supply the template explicitly:

```sh
./yaml2video -n -t template.yaml video.yaml
./yaml2video -t template.yaml -o output.mp4 video.yaml
```

`-n` validates the documents, bindings, timing, media files, and render plan, and prints the timeline and FFmpeg commands without encoding. `-work-dir DIR` overrides the intermediate-file directory (by default `.yaml2video-v2` next to the video file); `-o FILE` overrides the output file (by default `output.mp4` next to the video file). Flags may also follow the video path.

## Container

From the project root, build and run the image with the Make targets (using `template.yaml` and `video.yaml` in the project root by default):

```sh
make docker-build
make docker-run
```

To use the published image instead, run `make docker-run DOCKER_IMAGE=ghcr.io/ondics/yaml2video:main`. Pushes to `main` publish the `main` tag; published releases use their release tag. Set `DOCKER_ARGS` to pass different input paths or flags, for example `make docker-run DOCKER_ARGS="-n -t examples/simple/template-simple.yaml examples/simple/video-simple.yaml"`. The Make target mounts the project root at `/data`, so use paths under that directory for input, output, and `-work-dir` to retain generated files. The image includes FFmpeg and fonts.

The v2 document schemas are in [`docs/video.schema.json`](docs/video.schema.json) and [`docs/template.schema.json`](docs/template.schema.json); see the [format specification](docs/video2yaml-spec.md) and [renderer specification](docs/renderer-spec.md) for details. Unsupported renderer capabilities and nonlocal media URIs are reported as errors, not ignored.

## Development

```sh
go test ./...
```

## License

[MIT](LICENSE)
