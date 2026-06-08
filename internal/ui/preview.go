package ui

import (
	"image"
	"image/color"
	"strconv"
	"strings"
)

// PreviewConfig holds configuration for the video preview
type PreviewConfig struct {
	Width  int // Width in terminal cells
	Height int // Height in terminal cells
}

// DefaultPreviewConfig returns a sensible default preview size
// Using 72x20 1.8:1 (slightly wider than 16:9 but very close)
func DefaultPreviewConfig() PreviewConfig {
	return PreviewConfig{
		Width:  72,
		Height: 20,
	}
}

// DownsampleFrame takes a full-resolution RGB frame and downsamples it to preview size
// Each terminal cell represents a rectangular region of the source image
// Averages all pixels in each region for smooth, high-quality downsampling
func DownsampleFrame(frame *image.RGBA, config PreviewConfig) [][]color.RGBA {
	bounds := frame.Bounds()
	srcWidth := bounds.Dx()
	srcHeight := bounds.Dy()

	// Source pixels covered by each terminal cell.
	cellWidth := srcWidth / config.Width
	cellHeight := srcHeight / config.Height

	preview := make([][]color.RGBA, config.Height)

	// Access the pixel buffer directly; much faster than frame.At().
	stride := frame.Stride
	pix := frame.Pix

	for row := 0; row < config.Height; row++ {
		preview[row] = make([]color.RGBA, config.Width)
		for col := 0; col < config.Width; col++ {
			srcX := col * cellWidth
			srcY := row * cellHeight

			// Average every source pixel in this cell's region.
			var sumR, sumG, sumB uint32
			pixelCount := uint32(0)

			for y := srcY; y < srcY+cellHeight && y < srcHeight; y++ {
				offset := y*stride + srcX*4
				for x := 0; x < cellWidth && srcX+x < srcWidth; x++ {
					sumR += uint32(pix[offset])
					sumG += uint32(pix[offset+1])
					sumB += uint32(pix[offset+2])
					offset += 4
					pixelCount++
				}
			}

			if pixelCount > 0 {
				preview[row][col] = color.RGBA{
					R: uint8(sumR / pixelCount), //nolint:gosec // average of uint8 values fits in uint8
					G: uint8(sumG / pixelCount), //nolint:gosec // average of uint8 values fits in uint8
					B: uint8(sumB / pixelCount), //nolint:gosec // average of uint8 values fits in uint8
					A: 255,
				}
			}
		}
	}

	return preview
}

// RenderPreview converts an RGB preview grid to a string representation
// using ANSI 24-bit true color escape codes for beautiful colored rendering
func RenderPreview(preview [][]color.RGBA) string {
	if len(preview) == 0 {
		return ""
	}

	var builder strings.Builder
	// Roughly ~20 bytes per pixel (ANSI escape) plus borders.
	builder.Grow(len(preview) * len(preview[0]) * 20)

	builder.WriteString("\n┌")
	builder.WriteString(strings.Repeat("─", len(preview[0])))
	builder.WriteString("┐\n")

	colorBuf := make([]byte, 0, 32)

	for _, row := range preview {
		builder.WriteString("│")
		for _, pixel := range row {
			// Build the ANSI escape by hand; faster than fmt.Sprintf.
			colorBuf = colorBuf[:0]
			colorBuf = append(colorBuf, "\x1b[48;2;"...)
			colorBuf = strconv.AppendInt(colorBuf, int64(pixel.R), 10)
			colorBuf = append(colorBuf, ';')
			colorBuf = strconv.AppendInt(colorBuf, int64(pixel.G), 10)
			colorBuf = append(colorBuf, ';')
			colorBuf = strconv.AppendInt(colorBuf, int64(pixel.B), 10)
			colorBuf = append(colorBuf, "m \x1b[0m"...)
			builder.Write(colorBuf)
		}
		builder.WriteString("│\n")
	}

	builder.WriteString("└")
	builder.WriteString(strings.Repeat("─", len(preview[0])))
	builder.WriteString("┘")

	return builder.String()
}
