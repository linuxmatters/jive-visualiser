package renderer

import (
	"image"
	"testing"

	"github.com/linuxmatters/jive-visualiser/internal/config"
)

// newAsymmetricBackground returns a background whose pixels vary with both
// x and y. Any mirroring defect then changes the output instead of hiding
// behind a symmetric backdrop.
func newAsymmetricBackground() *image.RGBA {
	bg := image.NewRGBA(image.Rect(0, 0, config.Width, config.Height))
	for y := range config.Height {
		for x := range config.Width {
			offset := y*bg.Stride + x*4
			bg.Pix[offset] = uint8(x % 256)
			bg.Pix[offset+1] = uint8(y % 256)
			bg.Pix[offset+2] = uint8((x + y) % 256)
			bg.Pix[offset+3] = 255
		}
	}
	return bg
}

// pixelAt returns the RGBA bytes of the pixel at (x, y).
func pixelAt(img *image.RGBA, x, y int) [4]uint8 {
	offset := y*img.Stride + x*4
	return [4]uint8{img.Pix[offset], img.Pix[offset+1], img.Pix[offset+2], img.Pix[offset+3]}
}

// TestMirrorSymmetry renders one frame with every bar driven past
// maxBarHeight, so the clamp engages, then asserts the four quadrants of
// each bar column are pixel-mirrored. For each drawn bar pixel above the
// centre gap, the vertically mirrored pixel below the gap, the horizontally
// mirrored bar's pixel, and the doubly mirrored pixel must all be equal.
func TestMirrorSymmetry(t *testing.T) {
	bg := newAsymmetricBackground()
	frame := NewFrame(bg, nil, PodcastMeta{}, &config.RuntimeConfig{})

	// Overdrive every bar past maxBarHeight with varying amounts.
	barHeights := make([]float64, config.NumBars)
	for i := range barHeights {
		barHeights[i] = float64(frame.maxBarHeight) + 50 + float64(i*7)
	}
	frame.Draw(barHeights)
	img := frame.GetImage()

	yEnd := frame.centerY - config.CenterGap/2
	downStart := frame.centerY + config.CenterGap/2
	halfBars := config.NumBars / 2
	for i := range halfBars {
		clamped := min(int(barHeights[i]), frame.maxBarHeight)
		yStart := frame.centerY - clamped - config.CenterGap/2
		xLeft := frame.startX + i*(config.BarWidth+config.BarGap)
		xRight := frame.startX + (config.NumBars-1-i)*(config.BarWidth+config.BarGap)

		for y := max(yStart, 0); y < yEnd; y++ {
			// Vertical mirror: row yEnd-1 maps to downStart, and so on.
			yMirror := downStart + (yEnd - 1 - y)
			if yMirror >= config.Height {
				continue
			}
			for dx := range config.BarWidth {
				upLeft := pixelAt(img, xLeft+dx, y)
				downLeft := pixelAt(img, xLeft+dx, yMirror)
				upRight := pixelAt(img, xRight+dx, y)
				downRight := pixelAt(img, xRight+dx, yMirror)
				if upLeft != downLeft || upLeft != upRight || upLeft != downRight {
					t.Fatalf("bar %d pixel (%d,%d) not mirrored: upLeft=%v downLeft=%v upRight=%v downRight=%v",
						i, xLeft+dx, y, upLeft, downLeft, upRight, downRight)
				}
			}
		}
	}
}

// TestBarPixelColour samples a pixel inside a bar of known height and
// asserts it carries the bar colour. This replaces the old check in
// TestFrameRendering, which sampled centerY (inside CenterGap, where no bar
// is drawn) with a near-unfalsifiable background fallback.
func TestBarPixelColour(t *testing.T) {
	bg := newAsymmetricBackground()
	frame := NewFrame(bg, nil, PodcastMeta{}, &config.RuntimeConfig{})

	const barHeight = 100
	barHeights := make([]float64, config.NumBars)
	barHeights[0] = barHeight
	frame.Draw(barHeights)
	img := frame.GetImage()

	// Sample the middle of bar 0, halfway up the bar, clear of the framing
	// line that overwrites the rows nearest the centre gap.
	x := frame.startX + config.BarWidth/2
	yEnd := frame.centerY - config.CenterGap/2
	y := yEnd - barHeight/2

	got := pixelAt(img, x, y)
	background := pixelAt(bg, x, y)
	if got == background {
		t.Fatalf("pixel (%d,%d) still shows background %v, bar not drawn", x, y, got)
	}
	// Default bar colour is red (164, 0, 0), dimmed by the intensity
	// gradient. The green and blue channels stay zero at every intensity.
	if got[0] == 0 || got[1] != 0 || got[2] != 0 || got[3] != 255 {
		t.Fatalf("pixel (%d,%d) not bar-coloured: got %v, want dimmed red with G=B=0, A=255", x, y, got)
	}
}
