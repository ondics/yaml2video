# YAML to Video

yaml2video turns simple YAML files into stunning, polished, repeatable MP4 videos,
making it easy to automate product catalogs, social media presentations, and other
recurring video content. Get started with Docker, create consistent video series with
reusable templates, and integrate video generation into your production workflow 
through the API or CLI.

Features

* Simple setup and usage with Docker
* API integration to embed video generation in your production workflow
* YAML input to describe video content
* Reusable YAML templates for consistent video series and simplified content creation
* Template library with a variety of video styles
* Flexible video content, including static images, text, logos, intros, and outros
* Visual and audio effects, including sliding animations, smooth transitions, and background music
* MP4 output ready to use and share

Showcase

This is a little showcase of yaml2video. See the video and template file in [examples/showcase](examples/showcase)

[![yaml2video showcase on YouTube](https://img.youtube.com/vi/FRqc2Kr5sQc/hqdefault.jpg)](https://www.youtube.com/shorts/FRqc2Kr5sQc)

## Prerequisites

* Docker / Docker Compose
* make

The Docker image size is about 2,5 GB.

## Quick start

Clone this repository and open a terminal in its folder. You need Docker with Compose and `make`; Go, FFmpeg, and fonts are included in the image.

```sh
$ git clone https://github.com/ondics/yaml2video
$ cd yaml2video
$ make build up
$ make render FILE=examples/simple/video.yaml
/renders/TVMSVSQRYZTI7EQPGDUVHBHKQK/download
```

Then open your browser with the url: 

```
http://<your ip-address>:8080/renders/TVMSVSQRYZTI7EQPGDUVHBHKQK/download
```

## Using CLI for video generation

`make cli` runs yaml2video in the container with your current folder. The example's video YAML includes `template_file: template.yaml`, resolved beside the video YAML, so `-t` is optional. The result is `examples/simple/output.mp4` on your computer.

For your own files, pass CLI arguments through `ARGS`:

```sh
make cli ARGS="-t template.yaml -o output.mp4 video.yaml"
```

Omit `-t` if your video YAML includes `template_file`; otherwise, `-t` is required. An explicit `-t` overrides `template_file`, but the required `video.template` identifier must still match the selected template's `template_id`.

Add `-n` to check the YAML and preview the rendering commands without creating a video. See the [user guide](docs/user-guide.md) for writing YAML and using media files.

## Container

Available Make commands (`make` defaults to `make help`). Container commands use Docker Compose and the same image:

| Command | What it does |
| --- | --- |
| `make help` | Show available Make commands |
| `make build` | Build or rebuild the image |
| `make cli ARGS="..."` | Run the CLI with the current folder mounted at `/data` (defaults to `--help`) |
| `make fonts` | List font families in a readable table via `yaml2video fonts` in the container |
| `make render FILE=... [DIR=...]` | Start the server, render a mounted video YAML, and print the download URL (or an error) |
| `make up` | Start the API server at `http://127.0.0.1:8080` |
| `make down` | Stop and remove the server container (including its stored renders) |
| `make logs` | Follow container logs; press Ctrl-C to stop following |
| `make ps` | Show service status |
| `make config` | Show the resolved Docker Compose configuration |
| `make bash` | Open an optional container shell with the current folder mounted at `/data` |
| `make test` | Run Go tests against the current checkout |

Build the image before starting the server. Rendered files are stored inside the server container, not alongside your input files; `make down` removes them.

### Fonts in the image

The Docker image installs **DejaVu Sans**, **DejaVu Sans Mono**, and **DejaVu Serif** through `fonts-dejavu-core`. The executable also bundles **Go** and **Go Mono** in regular and bold. These families require no Google Fonts downloads.

Use `make fonts` (or `yaml2video fonts` outside Docker) to inspect the full catalogue. The HTTP server exposes the same catalogue at [`GET /fonts`](docs/server-api.md#get-fonts). Entries include their sources, whether a Google font file is cached, and whether the family is available offline. Google Fonts metadata may be fetched online, but listing never downloads font files. Offline mode includes cached metadata and downloaded instances; metadata alone does not make a font available offline, and a cached instance does not guarantee every weight or optical size is available.

To show only families available offline, run `make fonts ARGS="--offline-only"` or `yaml2video fonts --offline-only`. This filters the table to bundled, installed, or cached families; it does not disable metadata network requests. Set `YAML2VIDEO_OFFLINE=1` as well when network access must be disabled.

Each command lists the fonts and cache visible to its own process/container. To see a running server's cached downloads, use its `/fonts` endpoint or mount the same cache directory into the CLI container via `YAML2VIDEO_CACHE_DIR`.

### Server input files

`make up` mounts the repository folder (`./` by default) in the container. Put your video YAML, template YAML, and referenced media in this folder, ideally together in a project subfolder.

To render that example through the API, run:

```sh
make render FILE=examples/simple/video.yaml
```

`make render` starts the server if needed and prints the download URL when rendering completes. `FILE` is relative to the mount.  
Set `DIR` only when `template_file` should resolve from a different directory; by default it resolves beside `FILE`. Relative media paths always resolve beside the video YAML. See the [API documentation](docs/server-api.md) for downloading renders.

Override the host folder with `MOUNT_DIR` (not `PATH`, which controls executable lookup):

```sh
make up MOUNT_DIR=/absolute/path/to/my-video-files
make config MOUNT_DIR=/absolute/path/to/my-video-files
```

Relative `MOUNT_DIR` values resolve from the directory containing `docker-compose.yaml`. The container path remains `/data` regardless of the host folder. The API reads input files from this mount.

Use the same `MOUNT_DIR` value for subsequent Compose commands. With Docker Compose directly, use `MOUNT_DIR=/absolute/path/to/my-video-files docker compose up -d`.

`make cli` and `make bash` always mount the repository's current folder at `/data` instead, preserving the CLI examples above. `make render` uses the server's `MOUNT_DIR` mount; pass the same `MOUNT_DIR` when overriding it.

**The API has no authentication and is for trusted, internal use only.** Keep it private; do not expose it to the internet. See the [API documentation](docs/server-api.md) for requests, media provisioning, and downloads.

### Using without cloning

To render without cloning the repository, run the published image from the folder containing your YAML and media files:

```sh
docker run --rm -v "$PWD:/data" ghcr.io/ondics/yaml2video:latest -t template.yaml video.yaml
```

## Run without Docker

Download the executable for your platform from [releases](https://github.com/ondics/yaml2video/releases/latest) and install [FFmpeg](https://ffmpeg.org/download.html) with the `ass` filter enabled. Make sure `ffmpeg` is on your `PATH`.

```sh
# macOS / Linux
chmod +x ./yaml2video
./yaml2video -t template.yaml video.yaml
```

On Windows, use `yaml2video.exe` instead. Without `-o`, the output is `output.mp4` next to the video YAML file.

## Documentation

- [User guide](docs/user-guide.md) and [examples](examples/)
- [Server API](docs/server-api.md)
- YAML schemas: [video](docs/video.schema.json) · [template](docs/template.schema.json)
- [YAML format and rendering reference](docs/reference.md)
- [Layouts: named, classic, and custom](docs/layouts.md)
- [Advanced mode: tracks, keyframes, and effect layers](docs/advanced.md)
- [Contributing and development setup](CONTRIBUTING.md)

## License

This project is licensed under [MIT](LICENSE).

## Credits 

Our hugs to their great work goes to 

* FFmpeg

## Author

Ondics GmbH

https://ondics.de

(C) 2026 Ondics GmbH
