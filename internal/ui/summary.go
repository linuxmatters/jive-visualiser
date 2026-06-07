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

func (m *Model) renderAudioProfile(s *strings.Builder) {
	labelStyle := lipgloss.NewStyle().Faint(true)
	valueStyle := lipgloss.NewStyle()
	headerStyle := lipgloss.NewStyle().Faint(true).Bold(true)

	header := headerStyle.Render("Audio")

	if m.audioProfile == nil {
		// Placeholder during Pass 1: header, divider, then the italic notice.
		placeholderStyle := lipgloss.NewStyle().Faint(true).Italic(true)
		s.WriteString(lipgloss.JoinHorizontal(lipgloss.Top,
			header,
			" │ ",
			placeholderStyle.Render("Analysing..."),
		))
		return
	}

	// Populated row: each metric is a label/value cell joined with consistent
	// two-space gaps. JoinHorizontal aligns the cells so the divider and metrics
	// line up regardless of value width. Same labels, units and formatting as the
	// previous manual WriteString row (Peak/RMS/Range in dB, Scale to 3 dp).
	cell := func(label, value string) string {
		return lipgloss.JoinHorizontal(lipgloss.Top,
			labelStyle.Render(label),
			" ",
			valueStyle.Render(value),
		)
	}

	gap := "  "
	s.WriteString(lipgloss.JoinHorizontal(lipgloss.Top,
		header,
		" │ ",
		valueStyle.Render(fmt.Sprintf("%.1fs", m.audioProfile.Duration.Seconds())),
		gap,
		cell("Peak:", fmt.Sprintf("%.1f dB", m.audioProfile.PeakLevel)),
		gap,
		cell("RMS:", fmt.Sprintf("%.1f dB", m.audioProfile.RMSLevel)),
		gap,
		cell("Range:", fmt.Sprintf("%.1f dB", m.audioProfile.DynamicRange)),
		gap,
		cell("Scale:", fmt.Sprintf("%.3f", m.audioProfile.OptimalScale)),
	))
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
