# Jivefire - Just Commands

# List commands
default:
    @just --list

# Check ffmpeg-statigo submodule is present
_check-submodule:
    #!/usr/bin/env bash
    if [ ! -f "third_party/ffmpeg-statigo/go.mod" ]; then
        echo "Error: ffmpeg-statigo submodule not initialised. Run 'just setup' first."
        exit 1
    fi
    if [ ! -f "third_party/ffmpeg-statigo/lib/$(go env GOOS)_$(go env GOARCH)/libffmpeg.a" ]; then
        echo "Error: ffmpeg-statigo library not downloaded. Run 'just setup' first."
        exit 1
    fi

# Get latest stable ffmpeg-statigo release tag from GitHub
_get-latest-tag:
    #!/usr/bin/env bash
    if command -v jq &> /dev/null; then
        curl -s https://api.github.com/repos/linuxmatters/ffmpeg-statigo/releases | \
            jq -r '[.[] | select(.prerelease == false and .draft == false and (.tag_name | startswith("v")))][0].tag_name'
    else
        curl -s https://api.github.com/repos/linuxmatters/ffmpeg-statigo/releases | \
            grep -B5 '"prerelease": false' | grep '"tag_name"' | grep -v 'lib-' | head -1 | cut -d'"' -f4
    fi

# Setup or update ffmpeg-statigo submodule and library
setup:
    #!/usr/bin/env bash
    set -e
    echo "Configuring git for submodule-friendly pulls..."
    git config pull.ff only
    git config submodule.recurse true

    # Get latest stable release tag
    TAG=$(just _get-latest-tag)
    if [ -z "$TAG" ] || [ "$TAG" = "null" ]; then
        echo "Error: Could not fetch latest release tag"
        exit 1
    fi

    # Initialise submodule if not already present
    if [ ! -f "third_party/ffmpeg-statigo/go.mod" ]; then
        echo "Initialising ffmpeg-statigo submodule..."
        git submodule update --init --recursive
    fi

    # Check current version
    cd third_party/ffmpeg-statigo
    git fetch --tags
    CURRENT=$(git describe --tags --exact-match 2>/dev/null || echo "")

    if [ "$CURRENT" = "$TAG" ]; then
        echo "ffmpeg-statigo already at latest version ($TAG)"
        cd ../..
    else
        if [ -n "$CURRENT" ]; then
            echo "Updating ffmpeg-statigo from $CURRENT to $TAG..."
        else
            echo "Setting up ffmpeg-statigo $TAG..."
        fi
        git checkout "$TAG"
        cd ../..

        # Remove old library to force re-download
        rm -f third_party/ffmpeg-statigo/lib/*/libffmpeg.a

        # Stage the submodule change
        git add third_party/ffmpeg-statigo
    fi

    # Download libraries (will skip if correct version already exists)
    echo "Checking ffmpeg-statigo libraries..."
    cd third_party/ffmpeg-statigo && go run ./cmd/download-lib
    cd ../..

    # Check if there are staged changes to commit
    if git diff --cached --quiet third_party/ffmpeg-statigo; then
        echo "Setup complete!"
    else
        echo ""
        echo "Setup complete! Submodule updated to $TAG"
        echo "Don't forget to commit: git commit -m 'chore: update ffmpeg-statigo to $TAG'"
    fi

# Build jivefire
build: _check-submodule
    #!/usr/bin/env bash
    VERSION=$(git describe --tags --always --dirty 2>/dev/null || echo "dev")
    echo "Building jivefire version: $VERSION"
    CGO_ENABLED=1 go build -ldflags="-X main.version=$VERSION" -o jivefire ./cmd/jivefire

# Clean build artifacts
clean:
    rm -fv jivefire 2>/dev/null || true
    @rm testdata/*.mp4 2>/dev/null || true
    @rm testdata/*.flac 2>/dev/null || true
    @rm testdata/*.wav 2>/dev/null || true
    @rm testdata/*-stereo.mp3 2>/dev/null || true

# Install jivefire to ~/.local/bin
install: build
    @mkdir -p ~/.local/bin 2>/dev/null || true
    @mv ./jivefire ~/.local/bin/jivefire
    @echo "Installed jivefire to ~/.local/bin/jivefire"
    @echo "Make sure ~/.local/bin is in your PATH"

# Benchmark RGB→YUV conversion (quick summary)
bench-yuv:
    @go test -v ./internal/encoder/ -run=TestBenchmarkSummary

# Benchmark RGB→YUV with Go's testing.B
bench-yuv-full:
    go test -bench=. -benchmem ./internal/encoder/ -run='^$$'

# Benchmark RGB→YUV with hyperfine (statistical analysis)
bench-yuv-hyperfine:
    #!/usr/bin/env bash
    set -e

    if ! command -v hyperfine &> /dev/null; then
        echo "Error: hyperfine not found. Install it with your package manager."
        exit 1
    fi

    echo "Building bench-yuv tool..."
    go build -o ./bench-yuv ./cmd/bench-yuv

    echo ""
    echo "Benchmarking RGB→YUV420P colourspace conversion (1280×720, 1000 iterations)"
    echo ""

    hyperfine \
        --warmup 1 \
        --runs 5 \
        --command-name "Go (parallel)" "./bench-yuv --impl=go --iterations=1000" \
        --command-name "FFmpeg swscale" "./bench-yuv --impl=swscale --iterations=1000" \
        --export-markdown testdata/bench-yuv.md

    rm -f ./bench-yuv
    echo ""
    echo "Results saved to testdata/bench-yuv.md"

# Benchmark video encoders (auto-detects available hardware)
bench-encoders: build
    #!/usr/bin/env bash
    set -e

    # Check hyperfine is installed
    if ! command -v hyperfine &> /dev/null; then
        echo "Error: hyperfine not found. Install it with your package manager."
        exit 1
    fi

    INPUT="testdata/LMP0.mp3"
    if [ ! -f "$INPUT" ]; then
        echo "Error: Test file $INPUT not found"
        exit 1
    fi

    # Clean up any previous benchmark outputs
    rm -f testdata/bench-*.mp4

    # Use jivefire's built-in hardware probe to detect available encoders
    echo "Probing hardware encoders..."
    ./jivefire --probe

    # Build encoder list from probe results
    ENCODERS=()

    # Software is always available
    ENCODERS+=("--command-name" "Software (libx264)" "./jivefire --no-preview --encoder=software '$INPUT' testdata/bench-software.mp4")

    # Parse jivefire --probe output to detect available hardware encoders
    PROBE_OUTPUT=$(./jivefire --probe 2>&1)

    if echo "$PROBE_OUTPUT" | grep -q "h264_nvenc.*✓ available"; then
        ENCODERS+=("--command-name" "NVENC (h264_nvenc)" "./jivefire --no-preview --encoder=nvenc '$INPUT' testdata/bench-nvenc.mp4")
    fi

    if echo "$PROBE_OUTPUT" | grep -q "h264_vaapi.*✓ available"; then
        ENCODERS+=("--command-name" "VA-API (h264_vaapi)" "./jivefire --no-preview --encoder=vaapi '$INPUT' testdata/bench-vaapi.mp4")
    fi

    if echo "$PROBE_OUTPUT" | grep -q "h264_vulkan.*✓ available"; then
        ENCODERS+=("--command-name" "Vulkan (h264_vulkan)" "./jivefire --no-preview --encoder=vulkan '$INPUT' testdata/bench-vulkan.mp4")
    fi

    if echo "$PROBE_OUTPUT" | grep -q "h264_qsv.*✓ available"; then
        ENCODERS+=("--command-name" "QSV (h264_qsv)" "./jivefire --no-preview --encoder=qsv '$INPUT' testdata/bench-qsv.mp4")
    fi

    if echo "$PROBE_OUTPUT" | grep -q "h264_videotoolbox.*✓ available"; then
        ENCODERS+=("--command-name" "VideoToolbox (h264_videotoolbox)" "./jivefire --no-preview --encoder=videotoolbox '$INPUT' testdata/bench-videotoolbox.mp4")
    fi

    echo ""
    echo "Benchmarking video encoders with hyperfine..."
    echo "Input: $INPUT"
    echo ""

    hyperfine \
        --warmup 1 \
        --runs 3 \
        --export-markdown testdata/bench-encoders.md \
        "${ENCODERS[@]}"

    echo ""
    echo "Results saved to testdata/bench-encoders.md"

# Record gif
vhs: build
    # unset LD_LIBRARY_PATH to avoid ttyd/libwebsockets conflicts with GPU drivers
    @env -u LD_LIBRARY_PATH vhs ./jivefire.tape

# Test encoder
test-encoder: build
    #!/usr/bin/env bash
    if [ ! -f testdata/LMP0-stereo.mp3 ]; then
      ffmpeg -i testdata/LMP0.mp3 -ac 2 testdata/LMP0-stereo.mp3
    fi
    if [ ! -f testdata/LMP0.flac ]; then
      ffmpeg -i testdata/LMP0.mp3 testdata/LMP0.flac
    fi
    if [ ! -f testdata/LMP0-stereo.flac ]; then
      ffmpeg -i testdata/LMP0.mp3 -ac 2 testdata/LMP0-stereo.flac
    fi
    if [ ! -f testdata/LMP0.wav ]; then
      ffmpeg -i testdata/LMP0.mp3 testdata/LMP0.wav
    fi
    if [ ! -f testdata/LMP0-stereo.wav ]; then
      ffmpeg -i testdata/LMP0.mp3 -ac 2 testdata/LMP0-stereo.wav
    fi
    set -euo pipefail
    # probe_dur returns the duration in seconds of the first audio stream.
    probe_dur() {
      ffprobe -v error -select_streams a:0 -show_entries stream=duration \
        -of default=noprint_wrappers=1:nokey=1 "$1"
    }
    # assert_audio_match fails if the output audio duration drifts more than 0.5s
    # from the source. Catches the stereo desync bug (half-length/double-speed
    # audio) that a "did it run" check misses.
    assert_audio_match() {
      src_dur=$(probe_dur "$1")
      out_dur=$(probe_dur "$2")
      delta=$(awk -v a="$src_dur" -v b="$out_dur" 'BEGIN { d = a - b; if (d < 0) d = -d; print d }')
      ok=$(awk -v d="$delta" 'BEGIN { print (d <= 0.5) ? "1" : "0" }')
      if [ "$ok" != "1" ]; then
        echo "FAIL: $2 audio duration ${out_dur}s drifts ${delta}s from source ${src_dur}s ($1)" >&2
        exit 1
      fi
      echo "OK: $2 audio ${out_dur}s matches source ${src_dur}s (Δ${delta}s)"
    }
    ./jivefire --episode="01" --title "Linux Matters mp3 (mono)" testdata/LMP0.mp3 testdata/LMP0-mp3.mp4
    ./jivefire --no-preview --channels 2 --episode="02" --title "Linux Matters mp3 (stereo)" testdata/LMP0-stereo.mp3 testdata/LMP0-mp3-stereo.mp4
    ./jivefire --episode="01" --title "Linux Matters flac (mono)" testdata/LMP0.flac testdata/LMP0-flac.mp4
    ./jivefire --no-preview --channels 2 --episode="02" --title "Linux Matters flac (stereo)" testdata/LMP0-stereo.flac testdata/LMP0-flac-stereo.mp4
    ./jivefire --episode="01" --title "Linux Matters: wav (mono)" testdata/LMP0.wav testdata/LMP0-wav.mp4
    ./jivefire --no-preview --channels 2 --episode="02" --title "Linux Matters: wav (stereo)" testdata/LMP0-stereo.wav testdata/LMP0-wav-stereo.mp4
    assert_audio_match testdata/LMP0.mp3        testdata/LMP0-mp3.mp4
    assert_audio_match testdata/LMP0-stereo.mp3 testdata/LMP0-mp3-stereo.mp4
    assert_audio_match testdata/LMP0.flac       testdata/LMP0-flac.mp4
    assert_audio_match testdata/LMP0-stereo.flac testdata/LMP0-flac-stereo.mp4
    assert_audio_match testdata/LMP0.wav        testdata/LMP0-wav.mp4
    assert_audio_match testdata/LMP0-stereo.wav testdata/LMP0-wav-stereo.mp4

# Run linters
lint:
    @go vet ./...
    @gocyclo -top 20 -avg -ignore '_test\.go$' .
    @ineffassign ./...
    @golangci-lint run
    @actionlint

# Run tests
test: _check-submodule
    go test ./...
