package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/linuxmatters/jivefire/internal/theme"
)

// speedHistoryCap bounds the realtime-speed trace shown in the Speed card's
// sparkline. The window holds the most recent samples; older ones drop off.
const speedHistoryCap = 12

// sparklineBlocks are the eighth-block runes used to draw a single-row
// sparkline, ascending from shortest to tallest. Shared with the spectrum's
// block ramp shape but kept local so the sparkline owns its own ramp.
var sparklineBlocks = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// sparkline renders a one-row block-rune trace of the samples, scaled to the
// window's own min/max so a flat or near-flat series still reads cleanly. An
// empty series renders nothing; a flat series renders a uniform mid-level row so
// the gauge never collapses to blanks or panics. The function is pure over its
// arguments.
func sparkline(samples []float64) string {
	if len(samples) == 0 {
		return ""
	}

	minV, maxV := samples[0], samples[0]
	for _, v := range samples {
		if v < minV {
			minV = v
		}
		if v > maxV {
			maxV = v
		}
	}

	span := maxV - minV
	var b strings.Builder
	for _, v := range samples {
		// A flat window (span 0) maps every sample to the mid block so the trace
		// stays visible rather than collapsing to the lowest rune.
		var norm float64
		if span > 0 {
			norm = (v - minV) / span
		} else {
			norm = 0.5
		}
		b.WriteRune(sparklineBlocks[blockClamp(norm)])
	}
	return b.String()
}

// gaugeCard builds a small RoundedBorder card with an icon+label on the top
// border and a value line beneath. The card is sized to innerWidth content
// cells (the value line is padded/truncated to that width) so several cards join
// cleanly with lipgloss.JoinHorizontal. The border uses the fire theme colour.
func gaugeCard(icon, label, value string, innerWidth int) string {
	if innerWidth < 1 {
		innerWidth = 1
	}

	header := strings.TrimSpace(icon + " " + label)

	body := lipgloss.NewStyle().
		Width(innerWidth).
		Foreground(theme.FireYellow).
		Bold(true).
		Render(truncateCells(value, innerWidth))

	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(theme.FireOrange).
		Padding(0, 1).
		Render(headerLine(header, innerWidth) + "\n" + body)
}

// headerLine renders the card header (icon + label) clamped to the inner width
// so a long label never widens the card past its joined neighbours.
func headerLine(header string, innerWidth int) string {
	return lipgloss.NewStyle().
		Width(innerWidth).
		Foreground(theme.WarmGray).
		Render(truncateCells(header, innerWidth))
}

// meter renders a horizontal level meter of the form
// "label ▕<filled><track>▏ value" where the filled portion is coloured with the
// fire gradient on a dim track. fraction is clamped to [0,1]; barWidth is the
// number of cells in the bar between the ▕ ▏ caps. The label is left-padded to
// labelWidth so stacked meters align their bars.
func meter(label string, fraction float64, barWidth, labelWidth int, value string) string {
	if fraction < 0 {
		fraction = 0
	}
	if fraction > 1 {
		fraction = 1
	}
	if barWidth < 1 {
		barWidth = 1
	}

	filled := min(int(fraction*float64(barWidth)), barWidth)

	labelStyle := lipgloss.NewStyle().Foreground(theme.WarmGray).Width(labelWidth)
	capStyle := lipgloss.NewStyle().Foreground(theme.WarmGray)
	trackStyle := lipgloss.NewStyle().Foreground(theme.WarmGray)

	var bar strings.Builder
	ramp := theme.FireSpectrum
	for i := 0; i < barWidth; i++ {
		if i < filled {
			// Colour each filled cell along the fire ramp by its position in the
			// bar, matching the spectrum's per-cell colouring approach.
			idx := colorClamp(float64(i)/float64(barWidth), len(ramp))
			bar.WriteString(lipgloss.NewStyle().
				Foreground(ramp[idx]). //nolint:gosec // index clamped by colorClamp
				Render("█"))
		} else {
			bar.WriteString(trackStyle.Render("░"))
		}
	}

	return lipgloss.JoinHorizontal(lipgloss.Top,
		labelStyle.Render(label),
		" ",
		capStyle.Render("▕"),
		bar.String(),
		capStyle.Render("▏"),
		" ",
		value,
	)
}

// truncateCells trims s to at most width terminal cells. It strips no styles, so
// callers should pass plain text. Used to keep card values within their box.
func truncateCells(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	var b strings.Builder
	count := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if count+rw > width {
			break
		}
		b.WriteRune(r)
		count += rw
	}
	return b.String()
}
