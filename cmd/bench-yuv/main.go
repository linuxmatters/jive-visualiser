// bench-yuv is a standalone benchmark for RGBA→YUV colour space conversion.
// Designed to be called by hyperfine for statistical analysis.
//
// Usage:
//
//	bench-yuv [--iterations N] [--impl go|swscale]
package main

import (
	"flag"
	"fmt"
	"os"
	"unsafe"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jive-visualiser/internal/yuv"
)

const (
	width  = 1280
	height = 720
)

// convertRGBAToYUVGo mirrors the production hot path (convertRGBAToYUV in
// internal/encoder/frame.go): RGBA input converted over a yuv.RowPool. The
// production function is unexported, so the loop is replicated here.
func convertRGBAToYUVGo(pool *yuv.RowPool, rgbaData []byte, yuvFrame *ffmpeg.AVFrame, width int) {
	yPlane := yuvFrame.Data().Get(0)
	uPlane := yuvFrame.Data().Get(1)
	vPlane := yuvFrame.Data().Get(2)

	yLinesize := yuvFrame.Linesize().Get(0)
	uLinesize := yuvFrame.Linesize().Get(1)
	vLinesize := yuvFrame.Linesize().Get(2)

	pool.Run(func(startY, endY int) {
		// Align startY to even for correct UV row calculation
		evenStart := startY
		if evenStart&1 != 0 {
			evenStart++
		}

		// Process even rows: Y + UV
		for y := evenStart; y < endY; y += 2 {
			yPtr := unsafe.Add(yPlane, y*yLinesize)
			uvY := y >> 1
			uRowPtr := unsafe.Add(uPlane, uvY*uLinesize)
			vRowPtr := unsafe.Add(vPlane, uvY*vLinesize)
			rgbaIdx := y * width * 4

			for x := range width {
				r := int32(rgbaData[rgbaIdx])
				g := int32(rgbaData[rgbaIdx+1])
				b := int32(rgbaData[rgbaIdx+2])
				rgbaIdx += 4 // Skip alpha

				*(*uint8)(unsafe.Add(yPtr, x)) = yuv.RGBToY(r, g, b)

				// UV subsampling: every other pixel on even rows
				if (x & 1) == 0 {
					uvX := x >> 1
					*(*uint8)(unsafe.Add(uRowPtr, uvX)) = yuv.RGBToCb(r, g, b)
					*(*uint8)(unsafe.Add(vRowPtr, uvX)) = yuv.RGBToCr(r, g, b)
				}
			}
		}

		// Process odd rows: Y only (no UV)
		oddStart := startY
		if oddStart&1 == 0 {
			oddStart++
		}
		for y := oddStart; y < endY; y += 2 {
			yPtr := unsafe.Add(yPlane, y*yLinesize)
			rgbaIdx := y * width * 4

			for x := range width {
				r := int32(rgbaData[rgbaIdx])
				g := int32(rgbaData[rgbaIdx+1])
				b := int32(rgbaData[rgbaIdx+2])
				rgbaIdx += 4 // Skip alpha

				*(*uint8)(unsafe.Add(yPtr, x)) = yuv.RGBToY(r, g, b)
			}
		}
	})
}

func checkFFmpeg(ret int, err error, op string) error {
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	if ret < 0 {
		return fmt.Errorf("%s: %w", op, ffmpeg.WrapErr(ret))
	}
	return nil
}

func allocateFrame(name string, pixFmt ffmpeg.AVPixelFormat, width, height int) (*ffmpeg.AVFrame, error) {
	frame := ffmpeg.AVFrameAlloc()
	if frame == nil {
		return nil, fmt.Errorf("%s frame allocation failed", name)
	}
	frame.SetWidth(width)
	frame.SetHeight(height)
	frame.SetFormat(int(pixFmt))

	ret, err := ffmpeg.AVFrameGetBuffer(frame, 0)
	if err := checkFFmpeg(ret, err, name+" frame buffer allocation"); err != nil {
		ffmpeg.AVFrameFree(&frame)
		return nil, err
	}

	return frame, nil
}

func convertSwscale(rgbaData []byte, yuvFrame *ffmpeg.AVFrame, swsCtx *ffmpeg.SwsContext, srcFrame *ffmpeg.AVFrame, width, height int) error {
	// Copy RGBA data into source frame
	srcLinesize := srcFrame.Linesize().Get(0)
	srcData := srcFrame.Data().Get(0)

	for y := range height {
		srcOffset := y * srcLinesize
		rgbaOffset := y * width * 4
		for x := 0; x < width*4; x++ {
			*(*uint8)(unsafe.Add(srcData, srcOffset+x)) = rgbaData[rgbaOffset+x]
		}
	}

	ret, err := ffmpeg.SwsScaleFrame(swsCtx, yuvFrame, srcFrame)
	return checkFFmpeg(ret, err, "SwsScaleFrame")
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "bench-yuv: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	iterations := flag.Int("iterations", 1000, "number of conversions to perform")
	impl := flag.String("impl", "go", "implementation: go or swscale")
	flag.Parse()

	if *impl != "go" && *impl != "swscale" {
		return fmt.Errorf("unknown implementation: %s (use 'go' or 'swscale')", *impl)
	}

	rgbaSize := width * height * 4
	rgbaData := make([]byte, rgbaSize)
	for i := 0; i < rgbaSize; i += 4 {
		rgbaData[i] = uint8(i % 256)   // R
		rgbaData[i+1] = uint8(i % 128) // G
		rgbaData[i+2] = uint8(i % 64)  // B
		rgbaData[i+3] = 255            // A
	}

	yuvFrame, err := allocateFrame("YUV output", ffmpeg.AVPixFmtYuv420P, width, height)
	if err != nil {
		return err
	}
	defer ffmpeg.AVFrameFree(&yuvFrame)

	switch *impl {
	case "go":
		pool := yuv.NewRowPool(height)
		defer pool.Close()

		for i := 0; i < *iterations; i++ {
			convertRGBAToYUVGo(pool, rgbaData, yuvFrame, width)
		}
	case "swscale":
		swsCtx := ffmpeg.SwsAllocContext()
		if swsCtx == nil {
			return fmt.Errorf("swscale context allocation failed")
		}
		swsCtx.SetSrcW(width)
		swsCtx.SetSrcH(height)
		swsCtx.SetSrcFormat(int(ffmpeg.AVPixFmtRgba))
		swsCtx.SetDstW(width)
		swsCtx.SetDstH(height)
		swsCtx.SetDstFormat(int(ffmpeg.AVPixFmtYuv420P))
		swsCtx.SetFlags(uint(ffmpeg.SwsBilinear))
		ret, err := ffmpeg.SwsInitContext(swsCtx, nil, nil)
		if err := checkFFmpeg(ret, err, "swscale context initialisation"); err != nil {
			ffmpeg.SwsFreecontext(swsCtx)
			return err
		}
		defer ffmpeg.SwsFreecontext(swsCtx)

		srcFrame, err := allocateFrame("RGBA source", ffmpeg.AVPixFmtRgba, width, height)
		if err != nil {
			return err
		}
		defer ffmpeg.AVFrameFree(&srcFrame)

		for i := 0; i < *iterations; i++ {
			if err := convertSwscale(rgbaData, yuvFrame, swsCtx, srcFrame, width, height); err != nil {
				return err
			}
		}
	}

	return nil
}
