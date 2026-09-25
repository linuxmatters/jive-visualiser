# Jive Visualiser - Just Commands

set allow-duplicate-recipes
import 'just/loader.just'

# List commands
default:
    @just --list

# Clean build artifacts
clean:
    rm -fv jive-visualiser 2>/dev/null || true
    @rm testdata/*.mp4 2>/dev/null || true
    @rm testdata/*.flac 2>/dev/null || true
    @rm testdata/*.wav 2>/dev/null || true
    @rm testdata/*-stereo.mp3 2>/dev/null || true

# Install jive-visualiser to ~/.local/bin
install: build
    @mkdir -p ~/.local/bin 2>/dev/null || true
    @mv ./jive-visualiser ~/.local/bin/jive-visualiser
    @echo "Installed jive-visualiser to ~/.local/bin/jive-visualiser"
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
    trap 'rm -f ./bench-yuv' EXIT

    echo ""
    echo "Benchmarking RGB→YUV420P colourspace conversion (1280×720, 1000 iterations)"
    echo ""

    hyperfine \
        --warmup 1 \
        --runs 5 \
        --command-name "Go (parallel)" "./bench-yuv --impl=go --iterations=1000" \
        --command-name "FFmpeg swscale" "./bench-yuv --impl=swscale --iterations=1000" \
        --export-markdown testdata/bench-yuv.md

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

    # Use jive-visualiser's built-in hardware probe to detect available encoders
    echo "Probing hardware encoders..."
    ./jive-visualiser --probe

    # Build encoder list from probe results
    ENCODERS=()

    # Software is always available
    ENCODERS+=("--command-name" "Software (libx264)" "./jive-visualiser --no-preview --encoder=software '$INPUT' testdata/bench-software.mp4")

    # Parse jive-visualiser --probe output to detect available hardware encoders
    PROBE_OUTPUT=$(./jive-visualiser --probe 2>&1)

    if echo "$PROBE_OUTPUT" | grep -q "h264_nvenc.*✓ available"; then
        ENCODERS+=("--command-name" "NVENC (h264_nvenc)" "./jive-visualiser --no-preview --encoder=nvenc '$INPUT' testdata/bench-nvenc.mp4")
    fi

    if echo "$PROBE_OUTPUT" | grep -q "h264_vaapi.*✓ available"; then
        ENCODERS+=("--command-name" "VA-API (h264_vaapi)" "./jive-visualiser --no-preview --encoder=vaapi '$INPUT' testdata/bench-vaapi.mp4")
    fi

    if echo "$PROBE_OUTPUT" | grep -q "h264_vulkan.*✓ available"; then
        ENCODERS+=("--command-name" "Vulkan (h264_vulkan)" "./jive-visualiser --no-preview --encoder=vulkan '$INPUT' testdata/bench-vulkan.mp4")
    fi

    if echo "$PROBE_OUTPUT" | grep -q "h264_qsv.*✓ available"; then
        ENCODERS+=("--command-name" "QSV (h264_qsv)" "./jive-visualiser --no-preview --encoder=qsv '$INPUT' testdata/bench-qsv.mp4")
    fi

    if echo "$PROBE_OUTPUT" | grep -q "h264_videotoolbox.*✓ available"; then
        ENCODERS+=("--command-name" "VideoToolbox (h264_videotoolbox)" "./jive-visualiser --no-preview --encoder=videotoolbox '$INPUT' testdata/bench-videotoolbox.mp4")
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
    @env -u LD_LIBRARY_PATH vhs ./jive-visualiser.tape

# Show current version (from git tags or "dev" if no tags)
version:
    @git describe --tags --always --dirty 2>/dev/null || echo "dev"

# List recent releases
releases:
    @git tag --sort=-creatordate | head -10

# Show what would be in the next release changelog
changelog:
    #!/usr/bin/env bash
    LATEST_TAG=$(git describe --tags --abbrev=0 2>/dev/null || echo "")
    if [ -n "$LATEST_TAG" ]; then
        echo "Changes since $LATEST_TAG:"
        git log $LATEST_TAG..HEAD --pretty=format:"* %s (%h)"
    else
        echo "No previous tags found. All commits:"
        git log --pretty=format:"* %s (%h)"
    fi

# Create a new release tag (requires VERSION=x.y.z)
release VERSION:
    #!/usr/bin/env bash
    set -e

    # Validate version format
    if ! echo "{{VERSION}}" | grep -qE '^[0-9]+\.[0-9]+\.[0-9]+$'; then
        echo "Error: VERSION must be in format x.y.z (e.g., 0.1.0)"
        exit 1
    fi

    # Check for uncommitted changes
    if ! git diff-index --quiet HEAD --; then
        echo "Error: Working directory has uncommitted changes"
        exit 1
    fi

    # Check if tag already exists
    if git rev-parse "{{VERSION}}" >/dev/null 2>&1; then
        echo "Error: Tag {{VERSION}} already exists"
        exit 1
    fi

    echo "Creating release {{VERSION}}..."
    git tag -a "{{VERSION}}" -m "Release {{VERSION}}"
    echo "✓ Tag {{VERSION}} created"
    echo ""
    echo "To publish the release:"
    echo "  git push origin {{VERSION}}"
    echo ""
    echo "This will trigger the GitHub Actions release workflow which will:"
    echo "  - Build binaries for all platforms"
    echo "  - Generate changelog from commits"
    echo "  - Create GitHub release with downloadable assets"

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
    ./jive-visualiser --episode="01" --title "Linux Matters mp3 (mono)" testdata/LMP0.mp3 testdata/LMP0-mp3.mp4
    ./jive-visualiser --no-preview --channels 2 --episode="02" --title "Linux Matters mp3 (stereo)" testdata/LMP0-stereo.mp3 testdata/LMP0-mp3-stereo.mp4
    ./jive-visualiser --episode="01" --title "Linux Matters flac (mono)" testdata/LMP0.flac testdata/LMP0-flac.mp4
    ./jive-visualiser --no-preview --channels 2 --episode="02" --title "Linux Matters flac (stereo)" testdata/LMP0-stereo.flac testdata/LMP0-flac-stereo.mp4
    ./jive-visualiser --episode="01" --title "Linux Matters: wav (mono)" testdata/LMP0.wav testdata/LMP0-wav.mp4
    ./jive-visualiser --no-preview --channels 2 --episode="02" --title "Linux Matters: wav (stereo)" testdata/LMP0-stereo.wav testdata/LMP0-wav-stereo.mp4
    assert_audio_match testdata/LMP0.mp3        testdata/LMP0-mp3.mp4
    assert_audio_match testdata/LMP0-stereo.mp3 testdata/LMP0-mp3-stereo.mp4
    assert_audio_match testdata/LMP0.flac       testdata/LMP0-flac.mp4
    assert_audio_match testdata/LMP0-stereo.flac testdata/LMP0-flac-stereo.mp4
    assert_audio_match testdata/LMP0.wav        testdata/LMP0-wav.mp4
    assert_audio_match testdata/LMP0-stereo.wav testdata/LMP0-wav-stereo.mp4
