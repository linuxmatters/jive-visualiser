package ui

import (
	"image"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// boxWidthFixture builds a model populated with enough state that every bordered
// box renders its full content: an audio profile, a Pass 2 render state with the
// widest codec strings, primed spectrum springs, a preview frame, and a
// completion summary. A representative window size is applied via WindowSizeMsg.
func boxWidthFixture(t *testing.T, width int) *Model {
	t.Helper()
	m := NewModel(false)
	if width > 0 {
		m.Update(tea.WindowSizeMsg{Width: width, Height: 50})
	}
	m.audioProfile = &AudioProfile{
		Duration:     100 * time.Second,
		PeakLevel:    -3.1,
		RMSLevel:     -18.4,
		DynamicRange: 15.3,
		OptimalScale: 1.234,
		AnalysisTime: 2 * time.Second,
	}
	bars := make([]float64, 64)
	for i := range bars {
		bars[i] = 0.5
	}
	m.renderState = RenderProgress{
		Frame:       250,
		TotalFrames: 1000,
		FileSize:    12345678,
		VideoCodec:  "H.264 1920×1080",
		AudioCodec:  "AAC 44.1kHz stereo",
		BarHeights:  bars,
		FrameData:   image.NewRGBA(image.Rect(0, 0, 1920, 1080)),
	}
	for i := range m.spectrumPos {
		m.spectrumPos[i] = 0.5
	}
	// Prime the speed sparkline and the Pass 2 wall clock so the gauge cards,
	// sparkline, meters and frame/source line all render with real content.
	m.pass2StartTime = time.Now().Add(-10 * time.Second)
	m.speedHistory = []float64{12, 18, 9, 25, 30, 22, 28, 31}
	m.complete = &RenderComplete{
		OutputFile:  "out.mp4",
		TotalFrames: 3000,
		TotalTime:   12 * time.Second,
		FileSize:    12345678,
		VisTime:     5 * time.Second,
		EncodeTime:  6 * time.Second,
		AudioTime:   time.Second,
		EncoderName: "libx264",
	}
	return m
}

// renderAllBoxes renders the four bordered boxes from one fixture: the Pass 1
// live box, the Pass 2 live box, the final-progress box, and the completion
// summary box.
func renderAllBoxes(m *Model) map[string]string {
	m.phase = PhaseAnalysis
	pass1 := m.renderProgress()
	m.phase = PhaseRendering
	pass2 := m.renderProgress()
	final := m.renderFinalProgress()
	complete := m.renderComplete()
	return map[string]string{
		"pass1":    pass1,
		"pass2":    pass2,
		"final":    final,
		"complete": complete,
	}
}

// TestBoxesShareOuterWidth asserts every bordered box renders at one identical
// outer width and that no line inside any box exceeds that width (which would
// signal content wrapping). It covers a wide terminal (fixed design width) and
// the unset case (also the design width).
func TestBoxesShareOuterWidth(t *testing.T) {
	tests := []struct {
		name      string
		width     int
		wantOuter int
	}{
		{name: "wide terminal uses fixed design width", width: 120, wantOuter: boxDesignWidth},
		{name: "unset width uses fixed design width", width: 0, wantOuter: boxDesignWidth},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := boxWidthFixture(t, tc.width)
			boxes := renderAllBoxes(m)

			for name, box := range boxes {
				outer := lipgloss.Width(box)
				if outer != tc.wantOuter {
					t.Errorf("%s box outer width = %d, want %d", name, outer, tc.wantOuter)
				}
				if maxLine := maxLineWidth(box); maxLine > tc.wantOuter {
					t.Errorf("%s box has a line of width %d, exceeds box width %d (content wrapping)",
						name, maxLine, tc.wantOuter)
				}
			}
		})
	}
}
