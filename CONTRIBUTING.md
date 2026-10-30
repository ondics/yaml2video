# Contributing to yaml2video

Keep changes focused, follow the existing code style, and add tests for behavior changes.

## Project layout

- `cmd/yaml2video/main.go`: thin executable entry point delegating to `internal/cli`.
- `internal/cli/`: argument parsing, commands, help, and console output.
- `internal/project/`: video project models and loading.
- `internal/render/`: document validation, render planning, FFmpeg execution, and fonts.
- `internal/server/`: HTTP server, routing, and middleware; `api/` provides response helpers, `controller/` manages renders and storage, and `handlers/` implements endpoints.
- `internal/template/`: YAML template expansion.
- `internal/types/`: shared value types.
- `docs/`: documentation and authoritative JSON schemas.

`docs/schemas.go` intentionally remains alongside `docs/*.schema.json` so `//go:embed` can include them without parent-directory paths. These JSON files are the single source used by editors, documentation, and the runtime; do not duplicate them under `internal/`.

## Develop with Docker

Install Docker with Compose and `make`, then clone the repository and enter its folder. No local Go or FFmpeg installation is required.

```sh
make build           # build the shared application and development image
make test            # run go test ./... against the current checkout
make run ARGS="-n examples/simple/video.yaml"
make run ARGS="examples/simple/video.yaml"
```

`make test` mounts the checkout at `/src` to test your current source. `make run` mounts the checkout at `/data` and passes `ARGS` to the CLI, so generated files are saved to your checkout. No interactive container shell is needed.

`make run` uses the executable built into the image. Run `make build` again after changing application code or the Dockerfile. To try source changes without rebuilding the image, use the local `go run` workflow below.

For the API server, run `make up` after building; `make down` stops it without deleting data. See [container usage](README.md#container) for details. The API is unauthenticated and must remain private.

## Develop without Docker

Install Go **1.27.1 or later** (see `go.mod`) and clone the repository. From the project folder:

```sh
go mod download
go test ./...
go run ./cmd/yaml2video -n examples/simple/video.yaml
```

For actual rendering, install FFmpeg and fonts. Text and subtitles require FFmpeg's `ass` filter (libass enabled). Dry-run mode (`-n`) validates YAML and prints rendering commands without creating a video.

## Before opening a pull request

With local Go installed, run these commands from the project folder:

```sh
gofmt -w cmd internal docs
go vet ./...
go test -race ./...
go build ./...
go run ./cmd/yaml2video -n -t examples/normal/template.yaml examples/normal/video.yaml
go run ./cmd/yaml2video -n -t examples/complex/template.yaml examples/complex/video.yaml
```

CI checks formatting, runs vet and race-enabled tests, builds all packages, and validates the normal and complex examples. `make test` is a quick test run; it does not run all CI checks.

- Add or update tests for behavior changes, especially validation, rendering, and the API.
- Update `README.md` or `docs/` when changing CLI options, YAML formats, or API behavior.
- Do not commit generated videos, build output, or temporary files.
- Avoid unrelated refactors in the same pull request.
- Use the [pull-request template](.github/pull_request_template.md) to explain the change and report what you tested.

## Reporting security issues

Do not post potential security vulnerabilities in public issues. Contact the repository maintainers privately through the contact method listed on the project's GitHub profile.
