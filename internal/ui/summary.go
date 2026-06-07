package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/linuxmatters/jivefire/internal/config"
	"github.com/linuxmatters/jivefire/internal/theme"
)

// Audio-meter display ranges. These map each metric onto its level-meter fill
// and are VISUAL INDICATORS ONLY, not calibrated scales: the bounds are picked
// so typical podcast values land partway along the bar rather than pinned at 0%
// or 100%. They do not represent the metric's true physical range.
const (
	meterPeakMinDB  = -40.0 // dB: quiet peak floor → empty
	meterPeakMaxDB  = 0.0   // dB: full-scale peak → full
	meterRMSMinDB   = -50.0 // dB: very quiet RMS floor → empty
	meterRMSMaxDB   = -6.0  // dB: loud RMS ceiling → full
	meterRangeMinDB = 0.0   // dB: no dynamic range → empty
	meterRangeMaxDB = 70.0  // dB: wide dynamic range → full
	meterScaleMin   = 0.0   // scale factor floor → empty
	meterScaleMax   = 2.0   // scale factor ceiling → full

	// Meter geometry shared across the two audio rows so their bars and values
	// line up. Two metrics per row at this width fit the 74-cell content area.
	meterBarWidth   = 10
	meterLabelWidth = 5
	// meterLeftValueWidth pads the left-column metric value (Peak/RMS) so the
	// right-column meter starts at a fixed x on both rows. Wide enough for
	// "-XX.X" dB readings.
	meterLeftValueWidth = 6
)

func (m *Model) renderAudioProfile(s *strings.Builder) {
	if m.audioProfile == nil {
		// Placeholder during Pass 1: header, divider, then the italic notice.
		headerStyle := lipgloss.NewStyle().Faint(true).Bold(true)
		placeholderStyle := lipgloss.NewStyle().Faint(true).Italic(true)
		s.WriteString(lipgloss.JoinHorizontal(lipgloss.Top,
			headerStyle.Render("Audio"),
			" │ ",
			placeholderStyle.Render("Analysing..."),
		))
		return
	}

	// Left-column values are padded to a fixed width so the right-column meters
	// (Range, Scale) start at the same x on both rows.
	leftValue := lipgloss.NewStyle().Width(meterLeftValueWidth)
	valueStyle := lipgloss.NewStyle()
	frac := func(v, lo, hi float64) float64 {
		if hi <= lo {
			return 0
		}
		return (v - lo) / (hi - lo)
	}

	peak := meter("Peak",
		frac(m.audioProfile.PeakLevel, meterPeakMinDB, meterPeakMaxDB),
		meterBarWidth, meterLabelWidth,
		leftValue.Render(fmt.Sprintf("%.1f", m.audioProfile.PeakLevel)))
	rangeM := meter("Range",
		frac(m.audioProfile.DynamicRange, meterRangeMinDB, meterRangeMaxDB),
		meterBarWidth, meterLabelWidth,
		valueStyle.Render(fmt.Sprintf("%.1f dB", m.audioProfile.DynamicRange)))
	rms := meter("RMS",
		frac(m.audioProfile.RMSLevel, meterRMSMinDB, meterRMSMaxDB),
		meterBarWidth, meterLabelWidth,
		leftValue.Render(fmt.Sprintf("%.1f", m.audioProfile.RMSLevel)))
	scale := meter("Scale",
		frac(m.audioProfile.OptimalScale, meterScaleMin, meterScaleMax),
		meterBarWidth, meterLabelWidth,
		valueStyle.Render(fmt.Sprintf("%.3f", m.audioProfile.OptimalScale)))

	// Two metrics per row: Peak | Range, then RMS | Scale. A fixed gap between
	// the columns keeps both rows aligned regardless of value width.
	gap := "   "
	s.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, peak, gap, rangeM))
	s.WriteString("\n")
	s.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, rms, gap, scale))
}

func (m *Model) renderComplete() string {
	var s strings.Builder

	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(theme.FireYellow).
		Render("✓ Encoding Complete!")

	s.WriteString(title)
	s.WriteString("\n\n")

	// Styles for output summary
	dimLabel := lipgloss.NewStyle().Faint(true)

	// Output summary
	fmt.Fprintf(&s, "%s%s\n", dimLabel.Render("Output:   "), m.complete.OutputFile)
	if m.complete.EncoderName != "" {
		fmt.Fprintf(&s, "%s%s\n", dimLabel.Render("Encoder:  "), m.complete.EncoderName)
	}

	videoDuration := time.Duration(m.complete.TotalFrames) * time.Second / config.FPS
	fmt.Fprintf(&s, "%s%d frames, %.2f fps average\n",
		dimLabel.Render("Video:    "),
		m.complete.TotalFrames,
		float64(m.complete.TotalFrames)/videoDuration.Seconds())
	if m.complete.SamplesProcessed > 0 {
		fmt.Fprintf(&s, "%s%d samples processed\n", dimLabel.Render("Audio:    "), m.complete.SamplesProcessed)
	}
	fmt.Fprintf(&s, "%s%.1fs video in %.1fs\n",
		dimLabel.Render("Duration: "),
		videoDuration.Seconds(),
		m.complete.TotalTime.Seconds())
	fmt.Fprintf(&s, "%s%s\n\n", dimLabel.Render("Size:     "), formatBytes(m.complete.FileSize))

	// Audio Profile section
	headerStyle := lipgloss.NewStyle().Bold(true).Foreground(theme.FireOrange)
	labelStyle := lipgloss.NewStyle().Faint(true)
	valueStyle := lipgloss.NewStyle()
	highlightValueStyle := lipgloss.NewStyle().Foreground(theme.FireOrange)

	totalMs := m.complete.TotalTime.Milliseconds()
	if totalMs == 0 {
		totalMs = 1
	}

	s.WriteString(headerStyle.Render("Pass 1: Audio Analysis"))
	s.WriteString("\n")

	// Pass 1 table: a borderless two-column label/value grid. The table handles
	// column alignment in place of the old %-18s manual padding; it renders
	// borderless so it nests inside the rounded-border box without double chrome.
	if m.audioProfile != nil {
		pass1 := summaryTable().StyleFunc(func(_, col int) lipgloss.Style {
			if col == 0 {
				return labelStyle.PaddingLeft(2).PaddingRight(2)
			}
			return valueStyle
		})
		pass1.Row("Duration:", fmt.Sprintf("%.1fs", m.audioProfile.Duration.Seconds()))
		pass1.Row("Peak Level:", fmt.Sprintf("%.1f dB", m.audioProfile.PeakLevel))
		pass1.Row("RMS Level:", fmt.Sprintf("%.1f dB", m.audioProfile.RMSLevel))
		pass1.Row("Dynamic Range:", fmt.Sprintf("%.1f dB", m.audioProfile.DynamicRange))
		pass1.Row("Optimal Scale:", fmt.Sprintf("%.3f", m.audioProfile.OptimalScale))
		pass1.Row("Analysis Time:", highlightValueStyle.Render(formatDuration(m.audioProfile.AnalysisTime)))
		s.WriteString(pass1.Render())
		s.WriteString("\n")
	}

	s.WriteString("\n")

	// Pass 2 Performance Breakdown
	s.WriteString(headerStyle.Render("Pass 2: Rendering & Encoding"))
	s.WriteString("\n")

	// Pass 2 table: label, duration, percentage and a rendered summary bar. The
	// bar is pre-rendered into a cell value (summaryBar.ViewAs renders once at
	// completion, which the proposal permits). The table aligns the four columns
	// in place of the old %-18s/%-6s manual padding.
	pass2 := summaryTable().StyleFunc(func(_, col int) lipgloss.Style {
		switch col {
		case 0:
			return labelStyle.PaddingLeft(2).PaddingRight(2)
		case 1, 2:
			return valueStyle.PaddingRight(2)
		default:
			return valueStyle
		}
	})

	barRow := func(label string, duration time.Duration) {
		pct := int(float64(duration.Milliseconds()) * 100 / float64(totalMs))
		pass2.Row(
			label,
			fmt.Sprintf("~%s", formatDuration(duration)),
			fmt.Sprintf("(~%d%%)", pct),
			m.summaryBar.ViewAs(float64(duration.Milliseconds())/float64(totalMs)),
		)
	}

	if m.complete.ThumbnailTime > 0 {
		barRow("Thumbnail:", m.complete.ThumbnailTime)
	}

	barRow("Visualisation:", m.complete.VisTime)
	barRow("Video encoding:", m.complete.EncodeTime)

	if m.complete.AudioTime > 0 {
		barRow("Audio encoding:", m.complete.AudioTime)
	}

	// Calculate unaccounted time including finalisation (Pass 2 only)
	// Roll finalisation into runtime/pipeline since it's typically small
	accountedTime := m.complete.ThumbnailTime + m.complete.VisTime +
		m.complete.EncodeTime + m.complete.AudioTime
	otherTime := m.complete.TotalTime - accountedTime
	if otherTime > 0 {
		// Label based on encoder type: hardware encoders have GPU pipeline overhead,
		// software encoder has Go runtime/GC overhead
		otherLabel := "Runtime:"
		if m.complete.EncoderIsHW {
			otherLabel = "GPU pipeline:"
		}
		barRow(otherLabel, otherTime)
	}

	// Total time gets its own label/value row with the highlight style applied.
	pass2.Row("Total time:", highlightValueStyle.Render(formatDuration(m.complete.TotalTime)), "", "")
	s.WriteString(pass2.Render())

	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(theme.FireOrange).
		Padding(1, 1).
		Width(m.boxContentWidth()).
		Render(s.String()) + "\n"
}

// summaryTable builds a borderless lipgloss table used for the completion
// summary. Borders and column dividers are off so the table provides column
// alignment only (not chrome) and nests inside the rounded-border box without a
// double border. Cell styling (labels, values, gaps) is applied per-table via
// StyleFunc.
func summaryTable() *table.Table {
	return table.New().
		Border(lipgloss.HiddenBorder()).
		BorderTop(false).
		BorderBottom(false).
		BorderLeft(false).
		BorderRight(false).
		BorderHeader(false).
		BorderColumn(false).
		BorderRow(false)
}
