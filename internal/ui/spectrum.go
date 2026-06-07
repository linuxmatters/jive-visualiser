package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/linuxmatters/jivefire/internal/theme"
)

// Spectrum spring tuning. The delta time matches the UI tick cadence so each
// tick advances the springs by exactly one repaint interval. Angular frequency
// and damping are chosen so bars chase a new target quickly without overshoot.
const (
	spectrumSpringFreq    = 8.0
	spectrumSpringDamping = 1.0
)

// spectrumSpringDelta is the spring time step, locked to the UI tick cadence so
// one tick equals one spring step.
var spectrumSpringDelta = uiTickInterval.Seconds()

// advanceSpectrumSprings steps every spectrum spring one tick toward the latest
// producer-owned target (m.renderState.BarHeights), storing the new positions
// and velocities back into the Model. Called only from the tickMsg case so the
// tick is the single owner of spring state. Bars without a corresponding target
// (BarHeights shorter than the spring count, or empty before the first
// RenderProgress) ease toward zero.
func (m *Model) advanceSpectrumSprings() {
	targets := m.renderState.BarHeights
	for i := range m.spectrumSprings {
		var target float64
		if i < len(targets) {
			target = targets[i]
		}
		m.spectrumPos[i], m.spectrumVel[i] = m.spectrumSprings[i].Update(
			m.spectrumPos[i], m.spectrumVel[i], target)
	}
}

// spectrumBlocks are the eighth-block runes used to fill a half-cell row.
var spectrumBlocks = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// rowKind selects which of the two spectrum rows a row builder is drawing.
type rowKind int

const (
	topRow rowKind = iota
	bottomRow
)

// renderSpectrum creates a fire-coloured ASCII visualisation of bar heights. It
// renders two rows tall so each bar shows finer height resolution. The function
// is pure over its arguments.
func renderSpectrum(barHeights []float64, width int) string {
	if len(barHeights) == 0 || width == 0 {
		return ""
	}

	stride := len(barHeights) / width
	if stride == 0 {
		stride = 1
	}

	// Normalise to the loudest current bar so the spectrum fills both rows each
	// frame (per-frame auto-scaling, as the original did).
	maxHeight := 0.0
	for _, h := range barHeights {
		if h > maxHeight {
			maxHeight = h
		}
	}
	if maxHeight == 0 {
		maxHeight = 1.0 // Avoid division by zero
	}

	// Collect normalised bar heights for the columns we'll display.
	displayHeights := make([]float64, 0, width)
	for i := 0; i < len(barHeights) && len(displayHeights) < width; i += stride {
		displayHeights = append(displayHeights, barHeights[i]/maxHeight)
	}

	var result strings.Builder
	result.WriteString(spectrumRow(topRow, displayHeights))
	result.WriteString("\n")
	result.WriteString(spectrumRow(bottomRow, displayHeights))
	return result.String()
}

// spectrumRow builds one of the two spectrum rows. Each column shows the bar
// portion that falls in this row. Kept separate from renderSpectrum to keep that
// function's cyclomatic complexity within the lint budget.
func spectrumRow(kind rowKind, displayHeights []float64) string {
	fireColors := theme.FireSpectrum
	var row strings.Builder
	for _, normalised := range displayHeights {
		glyph, colorIdx, drawn := rowGlyph(kind, normalised)
		if !drawn {
			row.WriteString(" ")
			continue
		}
		row.WriteString(lipgloss.NewStyle().
			Foreground(fireColors[colorIdx]). //nolint:gosec // bounds clamped in rowGlyph
			Render(string(glyph)))
	}
	return row.String()
}

// rowGlyph picks the rune and colour index for one column in one row. It returns
// drawn=false when the column is empty space.
func rowGlyph(kind rowKind, normalised float64) (glyph rune, colorIdx int, drawn bool) {
	switch kind {
	case topRow:
		if normalised <= 0.5 {
			return ' ', 0, false
		}
		topPortion := (normalised - 0.5) * 2.0 // 0.0 to 1.0
		return spectrumBlocks[blockClamp(topPortion)], colorClamp(normalised, len(theme.FireSpectrum)), true
	default: // bottomRow
		if normalised >= 0.5 {
			return spectrumBlocks[len(spectrumBlocks)-1], colorClamp(normalised, len(theme.FireSpectrum)), true
		}
		return spectrumBlocks[blockClamp(normalised*2.0)], colorClamp(normalised, len(theme.FireSpectrum)), true
	}
}

// blockClamp maps a 0.0-1.0 portion to a valid spectrumBlocks index.
func blockClamp(portion float64) int {
	idx := int(portion * float64(len(spectrumBlocks)-1))
	if idx >= len(spectrumBlocks) {
		idx = len(spectrumBlocks) - 1
	}
	if idx < 0 {
		idx = 0
	}
	return idx
}

// colorClamp maps a 0.0-1.0 normalised height to a valid fire-ramp index.
func colorClamp(normalised float64, ramp int) int {
	idx := int(normalised * float64(ramp-1))
	if idx >= ramp {
		idx = ramp - 1
	}
	if idx < 0 {
		idx = 0
	}
	return idx
}
