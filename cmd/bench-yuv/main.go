// bench-yuv is a standalone benchmark for RGB→YUV colour space conversion.
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
	"github.com/linuxmatters/jivefire/internal/yuv"
)

const (
	width  = 1280
	height = 720
)

func convertRGBToYUVGo(rgbData []byte, yuvFrame *ffmpeg.AVFrame, width, height int) {
	yPlane := yuvFrame.Data().Get(0)
	uPlane := yuvFrame.Data().Get(1)
	vPlane := yuvFrame.Data().Get(2)

	yLinesize := yuvFrame.Linesize().Get(0)
	uLinesize := yuvFrame.Linesize().Get(1)
	vLinesize := yuvFrame.Linesize().Get(2)

	yuv.ParallelRows(height, func(startY, endY int) {
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
			rgbIdx := y * width * 3

			for x := range width {
				r := int32(rgbData[rgbIdx])
				g := int32(rgbData[rgbIdx+1])
				b := int32(rgbData[rgbIdx+2])
				rgbIdx += 3

				*(*uint8)(unsafe.Add(yPtr, x)) = yuv.RGBToY(r, g, b)

				if (x & 1) == 0 {
					uvX := x >> 1
					*(*uint8)(unsafe.Add(uRowPtr, uvX)) = yuv.RGBToCb(r, g, b)
					*(*uint8)(unsafe.Add(vRowPtr, uvX)) = yuv.RGBToCr(r, g, b)
				}
			}
		}

		// Process odd rows: Y only
		oddStart := startY
		if oddStart&1 == 0 {
			oddStart++
		}
		for y := oddStart; y < endY; y += 2 {
			yPtr := unsafe.Add(yPlane, y*yLinesize)
			rgbIdx := y * width * 3

			for x := range width {
				r := int32(rgbData[rgbIdx])
				g := int32(rgbData[rgbIdx+1])
				b := int32(rgbData[rgbIdx+2])
				rgbIdx += 3

				*(*uint8)(unsafe.Add(yPtr, x)) = yuv.RGBToY(r, g, b)
			}
		}
	})
}

func convertSwscale(rgbData []byte, yuvFrame *ffmpeg.AVFrame, swsCtx *ffmpeg.SwsContext, srcFrame *ffmpeg.AVFrame, width, height int) {
	// Copy RGB data into source frame
	srcLinesize := srcFrame.Linesize().Get(0)
	srcData := srcFrame.Data().Get(0)

	for y := range height {
		srcOffset := y * srcLinesize
		rgbOffset := y * width * 3
		for x := 0; x < width*3; x++ {
			*(*uint8)(unsafe.Add(srcData, srcOffset+x)) = rgbData[rgbOffset+x]
		}
	}

	_, _ = ffmpeg.SwsScaleFrame(swsCtx, yuvFrame, srcFrame)
}

func main() {
	iterations := flag.Int("iterations", 1000, "number of conversions to perform")
	impl := flag.String("impl", "go", "implementation: go or swscale")
	flag.Parse()

	if *impl != "go" && *impl != "swscale" {
		fmt.Fprintf(os.Stderr, "Unknown implementation: %s (use 'go' or 'swscale')\n", *impl)
		os.Exit(1)
	}

	// Create test RGB data
	rgbSize := width * height * 3
	rgbData := make([]byte, rgbSize)
	for i := 0; i < rgbSize; i += 3 {
		rgbData[i] = uint8(i % 256)
		rgbData[i+1] = uint8(i % 128)
		rgbData[i+2] = uint8(i % 64)
	}

	// Allocate YUV frame
	yuvFrame := ffmpeg.AVFrameAlloc()
	yuvFrame.SetWidth(width)
	yuvFrame.SetHeight(height)
	yuvFrame.SetFormat(int(ffmpeg.AVPixFmtYuv420P))
	_, _ = ffmpeg.AVFrameGetBuffer(yuvFrame, 0)
	defer ffmpeg.AVFrameFree(&yuvFrame)

	switch *impl {
	case "go":
		for i := 0; i < *iterations; i++ {
			convertRGBToYUVGo(rgbData, yuvFrame, width, height)
		}
	case "swscale":
		// Set up swscale context
		swsCtx := ffmpeg.SwsAllocContext()
		swsCtx.SetSrcW(width)
		swsCtx.SetSrcH(height)
		swsCtx.SetSrcFormat(int(ffmpeg.AVPixFmtRgb24))
		swsCtx.SetDstW(width)
		swsCtx.SetDstH(height)
		swsCtx.SetDstFormat(int(ffmpeg.AVPixFmtYuv420P))
		swsCtx.SetFlags(uint(ffmpeg.SwsBilinear))
		_, _ = ffmpeg.SwsInitContext(swsCtx, nil, nil)
		defer ffmpeg.SwsFreecontext(swsCtx)

		srcFrame := ffmpeg.AVFrameAlloc()
		srcFrame.SetWidth(width)
		srcFrame.SetHeight(height)
		srcFrame.SetFormat(int(ffmpeg.AVPixFmtRgb24))
		_, _ = ffmpeg.AVFrameGetBuffer(srcFrame, 0)
		defer ffmpeg.AVFrameFree(&srcFrame)

		for i := 0; i < *iterations; i++ {
			convertSwscale(rgbData, yuvFrame, swsCtx, srcFrame, width, height)
		}
	}
}
