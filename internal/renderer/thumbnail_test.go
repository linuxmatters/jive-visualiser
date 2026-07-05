package renderer

import (
	"bytes"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/linuxmatters/jive-visualiser/internal/config"
)

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
