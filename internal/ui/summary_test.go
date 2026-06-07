package ui

import (
	"strings"
	"testing"
	"time"
)

// TestRenderCompleteUsesTable verifies the completion summary renders through
// lipgloss/table rather than the old %-18s/%-6s manual padding: the rendered
// output must no longer contain a fixed 18-char label run, yet must still carry
// every key label and value.
func TestRenderCompleteUsesTable(t *testing.T) {
	m := NewModel(true)
	m.audioProfile = &AudioProfile{
		Duration:     185 * time.Second,
		PeakLevel:    -1.2,
		RMSLevel:     -14.8,
		DynamicRange: 13.6,
		OptimalScale: 1.234,
		AnalysisTime: 1500 * time.Millisecond,
	}
	m.complete = &RenderComplete{
		OutputFile:    "out.mp4",
		TotalFrames:   4500,
		ThumbnailTime: 50 * time.Millisecond,
		VisTime:       3 * time.Second,
		EncodeTime:    8 * time.Second,
		AudioTime:     900 * time.Millisecond,
		TotalTime:     13 * time.Second,
		FileSize:      10485760,
		EncoderName:   "libx264",
	}

	out := stripStyles(m.renderComplete())

	// The old writeRow/writeBarRow padded each label to a fixed 18-character
	// field via %-18s, so a label was always followed by spaces up to column 18
	// (e.g. "Duration:" + 9 trailing spaces). The table aligns columns to the
	// widest cell instead, so those padded-to-18 runs must be gone.
	fixed18 := map[string]string{
		"Duration:":      "Duration:" + strings.Repeat(" ", 18-len("Duration:")),
		"Peak Level:":    "Peak Level:" + strings.Repeat(" ", 18-len("Peak Level:")),
		"Total time:":    "Total time:" + strings.Repeat(" ", 18-len("Total time:")),
		"Visualisation:": "Visualisation:" + strings.Repeat(" ", 18-len("Visualisation:")),
	}
	for label, padded := range fixed18 {
		if strings.Contains(out, padded) {
			t.Errorf("output still contains the old %%-18s padding for %q (fixed 18-char label run)", label)
		}
	}

	// The same information must survive the layout swap: section headers, every
	// Pass 1 label and Pass 2 breakdown label, and representative values.
	wantSubstrings := []string{
		"Pass 1: Audio Analysis",
		"Pass 2: Rendering & Encoding",
		"Duration:", "185.0s",
		"Peak Level:", "-1.2 dB",
		"RMS Level:", "-14.8 dB",
		"Dynamic Range:", "13.6 dB",
		"Optimal Scale:", "1.234",
		"Analysis Time:", "1.5s",
		"Thumbnail:",
		"Visualisation:",
		"Video encoding:",
		"Audio encoding:",
		"Total time:", "13.0s",
	}
	for _, want := range wantSubstrings {
		if !strings.Contains(out, want) {
			t.Errorf("completion summary missing %q", want)
		}
	}

	// CompletionSummary() shares the render path used post-exit from main.go; it
	// must produce the same table-based summary.
	if !strings.Contains(stripStyles(m.CompletionSummary()), "Pass 1: Audio Analysis") {
		t.Error("CompletionSummary missing table-based Pass 1 section")
	}
}

// TestRenderAudioProfileAligns verifies the audio profile renders as horizontal
// level meters (Peak/RMS/Range/Scale, two per row) with aligned bars, keeps the
// metric labels and values, and preserves the placeholder branch when no profile
// is populated yet.
func TestRenderAudioProfileAligns(t *testing.T) {
	t.Run("placeholder branch preserved", func(t *testing.T) {
		m := NewModel(true) // audioProfile is nil
		var s strings.Builder
		m.renderAudioProfile(&s)
		out := stripStyles(s.String())

		if !strings.Contains(out, "Analysing...") {
			t.Errorf("placeholder branch missing %q, got %q", "Analysing...", out)
		}
		if !strings.Contains(out, "Audio") {
			t.Errorf("placeholder branch missing the Audio header, got %q", out)
		}
	})

	t.Run("populated metrics render as aligned meters", func(t *testing.T) {
		m := NewModel(true)
		m.audioProfile = &AudioProfile{
			Duration:     185 * time.Second,
			PeakLevel:    -1.2,
			RMSLevel:     -14.8,
			DynamicRange: 13.6,
			OptimalScale: 1.234,
		}
		var s strings.Builder
		m.renderAudioProfile(&s)
		out := stripStyles(s.String())

		// Two rows: Peak | Range, then RMS | Scale.
		rows := strings.Split(out, "\n")
		if len(rows) != 2 {
			t.Fatalf("expected 2 meter rows, got %d: %q", len(rows), out)
		}

		// Labels and values survive the meter layout.
		wantSubstrings := []string{
			"Peak", "-1.2",
			"RMS", "-14.8",
			"Range", "13.6 dB",
			"Scale", "1.234",
		}
		for _, want := range wantSubstrings {
			if !strings.Contains(out, want) {
				t.Errorf("populated audio profile missing %q, got %q", want, out)
			}
		}

		// Each row carries meter bar caps and at least one filled cell.
		for i, row := range rows {
			if strings.Count(row, "▕") != 2 || strings.Count(row, "▏") != 2 {
				t.Errorf("row %d missing the two meter bar caps: %q", i, row)
			}
			if !strings.ContainsRune(row, '█') {
				t.Errorf("row %d has no filled meter cells: %q", i, row)
			}
		}

		// Bars align: the right-column meter ("Range"/"Scale") starts at the same
		// column on both rows.
		col0 := strings.Index(rows[0], "Range")
		col1 := strings.Index(rows[1], "Scale")
		if col0 != col1 {
			t.Errorf("right-column meters misaligned: Range at %d, Scale at %d", col0, col1)
		}
	})
}
