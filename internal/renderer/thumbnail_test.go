package renderer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/linuxmatters/jivefire/internal/config"
)

// TestGenerateSampleThumbnail generates a sample thumbnail for development/testing
// This serves both as a test and as a useful development tool
func TestGenerateSampleThumbnail(t *testing.T) {
	// Test with real recent episode titles
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

	// Use default config (nil runtimeConfig uses defaults)
	runtimeConfig := &config.RuntimeConfig{}

	for _, tc := range testCases {
		t.Run(tc.title, func(t *testing.T) {
			// Create output in testdata directory
			outputPath := filepath.Join("../../testdata", tc.outputName)

			// Generate thumbnail
			err := GenerateThumbnail(outputPath, PodcastMeta{Title: tc.title}, runtimeConfig)
			if err != nil {
				t.Fatalf("failed to generate thumbnail: %v", err)
			}

			// Verify file was created
			if _, err := os.Stat(outputPath); os.IsNotExist(err) {
				t.Fatalf("thumbnail file was not created: %s", outputPath)
			}

			t.Logf("✓ Generated sample thumbnail: %s", outputPath)
		})
	}
}
