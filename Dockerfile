FROM golang:1.27.1-bookworm

# Font families available without Google Fonts downloads:
# - fonts-dejavu-core installs DejaVu Sans, DejaVu Sans Mono, and DejaVu Serif.
# - yaml2video embeds Go and Go Mono (regular and bold) in the binary.
# Google Fonts are fetched on demand and cached; offline mode can reuse cached
# instances. Inspect this image's full catalogue with `yaml2video fonts` or
# `make fonts` from the repository (GET /fonts when running the HTTP server).
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates ffmpeg fontconfig fonts-dejavu-core \
    && rm -rf /var/lib/apt/lists/*

WORKDIR /src

COPY go.mod go.sum ./

RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w" \
    -o /usr/local/bin/yaml2video ./src

WORKDIR /data
ENTRYPOINT ["yaml2video"]
