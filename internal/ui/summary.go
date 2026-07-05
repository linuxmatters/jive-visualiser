package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/linuxmatters/jive-visualiser/internal/config"
	"github.com/linuxmatters/jive-visualiser/internal/theme"
)

func (m *Model) renderComplete() string {
	var s strings.Builder

	s.WriteString(renderCompleteTitle())
	s.WriteString("\n\n")

	styles := completionSummaryStyles{
		dimLabel:            lipgloss.NewStyle().Faint(true),
		header:              lipgloss.NewStyle().Bold(true).Foreground(theme.FireOrange),
		label:               lipgloss.NewStyle().Faint(true),
		value:               lipgloss.NewStyle(),
		highlightValueStyle: lipgloss.NewStyle().Foreground(theme.FireOrange),
	}

	writeCompletionOverview(&s, *m.complete, styles.dimLabel)
	writeAudioAnalysisSummary(&s, m.audioProfile, styles)
	writeRenderPerformanceSummary(&s, *m.complete, styles, m.summaryBar.ViewAs)

	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(theme.FireOrange).
		Padding(1, 1).
		Width(m.boxContentWidth()).
		Render(s.String()) + "\n"
}

type completionSummaryStyles struct {
	dimLabel            lipgloss.Style
	header              lipgloss.Style
	label               lipgloss.Style
	value               lipgloss.Style
	highlightValueStyle lipgloss.Style
}

func renderCompleteTitle() string {
	return lipgloss.NewStyle().
		Bold(true).
		Foreground(theme.FireYellow).
		Render("✓ Encoding Complete!")
}

func writeCompletionOverview(s *strings.Builder, complete RenderComplete, dimLabel lipgloss.Style) {
	// Size, source duration, total time taken and the encoder live in the
	// finished Pass 2 box above, so they are omitted here.
	fmt.Fprintf(s, "%s%s\n", dimLabel.Render("Output:   "), complete.OutputFile)

	videoDuration := time.Duration(complete.TotalFrames) * time.Second / config.FPS
	fmt.Fprintf(s, "%s%d frames, %.2f fps average\n",
		dimLabel.Render("Video:    "),
		complete.TotalFrames,
		float64(complete.TotalFrames)/videoDuration.Seconds())
	if complete.SamplesProcessed > 0 {
		fmt.Fprintf(s, "%s%d samples processed\n\n", dimLabel.Render("Audio:    "), complete.SamplesProcessed)
	} else {
		s.WriteString("\n")
	}
}

func writeAudioAnalysisSummary(s *strings.Builder, profile *AudioProfile, styles completionSummaryStyles) {
	s.WriteString(styles.header.Render("Pass 1: Audio Analysis"))
	s.WriteString("\n")

	if profile != nil {
		pass1 := summaryTable().StyleFunc(func(_, col int) lipgloss.Style {
			if col == 0 {
				return styles.label.PaddingLeft(2).PaddingRight(2)
			}
			return styles.value
		})
		pass1.Row("Peak Level:", fmt.Sprintf("%.1f ㏈", profile.PeakLevel))
		pass1.Row("RMS Level:", fmt.Sprintf("%.1f ㏈", profile.RMSLevel))
		pass1.Row("Dynamic Range:", fmt.Sprintf("%.1f ㏈", profile.DynamicRange))
		pass1.Row("Optimal Scale:", fmt.Sprintf("%.3f", profile.OptimalScale))
		pass1.Row("Analysis Time:", styles.highlightValueStyle.Render(formatDuration(profile.AnalysisTime)))
		s.WriteString(pass1.Render())
		s.WriteString("\n")
	}

	s.WriteString("\n")
}

func writeRenderPerformanceSummary(
	s *strings.Builder,
	complete RenderComplete,
	styles completionSummaryStyles,
	renderBar func(float64) string,
) {
	totalMs := complete.TotalTime.Milliseconds()
	if totalMs == 0 {
		totalMs = 1
	}

	s.WriteString(styles.header.Render("Pass 2: Rendering & Encoding"))
	s.WriteString("\n")

	pass2 := summaryTable().StyleFunc(func(_, col int) lipgloss.Style {
		switch col {
		case 0:
			return styles.label.PaddingLeft(2).PaddingRight(2)
		case 1, 2:
			return styles.value.PaddingRight(2)
		default:
			return styles.value
		}
	})

	barRow := func(label string, duration time.Duration) {
		pct := int(float64(duration.Milliseconds()) * 100 / float64(totalMs))
		pass2.Row(
			label,
			fmt.Sprintf("~%s", formatDuration(duration)),
			fmt.Sprintf("(~%d%%)", pct),
			renderBar(float64(duration.Milliseconds())/float64(totalMs)),
		)
	}

	if complete.ThumbnailTime > 0 {
		barRow("Thumbnail:", complete.ThumbnailTime)
	}

	barRow("Visualisation:", complete.VisTime)
	barRow("Video encoding:", complete.EncodeTime)

	if complete.AudioTime > 0 {
		barRow("Audio encoding:", complete.AudioTime)
	}

	accountedTime := complete.ThumbnailTime + complete.VisTime +
		complete.EncodeTime + complete.AudioTime
	otherTime := complete.TotalTime - accountedTime
	if otherTime > 0 {
		otherLabel := "Runtime:"
		if complete.EncoderIsHW {
			otherLabel = "GPU pipeline:"
		}
		barRow(otherLabel, otherTime)
	}

	pass2.Row("Total time:", styles.highlightValueStyle.Render(formatDuration(complete.TotalTime)), "", "")
	s.WriteString(pass2.Render())
}

// summaryTable builds a borderless lipgloss table used for the completion
// summary. Borders and column dividers are off so the table provides column
// alignment only (not chrome) and nests inside the rounded-border box without a
// double border. Cell styling (labels, values, gaps) is applied per-table via
// StyleFunc.
func summaryTable() *table.Table {
	return theme.BorderlessTable()
}
