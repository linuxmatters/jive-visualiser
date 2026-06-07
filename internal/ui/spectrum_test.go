package ui

import (
	"fmt"
	"image/color"
	"strings"
	"testing"

	"github.com/linuxmatters/jivefire/internal/config"
	"github.com/linuxmatters/jivefire/internal/theme"
)

// TestRenderSpectrumUsesThemeRamp verifies renderSpectrum colours its bars from
// the theme-provided go-colorful Lab ramp: the ramp is consumed (FireSpectrum has
// stops), block runes survive, the output carries ANSI colour codes, and a hot
// bar is styled differently from a cool bar — proving the per-cell colour-index
// mapping still works across the new ramp length.
func TestRenderSpectrumUsesThemeRamp(t *testing.T) {
	if len(theme.FireSpectrum) == 0 {
		t.Fatal("theme.FireSpectrum is empty; spectrum has no colours to draw from")
	}

	// A clearly hot bar (1.0) beside a clearly cool one (0.1).
	bars := []float64{1.0, 0.1}
	out := renderSpectrum(bars, 2)
	if out == "" {
		t.Fatal("renderSpectrum returned empty output")
	}

	// Coloured output: lipgloss wraps each block in an ANSI SGR escape.
	if !strings.Contains(out, "\x1b[") {
		t.Error("spectrum output carries no ANSI colour codes; bars are not coloured")
	}

	// Block runes survive styling.
	stripped := stripStyles(out)
	if !strings.ContainsAny(stripped, "▁▂▃▄▅▆▇█") {
		t.Errorf("spectrum output has no block runes after stripping styles, got %q", stripped)
	}

	// The hottest bar maps to the top (yellow) end of the theme ramp. Derive the
	// expected truecolor SGR from the ramp itself (the source of truth) and assert
	// it appears in the output; assert the dark crimson end does not, proving the
	// height → ramp-index mapping spreads colour rather than flattening it.
	sgr := func(c color.Color) string {
		r, g, b, _ := c.RGBA()
		return fmt.Sprintf("38;2;%d;%d;%d", r>>8, g>>8, b>>8)
	}
	hot := theme.FireSpectrum[len(theme.FireSpectrum)-1]
	cool := theme.FireSpectrum[0]
	if !strings.Contains(out, sgr(hot)) {
		t.Errorf("hottest bar not styled with the ramp's hot (yellow) end %q; output=%q", sgr(hot), out)
	}
	if strings.Contains(out, sgr(cool)) {
		t.Errorf("cool crimson end %q appeared for a hot+mid bar set; colour-index mapping is wrong", sgr(cool))
	}
}

// TestSpectrumSpringsInterpolate verifies the spectrum springs ease toward a new
// target over multiple ticks rather than snapping in a single step. The producer
// (RenderProgress) sets the target BarHeights; only the tick advances the spring
// positions, so after one tick the positions are between the start (zero) and the
// target, and they keep approaching it on later ticks.
func TestSpectrumSpringsInterpolate(t *testing.T) {
	m := NewModel(true)

	// Producer sets the target only; it must not move the spring positions.
	target := make([]float64, config.NumBars)
	for i := range target {
		target[i] = 1.0
	}
	m.Update(RenderProgress{Frame: 1, TotalFrames: 100, BarHeights: target})

	if got := m.spectrumPos[0]; got != 0 {
		t.Fatalf("RenderProgress advanced a spring (pos=%v); the producer must only set the target", got)
	}

	// One tick: positions move off zero but stay short of the target (no snap).
	m.Update(tickMsg{})
	afterOne := m.spectrumPos[0]
	if afterOne <= 0 {
		t.Fatalf("spring did not move toward target after one tick, pos=%v", afterOne)
	}
	if afterOne >= target[0] {
		t.Fatalf("spring snapped to/past target in one tick, pos=%v want < %v", afterOne, target[0])
	}

	// Further ticks keep approaching the target monotonically over this range.
	m.Update(tickMsg{})
	afterTwo := m.spectrumPos[0]
	if afterTwo <= afterOne {
		t.Fatalf("spring did not continue toward target: tick1=%v tick2=%v", afterOne, afterTwo)
	}
	if afterTwo >= target[0] {
		t.Fatalf("spring overshot target by tick two, pos=%v want < %v", afterTwo, target[0])
	}
}

// TestRenderSpectrumNormalisesToBars verifies the spectrum normalises to the
// loudest current bar so a mid-level bar still fills the chart. The 50-unit bar
// is half the 100-unit max, which renders as a full bottom-row block.
func TestRenderSpectrumNormalisesToBars(t *testing.T) {
	bars := []float64{100, 50}
	out := renderSpectrum(bars, 2)

	rows := strings.Split(out, "\n")
	if len(rows) != 2 {
		t.Fatalf("expected 2 spectrum rows, got %d", len(rows))
	}

	// Bottom row carries the bars. Normalised to the 100-unit max bar, the
	// 50-unit bar is half height, which renders as a full block in the bottom row.
	bottom := stripStyles(rows[1])
	if !strings.ContainsRune(bottom, '█') {
		t.Errorf("bottom row not at full height; normalisation is wrong: %q", bottom)
	}
}
