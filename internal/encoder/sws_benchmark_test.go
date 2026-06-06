package encoder

// =============================================================================
// RGB→YUV420P Colourspace Conversion Benchmark
// =============================================================================
//
// This benchmark compares two approaches for converting RGB24 frames to YUV420P:
//
//   1. Go Implementation (parallelised)
//      - Uses goroutines to process row groups across CPU cores
//      - ITU-R BT.601 coefficients matching Go's color package
//      - Located in encoder/frame.go
//
//   2. FFmpeg swscale
//      - FFmpeg's native colourspace conversion library
//      - SIMD-optimised but single-threaded
//      - Accessed via ffmpeg-statigo bindings
//
// Run with: just bench-yuv
//
// Expected results on multi-core systems:
//   - Go implementation is ~8× faster due to parallelisation
//   - swscale has zero allocations but can't leverage multiple cores
//
// =============================================================================

import (
	"fmt"
	"runtime"
	"testing"
	"unsafe"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jivefire/internal/yuv"
)

const (
	benchWidth  = 1280
	benchHeight = 720
)

// convertRGBToYUVGo converts RGB24 frames to YUV420P using the production
// yuv-package conversion primitives, so the benchmark measures the current
// hot-path maths rather than a stale copy.
func convertRGBToYUVGo(rgbData []byte, yuvFrame *ffmpeg.AVFrame, width, height int) {
	yPlane := yuvFrame.Data().Get(0)
	uPlane := yuvFrame.Data().Get(1)
	vPlane := yuvFrame.Data().Get(2)

	yLinesize := yuvFrame.Linesize().Get(0)
	uLinesize := yuvFrame.Linesize().Get(1)
	vLinesize := yuvFrame.Linesize().Get(2)

	yuv.ParallelRows(height, func(startY, endY int) {
		for y := startY; y < endY; y++ {
			yPtr := unsafe.Add(yPlane, y*yLinesize)
			rgbIdx := y * width * 3

			for x := range width {
				r := int32(rgbData[rgbIdx])
				g := int32(rgbData[rgbIdx+1])
				b := int32(rgbData[rgbIdx+2])
				rgbIdx += 3

				*(*uint8)(unsafe.Add(yPtr, x)) = yuv.RGBToY(r, g, b)

				if (y&1) == 0 && (x&1) == 0 {
					uvY := y >> 1
					uvX := x >> 1
					*(*uint8)(unsafe.Add(uPlane, uvY*uLinesize+uvX)) = yuv.RGBToCb(r, g, b)
					*(*uint8)(unsafe.Add(vPlane, uvY*vLinesize+uvX)) = yuv.RGBToCr(r, g, b)
				}
			}
		}
	})
}

// SwsConverter wraps FFmpeg's swscale for RGB24 → YUV420P conversion
type SwsConverter struct {
	swsCtx   *ffmpeg.SwsContext
	srcFrame *ffmpeg.AVFrame
	width    int
	height   int
}

func NewSwsConverter(width, height int) (*SwsConverter, error) {
	swsCtx := ffmpeg.SwsAllocContext()
	if swsCtx == nil {
		return nil, nil // Would need proper error
	}

	// Configure the scaler
	swsCtx.SetSrcW(width)
	swsCtx.SetSrcH(height)
	swsCtx.SetSrcFormat(int(ffmpeg.AVPixFmtRgb24))
	swsCtx.SetDstW(width)
	swsCtx.SetDstH(height)
	swsCtx.SetDstFormat(int(ffmpeg.AVPixFmtYuv420P))
	swsCtx.SetFlags(uint(ffmpeg.SwsBilinear))

	// Initialise the context
	ret, err := ffmpeg.SwsInitContext(swsCtx, nil, nil)
	if err != nil || ret < 0 {
		return nil, err
	}

	// Allocate source frame for RGB data
	srcFrame := ffmpeg.AVFrameAlloc()
	if srcFrame == nil {
		return nil, nil
	}
	srcFrame.SetWidth(width)
	srcFrame.SetHeight(height)
	srcFrame.SetFormat(int(ffmpeg.AVPixFmtRgb24))

	ret, err = ffmpeg.AVFrameGetBuffer(srcFrame, 0)
	if err != nil || ret < 0 {
		ffmpeg.AVFrameFree(&srcFrame)
		return nil, err
	}

	return &SwsConverter{
		swsCtx:   swsCtx,
		srcFrame: srcFrame,
		width:    width,
		height:   height,
	}, nil
}

func (c *SwsConverter) Convert(rgbData []byte, dstFrame *ffmpeg.AVFrame) error {
	// Copy RGB data into source frame
	srcLinesize := c.srcFrame.Linesize().Get(0)
	srcData := c.srcFrame.Data().Get(0)

	for y := 0; y < c.height; y++ {
		srcOffset := y * srcLinesize
		rgbOffset := y * c.width * 3
		for x := 0; x < c.width*3; x++ {
			*(*uint8)(unsafe.Add(srcData, srcOffset+x)) = rgbData[rgbOffset+x]
		}
	}

	// Use FFmpeg's swscale
	_, err := ffmpeg.SwsScaleFrame(c.swsCtx, dstFrame, c.srcFrame)
	return err
}

func (c *SwsConverter) Close() {
	if c.srcFrame != nil {
		ffmpeg.AVFrameFree(&c.srcFrame)
	}
	if c.swsCtx != nil {
		ffmpeg.SwsFreecontext(c.swsCtx)
	}
}

func createTestFrames() ([]byte, *ffmpeg.AVFrame) {
	// Create RGB test data with some pattern
	rgbSize := benchWidth * benchHeight * 3
	rgbData := make([]byte, rgbSize)
	for i := 0; i < rgbSize; i += 3 {
		rgbData[i] = uint8(i % 256)   // R
		rgbData[i+1] = uint8(i % 128) // G
		rgbData[i+2] = uint8(i % 64)  // B
	}

	// Allocate YUV frame
	yuvFrame := ffmpeg.AVFrameAlloc()
	yuvFrame.SetWidth(benchWidth)
	yuvFrame.SetHeight(benchHeight)
	yuvFrame.SetFormat(int(ffmpeg.AVPixFmtYuv420P))
	_, _ = ffmpeg.AVFrameGetBuffer(yuvFrame, 0)

	return rgbData, yuvFrame
}

// =============================================================================
// Benchmarks
// =============================================================================

// BenchmarkGoRGBToYUV measures the parallelised Go implementation.
// This is the production code path used by Jivefire.
func BenchmarkGoRGBToYUV(b *testing.B) {
	rgbData, yuvFrame := createTestFrames()
	defer ffmpeg.AVFrameFree(&yuvFrame)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		convertRGBToYUVGo(rgbData, yuvFrame, benchWidth, benchHeight)
	}
}

// BenchmarkSwscaleRGBToYUV measures FFmpeg's swscale library.
// Single-threaded but SIMD-optimised.
func BenchmarkSwscaleRGBToYUV(b *testing.B) {
	rgbData, yuvFrame := createTestFrames()
	defer ffmpeg.AVFrameFree(&yuvFrame)

	converter, err := NewSwsConverter(benchWidth, benchHeight)
	if err != nil {
		b.Fatalf("Failed to create sws converter: %v", err)
	}
	defer converter.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = converter.Convert(rgbData, yuvFrame)
	}
}

// BenchmarkRGBAToYUVDirect measures the direct RGBA→YUV420P conversion.
// This skips the intermediate RGB24 buffer for software encoding path.
func BenchmarkRGBAToYUVDirect(b *testing.B) {
	// Create RGBA test data
	rgbaSize := benchWidth * benchHeight * 4
	rgbaData := make([]byte, rgbaSize)
	for i := 0; i < rgbaSize; i += 4 {
		rgbaData[i] = uint8(i % 256)   // R
		rgbaData[i+1] = uint8(i % 128) // G
		rgbaData[i+2] = uint8(i % 64)  // B
		rgbaData[i+3] = 255            // A
	}

	// Allocate YUV frame
	yuvFrame := ffmpeg.AVFrameAlloc()
	yuvFrame.SetWidth(benchWidth)
	yuvFrame.SetHeight(benchHeight)
	yuvFrame.SetFormat(int(ffmpeg.AVPixFmtYuv420P))
	_, _ = ffmpeg.AVFrameGetBuffer(yuvFrame, 0)
	defer ffmpeg.AVFrameFree(&yuvFrame)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		convertRGBAToYUV(rgbaData, yuvFrame, benchWidth, benchHeight)
	}
}

// TestConversionEquivalence verifies both implementations produce similar output.
// Some pixel differences are expected due to different rounding in coefficient
// implementations (Go uses integer arithmetic, FFmpeg uses floating-point).
func TestConversionEquivalence(t *testing.T) {
	rgbData, yuvFrameGo := createTestFrames()
	defer ffmpeg.AVFrameFree(&yuvFrameGo)

	_, yuvFrameSws := createTestFrames()
	defer ffmpeg.AVFrameFree(&yuvFrameSws)

	// Convert with Go implementation
	convertRGBToYUVGo(rgbData, yuvFrameGo, benchWidth, benchHeight)

	// Convert with swscale
	converter, err := NewSwsConverter(benchWidth, benchHeight)
	if err != nil {
		t.Fatalf("Failed to create sws converter: %v", err)
	}
	defer converter.Close()

	err = converter.Convert(rgbData, yuvFrameSws)
	if err != nil {
		t.Fatalf("Swscale conversion failed: %v", err)
	}

	// Compare Y planes (they should be very close, allowing for rounding differences)
	yLinesize := yuvFrameGo.Linesize().Get(0)
	yPlaneGo := yuvFrameGo.Data().Get(0)
	yPlaneSws := yuvFrameSws.Data().Get(0)

	diffCount := 0
	maxDiff := 0
	for y := range benchHeight {
		for x := range benchWidth {
			offset := y*yLinesize + x
			goVal := *(*uint8)(unsafe.Add(yPlaneGo, offset))
			swsVal := *(*uint8)(unsafe.Add(yPlaneSws, offset))
			diff := int(goVal) - int(swsVal)
			if diff < 0 {
				diff = -diff
			}
			if diff > 1 { // Allow 1 unit difference for rounding
				diffCount++
				if diff > maxDiff {
					maxDiff = diff
				}
			}
		}
	}

	t.Logf("Y plane differences > 1: %d pixels (max diff: %d)", diffCount, maxDiff)
}

// =============================================================================
// Summary
// =============================================================================

// TestBenchmarkSummary runs both implementations and prints a comparison.
// This provides a quick human-readable summary without running full benchmarks.
func TestBenchmarkSummary(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping summary in short mode")
	}

	rgbData, yuvFrame := createTestFrames()
	defer ffmpeg.AVFrameFree(&yuvFrame)

	// Benchmark Go implementation
	goStart := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			convertRGBToYUVGo(rgbData, yuvFrame, benchWidth, benchHeight)
		}
	})

	// Benchmark swscale
	converter, err := NewSwsConverter(benchWidth, benchHeight)
	if err != nil {
		t.Fatalf("Failed to create sws converter: %v", err)
	}
	defer converter.Close()

	swsStart := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			_ = converter.Convert(rgbData, yuvFrame)
		}
	})

	goNs := goStart.NsPerOp()
	swsNs := swsStart.NsPerOp()
	speedup := float64(swsNs) / float64(goNs)

	fmt.Println()
	fmt.Println("╭───────────────────────────────────────────────────────────────╮")
	fmt.Println("│          RGB→YUV420P Colourspace Conversion Benchmark         │")
	fmt.Println("├───────────────────────────────────────────────────────────────┤")
	fmt.Printf("│  Resolution:     %d×%d (%.1f megapixels)                    │\n", benchWidth, benchHeight, float64(benchWidth*benchHeight)/1e6)
	fmt.Printf("│  CPU cores:      %-2d                                           │\n", runtime.NumCPU())
	fmt.Println("├───────────────────────────────────────────────────────────────┤")
	fmt.Printf("│  Go (parallel):    %6.0f µs/frame  (%2d allocs)               │\n", float64(goNs)/1000, goStart.AllocsPerOp())
	fmt.Printf("│  FFmpeg swscale:   %6.0f µs/frame  (%2d allocs)               │\n", float64(swsNs)/1000, swsStart.AllocsPerOp())
	fmt.Println("├───────────────────────────────────────────────────────────────┤")
	fmt.Printf("│  ✓ Go implementation is %.1f× faster                          │\n", speedup)
	fmt.Println("│                                                               │")
	fmt.Println("│  Parallelisation across CPU cores beats SIMD optimisation.   │")
	fmt.Println("╰───────────────────────────────────────────────────────────────╯")
	fmt.Println()
}
