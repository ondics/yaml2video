# v2 examples

Each of `simple/`, `normal/`, and `complex/` contains a `video-*.yaml` content document, a matching `template-*.yaml` layout document, and **generated mock media** under `assets/`. The placeholders are labeled graphics and synthetic tones, not real exercise photography or narrated audio.

Run from the repository root (requires FFmpeg with libass):

```sh
go run . -n -t examples/simple/template-simple.yaml examples/simple/video-simple.yaml
go run . -t examples/simple/template-simple.yaml examples/simple/video-simple.yaml
```

The second command creates `examples/simple/output.mp4`. Replace `simple` with `normal` or `complex` to try the other examples. Use `-o /path/to/output.mp4` to choose a different output path. To regenerate all placeholder media, run `sh examples/generate-mock-assets.sh` from the repository root. See the [v2 user guide](../docs/user-guide.md) for the format.
