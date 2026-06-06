package renderer

import (
	"fmt"
	"image"
	"image/color"

	"github.com/linuxmatters/jivefire/internal/config"
	"golang.org/x/image/font"
)

// PodcastMeta bundles the episode metadata shown on a frame and thumbnail.
type PodcastMeta struct {
	Title   string
	Episode int
}

// Frame represents a single video frame with visualization bars
type Frame struct {
	img        *image.RGBA
	bgImage    *image.RGBA
	fontFace   font.Face
	centerY    int
	startX     int
	totalWidth int

	// Text overlay
	episodeNum string
	title      string
	textColor  color.RGBA // Text color for overlays

	// Pre-computed values
	maxBarHeight    int
	intensityTable  []uint8    // Pre-computed intensity values for opaque gradient (0.5 to 1.0)
	barColorTable   [][3]uint8 // Pre-computed bar colors at different intensity levels
	framingLineData []byte     // Pre-rendered framing line pixel pattern
	hasBackground   bool
}

// NewFrame creates a new optimized frame renderer
func NewFrame(bgImage *image.RGBA, fontFace font.Face, meta PodcastMeta, runtimeConfig *config.RuntimeConfig) *Frame {
	totalWidth := config.NumBars*config.BarWidth + (config.NumBars-1)*config.BarGap
	startX := (config.Width - totalWidth) / 2
	centerY := config.Height / 2

	// Calculate maximum possible bar height
	maxBarHeight := centerY - config.CenterGap/2

	// Get colors from runtime config (uses override or default)
	barR, barG, barB := runtimeConfig.GetBarColor()
	textR, textG, textB := runtimeConfig.GetTextColor()

	// Pre-compute intensity gradient table (0.5 to 1.0 range for opaque gradient)
	// This creates a fade from dim at tips to bright at center without alpha blending
	intensityTable := make([]uint8, maxBarHeight)
	for i := range maxBarHeight {
		distanceFromCenter := float64(i) / float64(maxBarHeight)
		intensityFactor := 1.0 - (distanceFromCenter * 0.5) // 0.5 at tips, 1.0 at center
		intensityTable[i] = uint8(intensityFactor * 255)
	}

	// Pre-compute bar colors at different intensity levels (0-255)
	// Colors are fully opaque - RGB values dimmed by intensity, alpha always 255
	barColorTable := make([][3]uint8, 256)
	for intensity := range 256 {
		factor := float64(intensity) / 255.0
		barColorTable[intensity][0] = uint8(float64(barR) * factor)
		barColorTable[intensity][1] = uint8(float64(barG) * factor)
		barColorTable[intensity][2] = uint8(float64(barB) * factor)
	}

	// Pre-render framing line pattern (text color from config)
	framingLineData := make([]byte, totalWidth*4)
	for px := range totalWidth {
		offset := px * 4
		framingLineData[offset] = textR   // R
		framingLineData[offset+1] = textG // G
		framingLineData[offset+2] = textB // B
		framingLineData[offset+3] = 255   // A
	}

	// Format episode number as two-digit string
	episodeStr := "00"
	if meta.Episode > 0 {
		episodeStr = formatEpisodeNumber(meta.Episode)
	}

	f := &Frame{
		img:             image.NewRGBA(image.Rect(0, 0, config.Width, config.Height)),
		bgImage:         bgImage,
		fontFace:        fontFace,
		centerY:         centerY,
		startX:          startX,
		totalWidth:      totalWidth,
		episodeNum:      episodeStr,
		title:           meta.Title,
		textColor:       color.RGBA{R: textR, G: textG, B: textB, A: 255},
		maxBarHeight:    maxBarHeight,
		intensityTable:  intensityTable,
		barColorTable:   barColorTable,
		framingLineData: framingLineData,
		hasBackground:   bgImage != nil,
	}

	return f
}

// Draw renders the visualization bars using pre-computed values
func (f *Frame) Draw(barHeights []float64) {
	// Clear or copy background
	if f.hasBackground {
		copy(f.img.Pix, f.bgImage.Pix)
	} else {
		// Fast clear to black using optimized pattern
		// Clear 8 pixels at a time for better memory bandwidth
		blackPattern := [32]byte{
			0, 0, 0, 255, 0, 0, 0, 255,
			0, 0, 0, 255, 0, 0, 0, 255,
			0, 0, 0, 255, 0, 0, 0, 255,
			0, 0, 0, 255, 0, 0, 0, 255,
		}
		for i := 0; i < len(f.img.Pix); i += 32 {
			copy(f.img.Pix[i:i+32], blackPattern[:])
		}
	}

	// Draw bars with vertical symmetry optimization
	f.drawBars(barHeights)

	// Draw framing lines around center gap
	f.drawFramingLines()

	// Apply text overlay
	if f.fontFace != nil {
		f.applyTextOverlay()
	}
}

// drawBars renders all bars using horizontal + vertical symmetry optimization.
// The frequency data is arranged symmetrically: bars 0-31 are mirrored to create bars 32-63.
// We render only the first 32 bars upward, then mirror 3 times:
//  1. Vertical mirror → bars 0-31 downward
//  2. Horizontal mirror → bars 32-63 upward
//  3. Both mirrors → bars 32-63 downward
//
// This renders 1/4 of the pixels and is ~4x faster.
func (f *Frame) drawBars(barHeights []float64) {
	// Pre-allocate pixel pattern buffer (reused for all bars)
	pixelPattern := make([]byte, config.BarWidth*4)

	// Render only left half (bars 0-31), upward only
	halfBars := config.NumBars / 2
	for i := range halfBars {
		barHeight := int(barHeights[i])
		if barHeight <= 0 {
			continue
		}

		x := f.startX + i*(config.BarWidth+config.BarGap)
		if x+config.BarWidth > config.Width {
			continue
		}

		// Clamp bar height
		if barHeight > f.maxBarHeight {
			barHeight = f.maxBarHeight
		}

		// Render upward bar only (left half) - always opaque, no background blending needed
		yStart := f.centerY - barHeight - config.CenterGap/2
		yEnd := f.centerY - config.CenterGap/2

		f.renderBar(x, yStart, yEnd, barHeight, pixelPattern)
	}

	// Now mirror in 3 operations to fill remaining 3/4 of the bars:
	// 1. Vertical mirror: bars 0-31 upward → bars 0-31 downward
	// 2. Horizontal mirror: bars 0-31 upward → bars 32-63 upward
	// 3. Both mirrors: bars 0-31 upward → bars 32-63 downward
	for i := range halfBars {
		barHeight := int(barHeights[i])
		if barHeight <= 0 {
			continue
		}

		xLeft := f.startX + i*(config.BarWidth+config.BarGap)
		xRight := f.startX + (config.NumBars-1-i)*(config.BarWidth+config.BarGap)

		yStart := f.centerY - barHeight - config.CenterGap/2
		yEnd := f.centerY - config.CenterGap/2

		// 1. Vertical mirror: create left-side downward bars
		f.mirrorBarVertical(xLeft, yStart, yEnd)

		// 2. Horizontal mirror: create right-side upward bars
		f.mirrorBarHorizontal(xLeft, xRight, yStart, yEnd)

		// 3. Both mirrors: create right-side downward bars
		f.mirrorBarVertical(xRight, yStart, yEnd)
	}
}

// renderBar renders a single upward bar with opaque gradient (no alpha blending)
func (f *Frame) renderBar(x, yStart, yEnd, barHeight int, pixelPattern []byte) {
	for y := yStart; y < yEnd; y++ {
		if y < 0 {
			continue
		}

		// Calculate intensity for fade gradient (dim at tip → bright at center)
		distanceFromCenter := yEnd - 1 - y
		intensityIndex := (distanceFromCenter * f.maxBarHeight) / barHeight
		if intensityIndex >= f.maxBarHeight {
			intensityIndex = f.maxBarHeight - 1
		}
		intensity := f.intensityTable[intensityIndex]
		colors := &f.barColorTable[intensity]

		// Fill pixel pattern once for this scanline
		for px := range config.BarWidth {
			offset := px * 4
			pixelPattern[offset] = colors[0]
			pixelPattern[offset+1] = colors[1]
			pixelPattern[offset+2] = colors[2]
			pixelPattern[offset+3] = 255 // Fully opaque
		}

		// Write entire bar width with single copy
		offset := y*f.img.Stride + x*4
		copy(f.img.Pix[offset:offset+config.BarWidth*4], pixelPattern)
	}
}

// mirrorBarVertical creates downward bar by mirroring upward bar pixels.
// Copies scanlines in reverse order to preserve the fade gradient.
func (f *Frame) mirrorBarVertical(x, yStart, yEnd int) {
	upwardHeight := yEnd - yStart
	downStart := f.centerY + config.CenterGap/2

	// Copy each scanline from upward bar in reverse order
	for i := range upwardHeight {
		srcY := yEnd - 1 - i  // Read from bottom of upward bar
		dstY := downStart + i // Write to top of downward bar

		if srcY < 0 || dstY >= config.Height {
			continue
		}

		srcOffset := srcY*f.img.Stride + x*4
		dstOffset := dstY*f.img.Stride + x*4
		copy(f.img.Pix[dstOffset:dstOffset+config.BarWidth*4],
			f.img.Pix[srcOffset:srcOffset+config.BarWidth*4])
	}
}

// mirrorBarHorizontal creates right-side bar by copying left-side bar pixels.
// Copies the entire upward bar from left position to right position.
func (f *Frame) mirrorBarHorizontal(xLeft, xRight, yStart, yEnd int) {
	// Copy each scanline from left bar to right bar
	for y := yStart; y < yEnd; y++ {
		if y < 0 || y >= config.Height {
			continue
		}

		srcOffset := y*f.img.Stride + xLeft*4
		dstOffset := y*f.img.Stride + xRight*4
		copy(f.img.Pix[dstOffset:dstOffset+config.BarWidth*4],
			f.img.Pix[srcOffset:srcOffset+config.BarWidth*4])
	}
}

// applyTextOverlay renders text onto the frame
func (f *Frame) applyTextOverlay() {
	if f.fontFace != nil {
		DrawCenterText(f.img, f.fontFace, f.title, f.centerY, f.textColor)
		DrawEpisodeNumber(f.img, f.fontFace, f.episodeNum, f.textColor)
	}
}

// drawFramingLines draws horizontal lines above and below the center gap
// using the text color from config to frame the title text
func (f *Frame) drawFramingLines() {
	lineHeight := config.FramingLineHeight

	// Calculate line positions
	// Top line: just above where upward bars end
	topLineY := f.centerY - config.CenterGap/2 - lineHeight
	// Bottom line: just below where downward bars start
	bottomLineY := f.centerY + config.CenterGap/2

	// Draw top framing line (4 pixels high) - reuse pre-rendered pattern
	for y := topLineY; y < topLineY+lineHeight; y++ {
		if y >= 0 && y < config.Height {
			offset := y*f.img.Stride + f.startX*4
			copy(f.img.Pix[offset:offset+f.totalWidth*4], f.framingLineData)
		}
	}

	// Draw bottom framing line (4 pixels high) - reuse pre-rendered pattern
	for y := bottomLineY; y < bottomLineY+lineHeight; y++ {
		if y >= 0 && y < config.Height {
			offset := y*f.img.Stride + f.startX*4
			copy(f.img.Pix[offset:offset+f.totalWidth*4], f.framingLineData)
		}
	}
}

// formatEpisodeNumber formats an episode number as a two-digit string
func formatEpisodeNumber(num int) string {
	if num < 100 {
		return fmt.Sprintf("%02d", num)
	}
	// For numbers >= 100, return as-is
	return fmt.Sprintf("%d", num)
}

// GetImage returns the current frame image
func (f *Frame) GetImage() *image.RGBA {
	return f.img
}
