package renderer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/linuxmatters/jive-visualiser/internal/config"
)

// TestGenerateSampleThumbnail generates a sample thumbnail for development/testing
// This serves both as a test and as a useful development tool
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

	for _, tc := range testCases {
		t.Run(tc.title, func(t *testing.T) {
			outputPath := filepath.Join("../../testdata", tc.outputName)

			err := GenerateThumbnail(outputPath, PodcastMeta{Title: tc.title}, runtimeConfig)
			if err != nil {
				t.Fatalf("failed to generate thumbnail: %v", err)
			}

			if _, err := os.Stat(outputPath); os.IsNotExist(err) {
				t.Fatalf("thumbnail file was not created: %s", outputPath)
			}

			t.Logf("✓ Generated sample thumbnail: %s", outputPath)
		})
	}
}
