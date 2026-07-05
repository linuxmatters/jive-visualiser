package renderer

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/linuxmatters/jive-visualiser/internal/config"
	"golang.org/x/image/draw"
)

// TestThumbnailTextPlacement pins where the rotated title text lands, not just
// the image dimensions. It renders the same thumbnail twice with two different
// text colours; the background is identical between the two, so the pixels that
// differ are exactly the text. It then checks the vertical extent of those text
// pixels against the placement invariant in drawThumbnailText: the highest text
// pixel should sit roughly config.ThumbnailMargin from the top, and no text
// should reach the bottom half of the image.
//
// The test asserts observed placement; it does not re-derive the affine formula.
func TestThumbnailTextPlacement(t *testing.T) {
	// Multi-word title so both title lines are non-empty (see splitTitle).
	const title = "Frankenstein's Ubuntu Server Framework"
	outputDir := t.TempDir()

	// Two runtime configs identical except for the text colour. The overridden
	// colour drives getThumbnailTextColor, so the text is the only difference.
	cfgA := &config.RuntimeConfig{
		TextColor: config.OptionalColor{R: 255, G: 0, B: 0, Set: true},
	}
	cfgB := &config.RuntimeConfig{
		TextColor: config.OptionalColor{R: 0, G: 255, B: 0, Set: true},
	}

	imgA := generateThumbnailImage(t, outputDir, "placement_a.png", title, cfgA)
	imgB := generateThumbnailImage(t, outputDir, "placement_b.png", title, cfgB)

	if imgA.Bounds() != imgB.Bounds() {
		t.Fatalf("thumbnail bounds differ: %v vs %v", imgA.Bounds(), imgB.Bounds())
	}

	// Collect the vertical extent of pixels that differ between the two renders.
	// Those differing pixels are the title text.
	minY, maxY := -1, -1
	diffCount := 0
	for y := range config.Height {
		for x := range config.Width {
			if pixelAt(imgA, x, y) != pixelAt(imgB, x, y) {
				diffCount++
				if minY == -1 || y < minY {
					minY = y
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}

	if diffCount == 0 {
		t.Fatal("no differing pixels between the two renders; text not drawn")
	}

	// Invariant 1: the highest text pixel aligns with ThumbnailMargin from the
	// top. Allow a tolerance band for anti-aliasing, rounding, and the fact that
	// the rotated line-1 baseline geometry places the visible top a few pixels
	// off the ideal. The band is generous but still excludes gross regressions.
	const tolerance = 20
	wantTop := config.ThumbnailMargin
	if minY < wantTop-tolerance || minY > wantTop+tolerance {
		t.Fatalf("highest text pixel Y = %d, want within %d of %d (ThumbnailMargin)",
			minY, tolerance, wantTop)
	}

	// Invariant 2: text stays in the top region. drawThumbnailText and
	// findOptimalFontSize keep both lines above the vertical centre, so no text
	// pixel should reach the bottom half of the image.
	centreY := config.Height / 2
	if maxY >= centreY {
		t.Fatalf("lowest text pixel Y = %d reaches bottom half (centre %d); text not kept in top region",
			maxY, centreY)
	}
}

// generateThumbnailImage writes a thumbnail for the given title and config, then
// decodes it back into an *image.RGBA for pixel inspection.
func generateThumbnailImage(t *testing.T, dir, name, title string, cfg *config.RuntimeConfig) *image.RGBA {
	t.Helper()

	outputPath := filepath.Join(dir, name)
	if err := GenerateThumbnail(outputPath, PodcastMeta{Title: title}, cfg); err != nil {
		t.Fatalf("failed to generate thumbnail: %v", err)
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("failed to read thumbnail: %v", err)
	}

	decoded, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("failed to decode thumbnail PNG: %v", err)
	}

	rgba, ok := decoded.(*image.RGBA)
	if !ok {
		rgba = image.NewRGBA(decoded.Bounds())
		draw.Draw(rgba, rgba.Bounds(), decoded, decoded.Bounds().Min, draw.Src)
	}
	return rgba
}

// TestGenerateSampleThumbnail verifies generated thumbnails without writing to the repository.
func TestGenerateSampleThumbnail(t *testing.T) {
	testCases := []struct {
		title      string
		outputName string
	}{
		{
			title:      "Panache, for Men",
			outputName: "test_thumbnail_3words.png",
		},
		{
			title:      "Frankenstein's Ubuntu Server Framework",
			outputName: "test_thumbnail_4words.png",
		},
		{
			title:      "High Precision Solid Metal Balls",
			outputName: "test_thumbnail_5words.png",
		},
	}

	// An empty RuntimeConfig falls back to the default colours and assets.
	runtimeConfig := &config.RuntimeConfig{}
	outputDir := t.TempDir()
	outputs := make(map[string][]byte)

	for _, tc := range testCases {
		t.Run(tc.title, func(t *testing.T) {
			outputPath := filepath.Join(outputDir, tc.outputName)

			err := GenerateThumbnail(outputPath, PodcastMeta{Title: tc.title}, runtimeConfig)
			if err != nil {
				t.Fatalf("failed to generate thumbnail: %v", err)
			}

			data, err := os.ReadFile(outputPath)
			if err != nil {
				t.Fatalf("failed to read thumbnail: %v", err)
			}

			img, err := png.Decode(bytes.NewReader(data))
			if err != nil {
				t.Fatalf("failed to decode thumbnail PNG: %v", err)
			}

			bounds := img.Bounds()
			if bounds.Dx() != config.Width || bounds.Dy() != config.Height {
				t.Fatalf("thumbnail dimensions = %dx%d, want %dx%d",
					bounds.Dx(), bounds.Dy(), config.Width, config.Height)
			}

			outputs[tc.title] = data
		})
	}

	for i, first := range testCases {
		for _, second := range testCases[i+1:] {
			if bytes.Equal(outputs[first.title], outputs[second.title]) {
				t.Fatalf("thumbnails for %q and %q have identical output data", first.title, second.title)
			}
		}
	}
}
