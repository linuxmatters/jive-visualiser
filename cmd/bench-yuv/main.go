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
	"github.com/linuxmatters/jive-visualiser/internal/encoder"
	"github.com/linuxmatters/jive-visualiser/internal/yuv"
)

const (
	width  = 1280
	height = 720
)

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

func uploadSwscaleSource(rgbaData []byte, srcFrame *ffmpeg.AVFrame, width, height int) {
	srcLinesize := srcFrame.Linesize().Get(0)
	srcData := srcFrame.Data().Get(0)
	rowBytes := width * 4

	for y := range height {
		srcRow := unsafe.Slice((*byte)(unsafe.Add(srcData, y*srcLinesize)), rowBytes)
		rgbaRow := rgbaData[y*rowBytes : (y+1)*rowBytes]
		copy(srcRow, rgbaRow)
	}
}

func scaleSwscale(yuvFrame *ffmpeg.AVFrame, swsCtx *ffmpeg.SwsContext, srcFrame *ffmpeg.AVFrame) error {
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
			encoder.ConvertRGBAToYUV(pool, rgbaData, yuvFrame, width)
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

		uploadSwscaleSource(rgbaData, srcFrame, width, height)
		for i := 0; i < *iterations; i++ {
			if err := scaleSwscale(yuvFrame, swsCtx, srcFrame); err != nil {
				return err
			}
		}
	}

	return nil
}
