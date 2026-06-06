package encoder

// TODO: When Go 1.26 ships with the simd package, revisit with inlinable AVX2
// intrinsics for potentially 30-50% additional gains in colour space conversion.

import (
	"unsafe"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jivefire/internal/yuv"
)

// convertRGBAToYUV converts RGBA data directly to YUV420P (planar) format.
// Skips the intermediate RGB24 buffer allocation for significantly faster software encoding.
func convertRGBAToYUV(rgbaData []byte, yuvFrame *ffmpeg.AVFrame, width, height int) {
	yPlane := yuvFrame.Data().Get(0)
	uPlane := yuvFrame.Data().Get(1)
	vPlane := yuvFrame.Data().Get(2)

	yLinesize := yuvFrame.Linesize().Get(0)
	uLinesize := yuvFrame.Linesize().Get(1)
	vLinesize := yuvFrame.Linesize().Get(2)

	yuv.ParallelRows(height, func(startY, endY int) {
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

// convertRGBAToNV12 converts RGBA data to NV12 (semi-planar) format.
// NV12 has a Y plane followed by interleaved UV plane.
func convertRGBAToNV12(rgbaData []byte, nv12Frame *ffmpeg.AVFrame, width, height int) {
	yPlane := nv12Frame.Data().Get(0)
	uvPlane := nv12Frame.Data().Get(1)

	yLinesize := nv12Frame.Linesize().Get(0)
	uvLinesize := nv12Frame.Linesize().Get(1)

	yuv.ParallelRows(height, func(startY, endY int) {
		// Align startY to even for correct UV row calculation
		evenStart := startY
		if evenStart&1 != 0 {
			evenStart++
		}

		// Process even rows: Y + UV
		for y := evenStart; y < endY; y += 2 {
			yPtr := unsafe.Add(yPlane, y*yLinesize)
			uvY := y >> 1
			uvRowPtr := unsafe.Add(uvPlane, uvY*uvLinesize)
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
					uvPtr := unsafe.Add(uvRowPtr, uvX*2)
					*(*uint8)(uvPtr) = yuv.RGBToCb(r, g, b)
					*(*uint8)(unsafe.Add(uvPtr, 1)) = yuv.RGBToCr(r, g, b)
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
