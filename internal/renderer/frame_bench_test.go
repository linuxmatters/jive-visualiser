package renderer

import (
	"image"
	"testing"

	"github.com/linuxmatters/jivefire/internal/config"
	"golang.org/x/image/font/basicfont"
)

// generateTestBarHeights creates test bar heights for benchmarking
func generateTestBarHeights() []float64 {
	heights := make([]float64, config.NumBars)
	for i := range heights {
		// Create a wave pattern
		heights[i] = float64(config.Height/4) * (1.0 + float64(i%8)/8.0)
	}
	return heights
}

// BenchmarkFrameWithBackground benchmarks frame rendering with background
func BenchmarkFrameWithBackground(b *testing.B) {
	// Setup
	bgImage := image.NewRGBA(image.Rect(0, 0, config.Width, config.Height))
	// Fill background with test pattern
	for i := 0; i < len(bgImage.Pix); i += 4 {
		bgImage.Pix[i] = 64
		bgImage.Pix[i+1] = 64
		bgImage.Pix[i+2] = 64
		bgImage.Pix[i+3] = 255
	}

	runtimeConfig := &config.RuntimeConfig{}
	frame := NewFrame(bgImage, nil, PodcastMeta{}, runtimeConfig)
	barHeights := generateTestBarHeights()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		frame.Draw(barHeights)
	}
}

// BenchmarkFrameNoBackground benchmarks frame rendering without background (black)
func BenchmarkFrameNoBackground(b *testing.B) {
	// Setup - no background
	runtimeConfig := &config.RuntimeConfig{}
	frame := NewFrame(nil, nil, PodcastMeta{}, runtimeConfig)
	barHeights := generateTestBarHeights()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		frame.Draw(barHeights)
	}
}

// BenchmarkFrameWithText benchmarks frame rendering with text overlay
func BenchmarkFrameWithText(b *testing.B) {
	// Setup
	bgImage := image.NewRGBA(image.Rect(0, 0, config.Width, config.Height))
	fontFace := basicfont.Face7x13
	runtimeConfig := &config.RuntimeConfig{}
	frame := NewFrame(bgImage, fontFace, PodcastMeta{Title: "Test Episode", Episode: new(1)}, runtimeConfig)
	barHeights := generateTestBarHeights()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		frame.Draw(barHeights)
	}
}

// TestFrameRendering verifies that frame rendering produces expected output
func TestFrameRendering(t *testing.T) {
	bgImage := image.NewRGBA(image.Rect(0, 0, config.Width, config.Height))
	// Fill with a simple pattern
	for y := range config.Height {
		for x := range config.Width {
			offset := y*bgImage.Stride + x*4
			bgImage.Pix[offset] = uint8(x % 256)
			bgImage.Pix[offset+1] = uint8(y % 256)
			bgImage.Pix[offset+2] = uint8((x + y) % 256)
			bgImage.Pix[offset+3] = 255
		}
	}

	fontFace := basicfont.Face7x13
	runtimeConfig := &config.RuntimeConfig{}
	frame := NewFrame(bgImage, fontFace, PodcastMeta{Title: "Linux Matters", Episode: new(42)}, runtimeConfig)

	// Test with various bar heights
	barHeights := generateTestBarHeights()
	frame.Draw(barHeights)

	img := frame.GetImage()

	// Verify image dimensions
	if img.Bounds().Dx() != config.Width || img.Bounds().Dy() != config.Height {
		t.Errorf("Image dimensions incorrect: got %dx%d, want %dx%d",
			img.Bounds().Dx(), img.Bounds().Dy(), config.Width, config.Height)
	}

	// Check that bars were drawn (center should have bar color)
	centerY := config.Height / 2

	// Find a bar position
	totalWidth := config.NumBars*config.BarWidth + (config.NumBars-1)*config.BarGap
	startX := (config.Width - totalWidth) / 2

	// Check first bar area
	barX := startX + config.BarWidth/2
	offset := centerY*img.Stride + barX*4

	// Should see bar color (red) or background depending on bar height
	r := img.Pix[offset]
	if r != config.BarColorR && r != uint8(barX%256) {
		t.Errorf("Unexpected color at bar position: got R=%d", r)
	}
}
