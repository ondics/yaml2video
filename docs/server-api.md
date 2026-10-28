# Render API

For trusted internal clients only. No authentication.

## Local input files in Docker

The Compose server mounts the host's `./examples/` folder read-only at `/data`, which is also its working directory. Place video YAML files, templates, and their media there. For example, `examples/simple/video.yaml` on the host becomes `/data/simple/video.yaml` in the container. Host paths outside this mount are not available through it.

To mount another folder, start the server with `make up MOUNT_DIR=/absolute/path/to/files`. The container path is still `/data`. Relative mount paths resolve beside `docker-compose.yaml`. The server reads YAML and media from this mount but does not write render files there.

This input mount is separate from the server's named data volume at `/var/lib/yaml2video`. `POST /render` continues to resolve relative media paths under `/var/lib/yaml2video/media`, not `/data`.

## GET /fonts

List font families using the shared `render.ListFonts()` catalog. No query parameters are supported initially.

```sh
curl --fail http://localhost:8080/fonts
```

Returns `200 OK` with `Content-Type: application/json` and `Cache-Control: no-store`:

```json
{
  "fonts": [
    {"family": "Go", "sources": ["bundled"], "cached": false, "available_offline": true},
    {"family": "Go Mono", "sources": ["bundled"], "cached": false, "available_offline": true}
  ]
}
```

| Field | Meaning |
| --- | --- |
| `fonts` | Array of deduplicated families, sorted by family name (case-insensitive). Entries describe families, not individual font files; there are no file-path fields. |
| `fonts[].family` | Display name to use as a font family. |
| `fonts[].sources` | One or more of `bundled`, `system`, and `google`, in that order. A family can come from multiple sources. |
| `fonts[].cached` | At least one actual Google font instance is cached. Cached family metadata alone does not count. Bundled or system fonts alone do not set this flag. |
| `fonts[].available_offline` | The family is bundled, installed on the server, or has at least one cached Google instance. This does **not** guarantee that all sizes, optical sizes, styles, or weights are available offline. |
| `warnings` | Optional array of discovery warnings, omitted when empty. If metadata or another source is unavailable, the response remains `200 OK` with partial results and any warnings reported by the shared catalog. |

Discovery honors the server's `YAML2VIDEO_SYSTEM_FONTS=off` and `YAML2VIDEO_OFFLINE` settings. With offline mode enabled, it lists cached metadata (even if stale) and cached instances without downloads; metadata-only Google families have `cached: false` and `available_offline: false` unless another source makes them available. Missing offline metadata is normal and does not itself produce a warning. Online discovery may fetch Google family metadata, but this endpoint never downloads font files.

## GET /render/local

Render YAML files already present on the server (not uploaded by the request):

```sh
curl --fail -N --get http://localhost:8080/render/local \
  --data-urlencode 'path=simple/video.yaml'
```

| Query parameter | Meaning |
| --- | --- |
| `path` | Required video YAML path. Relative paths resolve from the server's working directory (`/data` in Compose). |
| `template` | Optional template YAML path, resolved from the same working directory. Overrides the video's `template_file`. |
| `dir` | Optional base for relative `template_file` references. Relative paths resolve from the server's working directory. It does not control where render files are written. |

Without `template`, the video YAML must contain `template_file`. Without `dir`, that reference resolves beside the video YAML. Relative media paths always resolve beside the video YAML, even when `dir` is supplied.

For example, override the video's template with an explicit path:

```sh
curl --fail -N --get http://localhost:8080/render/local \
  --data-urlencode 'path=simple/video.yaml' \
  --data-urlencode 'template=simple/template.yaml'
```

If the video uses `template_file: template.yaml` but the template is stored under `/data/templates/`, omit `template` and pass `dir=templates`. This changes template lookup only, not the render workspace.

The server generates an ID for each request. Responses use the same `progress`, `complete`, and `error` SSE events as `POST /render`; the `complete` event includes the download URL. Completed MP4s are stored in the server's data volume, not alongside the source YAML, and use the same retention rules. Disconnecting cancels rendering. Missing or invalid files return `400` JSON before streaming starts.

**Trusted clients only:** this endpoint can read paths accessible to the server process; `/data` is a mount location, not a filesystem access sandbox. It starts a render even though its HTTP method is GET.

## POST /render

Send `Content-Type: application/json`. `video` and `template` must be JSON objects matching [video.schema.json](video.schema.json) and [template.schema.json](template.schema.json). `$schema` is an optional editor hint and is ignored by the server; validation uses embedded schemas. No base64 or YAML strings.

Request structure (replace the empty objects with complete documents):

```json
{
  "id": "demo-001",
  "video": {},
  "template": {}
}
```

All fields are required. `id` must be valid UTF-8, at most 512 bytes, and contain no NUL or line breaks. The request body limit is 16 MiB. Referenced media must already exist on the server; relative paths resolve under its media directory.

**Response:** `200 OK`, `Content-Type: text/event-stream`. Keep the connection open until `complete` or `error`; disconnecting cancels rendering.

| SSE event | JSON data |
| --- | --- |
| `progress` | `id`, `stage`, `percent` (0–100), plus optional render details |
| `complete` | `id`, `download` pointing to `/renders/{id}/download`, `url` pointing to `/renders/{id}` |
| `error` | `id`, `error` describing the failure |

```text
event: progress
data: {"id":"demo-001","stage":"starting","percent":0}

event: complete
data: {"id":"demo-001","url":"/renders/demo-001","download":"/renders/demo-001/download"}

```

Ignore SSE heartbeat comments (`: heartbeat`). Failures after streaming starts use the `error` event, not a new HTTP status.

Before streaming, errors return JSON such as `{"error":"message"}`:

| Status | Meaning |
| --- | --- |
| `400` | Invalid request or video/template document |
| `409` | ID already occupied |
| `413` | Request body too large |
| `500` | Server failure |

## GET /renders

List completed, unexpired renders, newest first. Returns `200 OK` with a JSON array (`[]` if none are available):

```json
[
	{
		"id": "demo-001",
		"created_at": "2026-10-14T12:00:00Z",
		"expires_at": "2026-10-21T12:00:00Z",
		"download": "/renders/demo-001/download"
	}
]
```

`expires_at` is the earlier of seven days after creation or 59 minutes after the first successful full download. The render becomes unavailable then; cleanup may delete its files later. Listing does not change retention. Returns `500` if storage cannot be read.

## GET /renders/{id}

Information about the render. Returns `200 OK` with a JSON object.


```json
{
	"id": "demo-001",
	"created_at": "2026-10-14T12:00:00Z",
	"expires_at": "2026-10-21T12:00:00Z",
	"download": "/renders/demo-001/download"
}
```

## GET /renders/{id}/download

Download the completed MP4 using the URL from the `complete` event.

- `200 OK`: `video/mp4` attachment.
- Range requests are supported (`206 Partial Content`).
- `404 Not Found`: render missing, incomplete, or expired.

```sh
curl --fail --output demo-001.mp4 http://localhost:8080/renders/demo-001/download
```

## Retention

Renders expire seven days after creation. A successful full, non-range GET makes a render eligible for deletion after 59 minutes; cleanup runs every minute. HEAD, partial, failed, or interrupted downloads do not shorten retention. An ID remains occupied until its render is deleted.
