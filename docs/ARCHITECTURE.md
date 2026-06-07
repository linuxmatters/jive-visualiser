# Jivefire Architecture

**TL;DR:** 2-pass streaming audio visualiser that generates broadcast-ready MP4s from podcast audio. FFmpeg-based audio decoding + ffmpeg-statigo static linking = single deployable binary with broad format support.

---

## Core Design Principles

### 1. **Single Binary Distribution**
Uses [ffmpeg-statigo](https://github.com/linuxmatters/ffmpeg-statigo) with embedded static FFmpeg 8.0 libraries (~100MB per platform). No external FFmpeg installation required. Ships for Linux/macOS on amd64/arm64. Hardware acceleration support for NVENC, QuickSync (WIP), VideoToolbox (WIP), and Vulkan Video.

**Why ffmpeg-statigo specifically?** Pre-built static libraries with GPL-licensed FFmpeg that includes H.264 (libx264), H.265 (x265), AV1 (rav1e/dav1d), and AAC encoders. Produces YouTube-compatible MP4s out of the box.

### 2. **2-Pass Streaming Architecture**
Memory-efficient approach: analyse first, render second.

**Pass 1 (Analysis):**
- Stream audio chunks (2048 samples/frame)
- FFT analysis to determine peak magnitudes across all frames
- Calculates optimal scaling parameters
- Memory footprint: ~50MB for 30-minute audio

**Pass 2 (Rendering):**
- Stream audio again with optimal scaling
- Generate RGB frames on-the-fly
- Encode video + audio simultaneously
- No frame buffering—everything streaming

**Why not single-pass?** Naive approach requires pre-loading entire audio file into memory (600MB for 30 minutes). 2-pass reduces memory by 92% while enabling optimal bar height scaling.

### 3. **Unified FFmpeg Audio Pipeline**
Audio decoding uses ffmpeg-statigo's libavformat/libavcodec, supporting any format FFmpeg handles: MP3, FLAC, WAV, OGG, AAC, and more.

**Why FFmpeg for decoding?** Single decode path for all formats. Audio samples are decoded once and shared between FFT analysis (Pass 1) and AAC encoding (Pass 2). The unified pipeline eliminates the "catch-up" delay that occurred when audio was re-decoded during encoding.

**Architecture:**
- `StreamingReader` provides chunk-based streaming decode (no `AudioDecoder` interface)
- Reads chunks on demand; no full-file buffering
- Automatic stereo-to-mono downmixing for visualisation
- Sample rate preserved for AAC encoding

---

## Processing Pipeline

```
Input Audio (MP3/FLAC/WAV/OGG/AAC/...)
    ↓
FFmpeg Decoder (ffmpeg-statigo, streaming)
    ├─ libavformat for demuxing
    ├─ libavcodec for decoding
    └─ Automatic stereo→mono downmix
    ↓
FFT Analysis (gonum/fourier)
    ├─ 2048-point Hanning window
    ├─ Log-scale frequency binning → 64 bars
    └─ Harmonica spring peak-hold dynamics (bars snap up, spring back down)
    ↓
Frame Renderer (image/draw + custom optimizations)
    ├─ 64 bars with symmetric vertical mirroring
    ├─ Pre-computed alpha tables for gradients
    └─ RGB24 pixel buffer (1280×720)
    ↓
Colourspace Conversion (path depends on encoder)
    │
    ├─ [Software] RGBA → YUV420P (Pure Go, parallelised)
    │   ├─ Direct conversion skips intermediate RGB24 buffer
    │   ├─ Parallel row processing via internal/yuv.ParallelRows
    │   └─ ITU-R BT.601 coefficients from internal/yuv
    │
    └─ [Hardware] RGBA → NV12 (Pure Go, parallelised)
        └─ Semi-planar format for GPU encoder upload
    ↓
H.264 Encoder (auto-selected)
    ├─ NVENC (NVIDIA GPU) - hardware accelerated, RGBA input
    ├─ Quick Sync (Intel iGPU) - hardware accelerated
    ├─ VideoToolbox (macOS) - Apple Silicon/Intel
    └─ libx264 (software fallback) - YUV420P input
    ↓
ffmpeg-statigo AAC Encoder
    ├─ Receives pre-decoded samples via WriteAudioSamples()
    ├─ Audio FIFO buffer (handles frame size mismatches)
    ├─ float32 → float32 planar conversion
    └─ Mono or stereo output
    ↓
MP4 Muxer (libavformat)
    └─ Interleaved audio/video packets
```

---

## Key Technical Choices

### Audio Frame Size Mismatch
FFT analysis requires 2048 samples for frequency resolution, but AAC encoder expects 1024 samples per frame. **Solution:** `AudioFIFO` in `encoder/encoder.go` buffers incoming audio samples and drains them in encoder-sized frames, decoupling the FFT chunk size from the AAC frame size.

### Hardware-Accelerated Encoding
Automatic GPU encoder detection in `encoder/hwaccel.go`:
- **NVENC** (NVIDIA): Sends RGBA frames directly to GPU—colourspace conversion happens on GPU, not CPU
- **Quick Sync** (Intel): Hardware-accelerated H.264 encoding via Intel iGPU
- **VideoToolbox** (macOS): Apple Silicon and Intel Mac hardware encoding
- **Software fallback**: Optimised libx264 with `veryfast` preset when no GPU available

**Why RGBA for hardware encoders?** Initial implementation used CPU-side RGB→YUV conversion for all encoders. Benchmarking showed hardware encoders were bottlenecked by CPU conversion overhead. Hardware encoders accept NV12 (semi-planar YUV) natively, so we convert RGBA→NV12 on CPU and let the GPU handle encoding only—avoiding the RGB→YUV→NV12 double conversion that would occur if we sent YUV420P.

### Colourspace Conversion
Hot-path converters in `encoder/frame.go` (`convertRGBAToYUV`, `convertRGBAToNV12`):
- **RGBA→YUV420P** (software encoder): Direct conversion skips intermediate RGB24 buffer allocation
- **RGBA→NV12** (hardware encoders): Semi-planar format for GPU upload

Both call shared BT.601 coefficient helpers and `ParallelRows` from `internal/yuv`. The two functions are kept deliberately separate despite near-identical structure — the hot-path duplication avoids a callback/interface indirection that would hurt throughput.

All converters share common characteristics:
- Parallel row processing across CPU cores via `internal/yuv.ParallelRows`
- Even/odd row separation eliminates per-pixel conditionals in inner loops
- ITU-R BT.601 coefficients with fixed-point integer arithmetic (no floating-point in hot path)

**Why not FFmpeg's swscale?** While ffmpeg-statigo exposes the full swscale API, our parallelised Go implementation significantly outperforms it. FFmpeg's swscale is single-threaded; our implementation distributes row processing across all CPU cores. Parallelisation across cores beats single-threaded SIMD for this workload.

**Why not Go's `color.RGBToYCbCr()`?** It's correct for single pixels but not parallelised. Our implementation processes multiple rows simultaneously across goroutines.

### Symmetric Bar Rendering
Bars 0-31 are mirrored to create bars 32-63. Renderer draws upper-left quadrant (1/4 of pixels), then mirrors 3 times:
1. Vertical flip → downward bars (left half)
2. Horizontal flip → upward bars (right half)
3. Both flips → downward bars (right half)

**Result:** 4x rendering speedup via reduced pixel writes.

### Bubbletea Live Preview
Unified terminal UI (`progress.go`) shows:
- **Pass 1:** Progress bar with frame count, audio profile placeholder
- **Pass 2:** Progress bar, timing/ETA, audio profile (persisted), spectrum visualisation, video preview
- **Completion:** Final progress state + consolidated summary with metrics from both passes

Preview renders via Unicode blocks (`▁▂▃▄▅▆▇█`) using actual bar heights from renderer. Non-blocking goroutine channels prevent UI updates from stalling the encoding pipeline.

---

## File Structure

```
cmd/jivefire/main.go         → CLI entry, 2-pass coordinator
internal/audio/              → StreamingReader (chunk-based FFmpeg decode), FFT analysis
internal/encoder/            → ffmpeg-statigo wrapper, RGB→YUV conversion, FIFO buffer
  ├─ encoder.go              → Video/audio encoding, frame submission
  ├─ hwaccel.go              → Hardware encoder detection (NVENC, QSV, VA-API, Vulkan, VideoToolbox)
  └─ frame.go                → RGBA→YUV420P / RGBA→NV12 parallelised conversion
internal/renderer/           → Frame generation, bar drawing, thumbnail
internal/ui/                 → Bubbletea TUI (unified progress.go for both passes)
internal/config/             → Constants (dimensions, FFT params, colours)
internal/yuv/                → Shared BT.601 coefficient helpers and ParallelRows
internal/theme/              → Terminal colour theme
internal/cli/                → Kong CLI helpers and styled help
third_party/ffmpeg-statigo/  → Git submodule: FFmpeg 8.0 static bindings
```

---

## Future-Proofing

### go-yuv: Parallelised Colourspace Conversion
BT.601 coefficient helpers and `ParallelRows` have been extracted into `internal/yuv`. The hot-path converters (`convertRGBAToYUV`, `convertRGBAToNV12`) in `encoder/frame.go` call these shared primitives. The `internal/yuv` package is a strong candidate for further extraction as a standalone Go module:
- Multiple format conversions: RGBA→YUV420P, RGBA→NV12
- Goroutine-based parallelisation across CPU cores via `ParallelRows`
- Pure Go with no CGO dependencies (coefficients only, no FFmpeg)

There's currently no pure Go library offering parallelised colourspace conversion. Existing options are either single-threaded (stdlib `color.RGBToYCbCr`) or require CGO FFmpeg bindings. A standalone `go-yuv` module would benefit:
- Video encoding pipelines avoiding FFmpeg dependencies
- Image processing tools needing high-throughput colourspace conversion
- WebRTC/streaming applications with real-time constraints

The FIFO buffer implementation is generic enough for any audio frame size mismatch scenario in Go audio processing pipelines.

FFT bar binning logic mirrors CAVA's approach, making it familiar territory for anyone who's worked with terminal audio visualisers.
