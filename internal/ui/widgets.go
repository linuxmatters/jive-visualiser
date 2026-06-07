package ui

import (
	"image/color"
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
		b.WriteRune(sparklineBlocks[blockClamp(norm, len(sparklineBlocks))])
	}
	return b.String()
}

// gaugeCard builds a small RoundedBorder card with an icon+label on the top
// border and a value line beneath. The card is sized to innerWidth content
// cells (the value line is padded/truncated to that width) so several cards join
// cleanly with lipgloss.JoinHorizontal. The border uses the fire theme colour.
// iconColour styles just the glyph; the label keeps the WarmGray header colour.
func gaugeCard(icon string, iconColour color.Color, label, value string, innerWidth int) string {
	if innerWidth < 1 {
		innerWidth = 1
	}

	body := lipgloss.NewStyle().
		Width(innerWidth).
		Foreground(theme.FireYellow).
		Bold(true).
		Render(truncateCells(value, innerWidth))

	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(theme.FireOrange).
		Padding(0, 1).
		Render(headerLine(icon, iconColour, label, innerWidth) + "\n" + body)
}

// headerLine renders the card header (icon + label) clamped to the inner width
// so a long label never widens the card past its joined neighbours. The icon is
// coloured with iconColour while the label uses the WarmGray header colour; the
// plain "icon label" text drives the width clamp so styling never miscounts
// cells.
func headerLine(icon string, iconColour color.Color, label string, innerWidth int) string {
	plain := strings.TrimSpace(icon + " " + label)
	if lipgloss.Width(plain) > innerWidth {
		// A long label is truncated as before, with no per-glyph colouring so the
		// clamp stays simple and the card never widens past its neighbours.
		return lipgloss.NewStyle().
			Width(innerWidth).
			Foreground(theme.WarmGray).
			Render(truncateCells(plain, innerWidth))
	}

	styledIcon := lipgloss.NewStyle().Foreground(iconColour).Render(icon)
	styledLabel := lipgloss.NewStyle().Foreground(theme.WarmGray).Render(label)
	header := lipgloss.JoinHorizontal(lipgloss.Top, styledIcon, " ", styledLabel)
	return lipgloss.NewStyle().Width(innerWidth).Render(header)
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
