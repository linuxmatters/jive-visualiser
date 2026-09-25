#!/usr/bin/env bash
set -euo pipefail

if [ ! -f "third_party/ffmpeg-statigo/go.mod" ]; then
    echo "Error: ffmpeg-statigo submodule not initialised. Run 'just setup' first." >&2
    exit 1
fi
if [ ! -f "third_party/ffmpeg-statigo/lib/$(go env GOOS)_$(go env GOARCH)/libffmpeg.a" ]; then
    echo "Error: ffmpeg-statigo library not downloaded. Run 'just setup' first." >&2
    exit 1
fi

VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
echo "Building jive-visualiser version: $VERSION"
CGO_ENABLED=1 go build -ldflags="-X main.version=$VERSION" -o jive-visualiser ./cmd/jive-visualiser
