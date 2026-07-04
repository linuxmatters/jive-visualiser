package encoder

// =============================================================================
// RGBA→YUV420P Colourspace Conversion Benchmark
// =============================================================================
//
// This benchmark compares two approaches for converting RGBA frames to YUV420P:
//
//   1. Go Implementation (parallelised)
//      - The production hot path: convertRGBAToYUV over a yuv.RowPool
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
//   - Go implementation is several times faster due to parallelisation
//   - swscale has zero allocations but cannot use multiple cores
//
// =============================================================================

import (
	"fmt"
	"runtime"
	"testing"
	"unsafe"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jive-visualiser/internal/yuv"
)

const (
	benchWidth  = 1280
	benchHeight = 720
)

// SwsConverter wraps FFmpeg's swscale for RGBA → YUV420P conversion
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
	swsCtx.SetSrcFormat(int(ffmpeg.AVPixFmtRgba))
	swsCtx.SetDstW(width)
	swsCtx.SetDstH(height)
	swsCtx.SetDstFormat(int(ffmpeg.AVPixFmtYuv420P))
	swsCtx.SetFlags(uint(ffmpeg.SwsBilinear))

	// Initialise the context
	ret, err := ffmpeg.SwsInitContext(swsCtx, nil, nil)
	if err != nil || ret < 0 {
		return nil, err
	}

	// The Go path uses full-range BT.601 (Go's colour package), but swscale
	// defaults to limited-range YUV output. Request full-range output so the
	// two implementations differ only by rounding.
	invTable, srcRange, table, _, brightness, contrast, saturation, err := ffmpeg.SwsGetColorspaceDetails(swsCtx)
	if err != nil {
		return nil, err
	}
	ret, err = ffmpeg.SwsSetColorspaceDetails(swsCtx, invTable, srcRange, table, 1, brightness, contrast, saturation)
	if err != nil || ret < 0 {
		return nil, err
	}

	// Allocate source frame for RGBA data
	srcFrame := ffmpeg.AVFrameAlloc()
	if srcFrame == nil {
		return nil, nil
	}
	srcFrame.SetWidth(width)
	srcFrame.SetHeight(height)
	srcFrame.SetFormat(int(ffmpeg.AVPixFmtRgba))

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

func (c *SwsConverter) Convert(rgbaData []byte, dstFrame *ffmpeg.AVFrame) error {
	// Copy RGBA data into source frame
	srcLinesize := c.srcFrame.Linesize().Get(0)
	srcData := c.srcFrame.Data().Get(0)

	for y := 0; y < c.height; y++ {
		srcOffset := y * srcLinesize
		rgbaOffset := y * c.width * 4
		for x := 0; x < c.width*4; x++ {
			*(*uint8)(unsafe.Add(srcData, srcOffset+x)) = rgbaData[rgbaOffset+x]
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
	// Create RGBA test data with some pattern
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

	return rgbaData, yuvFrame
}

// =============================================================================
// Benchmarks
// =============================================================================

// BenchmarkGoRGBAToYUV measures the parallelised Go implementation.
// This is the production code path used by Jive Visualiser: convertRGBAToYUV
// over a persistent yuv.RowPool.
func BenchmarkGoRGBAToYUV(b *testing.B) {
	rgbaData, yuvFrame := createTestFrames()
	defer ffmpeg.AVFrameFree(&yuvFrame)

	pool := yuv.NewRowPool(benchHeight)
	defer pool.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		convertRGBAToYUV(pool, rgbaData, yuvFrame, benchWidth)
	}
}

// BenchmarkSwscaleRGBAToYUV measures FFmpeg's swscale library.
// Single-threaded but SIMD-optimised.
func BenchmarkSwscaleRGBAToYUV(b *testing.B) {
	rgbaData, yuvFrame := createTestFrames()
	defer ffmpeg.AVFrameFree(&yuvFrame)

	converter, err := NewSwsConverter(benchWidth, benchHeight)
	if err != nil {
		b.Fatalf("Failed to create sws converter: %v", err)
	}
	defer converter.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = converter.Convert(rgbaData, yuvFrame)
	}
}

// TestConversionEquivalence verifies both implementations produce similar output.
// Some pixel differences are expected due to different rounding in coefficient
// implementations (Go uses integer arithmetic, FFmpeg uses floating-point).
func TestConversionEquivalence(t *testing.T) {
	rgbaData, yuvFrameGo := createTestFrames()
	defer ffmpeg.AVFrameFree(&yuvFrameGo)

	_, yuvFrameSws := createTestFrames()
	defer ffmpeg.AVFrameFree(&yuvFrameSws)

	// Convert with the production Go implementation
	pool := yuv.NewRowPool(benchHeight)
	defer pool.Close()
	convertRGBAToYUV(pool, rgbaData, yuvFrameGo, benchWidth)

	// Convert with swscale
	converter, err := NewSwsConverter(benchWidth, benchHeight)
	if err != nil {
		t.Fatalf("Failed to create sws converter: %v", err)
	}
	defer converter.Close()

	err = converter.Convert(rgbaData, yuvFrameSws)
	if err != nil {
		t.Fatalf("Swscale conversion failed: %v", err)
	}

	// Compare Y planes (they should be very close, allowing for rounding differences)
	yLinesize := yuvFrameGo.Linesize().Get(0)
	yPlaneGo := yuvFrameGo.Data().Get(0)
	yPlaneSws := yuvFrameSws.Data().Get(0)

	// BT.601 integer arithmetic vs swscale floating-point permits a
	// per-sample difference of at most 1.
	const tolerance = 1

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
			if diff > maxDiff {
				maxDiff = diff
			}
			if diff > tolerance {
				diffCount++
			}
		}
	}

	t.Logf("Y plane differences > %d: %d pixels (max diff: %d)", tolerance, diffCount, maxDiff)

	if maxDiff > tolerance {
		t.Errorf("max Y plane difference %d exceeds tolerance %d", maxDiff, tolerance)
	}
	if diffCount > 0 {
		t.Errorf("%d Y plane samples differ by more than %d", diffCount, tolerance)
	}
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

	rgbaData, yuvFrame := createTestFrames()
	defer ffmpeg.AVFrameFree(&yuvFrame)

	// Benchmark the production Go implementation
	pool := yuv.NewRowPool(benchHeight)
	defer pool.Close()

	goStart := testing.Benchmark(func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			convertRGBAToYUV(pool, rgbaData, yuvFrame, benchWidth)
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
			_ = converter.Convert(rgbaData, yuvFrame)
		}
	})

	goNs := goStart.NsPerOp()
	swsNs := swsStart.NsPerOp()
	speedup := float64(swsNs) / float64(goNs)

	fmt.Println()
	fmt.Println("╭───────────────────────────────────────────────────────────────╮")
	fmt.Println("│         RGBA→YUV420P Colourspace Conversion Benchmark         │")
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
