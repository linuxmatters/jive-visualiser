package ui

import (
	"fmt"
	"image"
	"math"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/harmonica"
	"github.com/linuxmatters/jivefire/internal/config"
	"github.com/linuxmatters/jivefire/internal/theme"
)

// Phase represents the current processing phase
type Phase int

const (
	PhaseAnalysis Phase = iota
	PhaseRendering
	PhaseComplete
)

// AnalysisProgress represents progress updates from Pass 1 audio analysis
type AnalysisProgress struct {
	Frame       int
	TotalFrames int
	CurrentRMS  float64
	CurrentPeak float64
	BarHeights  []float64
	Duration    time.Duration
}

// AnalysisComplete signals completion of Pass 1 with audio profile data
type AnalysisComplete struct {
	PeakMagnitude float64
	RMSLevel      float64
	DynamicRange  float64 // raw peak/RMS ratio; converted to dB at assignment
	Duration      time.Duration
	OptimalScale  float64
	AnalysisTime  time.Duration
}

// RenderProgress represents progress updates from Pass 2 video rendering
type RenderProgress struct {
	Frame       int
	TotalFrames int
	Elapsed     time.Duration
	BarHeights  []float64
	FileSize    int64
	Sensitivity float64
	FrameData   *image.RGBA
	VideoCodec  string
	AudioCodec  string
}

// RenderComplete signals completion of Pass 2
type RenderComplete struct {
	OutputFile       string
	Duration         time.Duration
	FileSize         int64
	TotalFrames      int
	VisTime          time.Duration // Visualisation: FFT + binning + drawing
	EncodeTime       time.Duration // Video encoding time
	AudioTime        time.Duration // Audio reading + encoding time
	FinalizeTime     time.Duration // Encoder finalization (flush + close)
	TotalTime        time.Duration
	ThumbnailTime    time.Duration
	SamplesProcessed int64
	EncoderName      string // Video encoder used (e.g., "h264_nvenc", "libx264")
	EncoderIsHW      bool   // Whether the encoder was hardware-backed
}

// AudioProfile holds the audio analysis results for display
type AudioProfile struct {
	Duration     time.Duration
	PeakLevel    float64 // in dB
	RMSLevel     float64 // in dB
	DynamicRange float64 // in dB (converted from the raw peak/RMS ratio)
	OptimalScale float64
	AnalysisTime time.Duration
}

// progressQuitMsg is sent when it's time to quit after showing completion
type progressQuitMsg struct{}

// tickMsg drives the UI repaint clock. It fires on a fixed cadence independent
// of the p.Send data rate so animation and timers advance smoothly between data
// updates. This tick owns the animation clock; data producers own target state.
type tickMsg struct{}

// uiTickInterval is the fixed UI repaint cadence (~60ms ≈ 16fps), decoupled from
// the data-update rate.
const uiTickInterval = 60 * time.Millisecond

// tickCmd schedules the next UI tick.
func tickCmd() tea.Cmd {
	return tea.Tick(uiTickInterval, func(time.Time) tea.Msg {
		return tickMsg{}
	})
}

// keyMap holds the key bindings for the progress UI. It implements the
// help.KeyMap interface so the help component can render the footer affordance.
type keyMap struct {
	Quit key.Binding
}

// ShortHelp returns the bindings shown in the single-line help footer.
func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Quit}
}

// FullHelp returns the bindings grouped into columns for the expanded help view.
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Quit}}
}

// keys defines the active key bindings for the progress UI.
var keys = keyMap{
	Quit: key.NewBinding(
		key.WithKeys("q", "ctrl+c"),
		key.WithHelp("q", "quit"),
	),
}

// Model implements the unified Bubbletea model for both passes
type Model struct {
	progressBar progress.Model
	summaryBar  progress.Model
	help        help.Model
	spinner     spinner.Model
	phase       Phase

	// Audio profile (populated during/after Pass 1)
	audioProfile *AudioProfile

	// Pass 1 state
	analysisProgress AnalysisProgress

	// Pass 2 state
	renderState RenderProgress
	complete    *RenderComplete

	// Spectrum smoothing: one spring per displayed bar with parallel position
	// and velocity slices. The tick is the sole owner that advances these toward
	// the producer-owned target m.renderState.BarHeights; renderSpectrum reads
	// the positions but never allocates or steps the springs.
	spectrumSprings []harmonica.Spring
	spectrumPos     []float64
	spectrumVel     []float64

	// speedHistory is a bounded trace of recent realtime-speed samples, appended
	// once per RenderProgress update and drawn as the Speed card's sparkline.
	// Capped at speedHistoryCap; the oldest sample drops off the front.
	speedHistory []float64

	// Timing
	overallStartTime time.Time
	pass1StartTime   time.Time
	pass2StartTime   time.Time
	completionTime   time.Time

	// UI state
	width           int
	height          int
	noPreview       bool
	cachedPreview   string
	cachedFrameNum  int
	completionDelay time.Duration
	quitting        bool
}

// boxDesignWidth is the fixed shared outer width for every bordered box. It is
// derived from the video preview: the preview content is
// DefaultPreviewConfig().Width (72) cells, plus its own 1-cell border on each
// side (74), and the live box adds a 1-cell border plus 2 columns of padding on
// each side (border 2 + padding 4 = 6). 74 + 6 = 80, so the box hugs the preview
// with no empty margin. Every box renders at this width so none visibly resizes
// between Pass 1, Pass 2 and the completion screen.
const boxDesignWidth = 80

// boxContentWidth returns the shared outer width applied to every bordered box.
// lipgloss treats .Width(n) on a bordered style as the box's overall width
// (border included), so applying this single value to all box styles yields
// equal outer widths regardless of their differing padding. The width is fixed
// at boxDesignWidth; a narrower terminal clamps it down. The preview is
// fixed-width and may overflow on very narrow terminals, the pre-existing
// behaviour.
func (m *Model) boxContentWidth() int {
	if m.width > 0 && m.width < boxDesignWidth {
		return m.width
	}
	return boxDesignWidth
}

// newProgressBar builds a fire-gradient progress bar of the given width with the
// percentage label suppressed. Shared by NewModel and the analysis→rendering
// transition so a fresh bar is constructed consistently.
func newProgressBar(width int) progress.Model {
	// Fire gradient: deep red → orange → yellow
	return progress.New(
		progress.WithColors(theme.FireCrimson, theme.FireYellow),
		progress.WithWidth(width),
		progress.WithoutPercentage(),
	)
}

// NewModel creates a new unified progress UI model
func NewModel(noPreview bool) *Model {
	// Main 40-wide bar for pass progress.
	p := newProgressBar(40)

	// Smaller progress bar for summary performance charts
	summaryBar := newProgressBar(30)

	// Help footer styled to the fire palette: the key in FireOrange, the
	// description in WarmGray.
	h := help.New()
	h.Styles.ShortKey = h.Styles.ShortKey.Foreground(theme.FireOrange)
	h.Styles.ShortDesc = h.Styles.ShortDesc.Foreground(theme.WarmGray)
	h.Styles.ShortSeparator = h.Styles.ShortSeparator.Foreground(theme.WarmGray)

	// Spinner shown only during dead-air phases (no progress frames yet), styled
	// to the fire palette.
	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(theme.FireOrange)

	// One spring per displayed bar (config.NumBars == 64). Springs share the same
	// coefficients; positions and velocities are per-bar. Initialised at rest at
	// zero so the first targets ease in rather than snapping.
	springs := make([]harmonica.Spring, config.NumBars)
	for i := range springs {
		springs[i] = harmonica.NewSpring(spectrumSpringDelta, spectrumSpringFreq, spectrumSpringDamping)
	}

	return &Model{
		progressBar:      p,
		summaryBar:       summaryBar,
		help:             h,
		spinner:          sp,
		phase:            PhaseAnalysis,
		overallStartTime: time.Now(),
		pass1StartTime:   time.Now(),
		completionDelay:  2 * time.Second,
		noPreview:        noPreview,
		spectrumSprings:  springs,
		spectrumPos:      make([]float64, config.NumBars),
		spectrumVel:      make([]float64, config.NumBars),
	}
}

// Init initializes the model and starts both the self-perpetuating UI tick and
// the spinner's own tick. The two clocks stay distinct (tickMsg vs
// spinner.TickMsg); tea.Batch only combines them at startup.
func (m *Model) Init() tea.Cmd {
	return tea.Batch(tickCmd(), m.spinner.Tick)
}

// Update handles messages
func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.progressBar.SetWidth(min(msg.Width-30, 50))
		return m, nil

	case AnalysisProgress:
		m.analysisProgress = msg
		// Drive the bar's spring toward the new target. The producer owns the
		// target percent; the bar's own FrameMsg loop animates the fill. Only
		// applies when a total frame count is known; otherwise the no-total
		// fallback render branch handles display.
		if msg.TotalFrames > 0 {
			percent := float64(msg.Frame) / float64(msg.TotalFrames)
			return m, m.progressBar.SetPercent(percent)
		}
		return m, nil

	case AnalysisComplete:
		// Store audio profile for display
		m.audioProfile = &AudioProfile{
			Duration:     msg.Duration,
			PeakLevel:    20 * math.Log10(msg.PeakMagnitude),
			RMSLevel:     20 * math.Log10(msg.RMSLevel),
			DynamicRange: 20 * math.Log10(msg.DynamicRange),
			OptimalScale: msg.OptimalScale,
			AnalysisTime: msg.AnalysisTime,
		}
		// Transition to rendering phase. Recreate the progress bar from scratch so
		// Pass 2 starts from an empty fill: the shared bar still targets Pass 1's
		// ~100%, and SetPercent would animate it DOWN to the first small Pass 2
		// percent. An instant recreate resets the target to 0 with no drain, while
		// keeping the animated View() fill for Pass 2. Width was set on
		// WindowSizeMsg, so preserve it across the recreate.
		m.progressBar = newProgressBar(m.progressBar.Width())
		m.phase = PhaseRendering
		m.pass2StartTime = time.Now()
		return m, nil

	case RenderProgress:
		m.renderState = msg
		m.recordSpeedSample(msg)
		// Drive the bar's spring toward the new target. The producer owns the
		// target percent; the bar's own FrameMsg loop animates the fill.
		if msg.TotalFrames > 0 {
			percent := float64(msg.Frame) / float64(msg.TotalFrames)
			return m, m.progressBar.SetPercent(percent)
		}
		return m, nil

	case RenderComplete:
		m.complete = &msg
		m.phase = PhaseComplete
		m.completionTime = time.Now()
		m.quitting = true

		return m, tea.Tick(m.completionDelay, func(t time.Time) tea.Msg {
			return progressQuitMsg{}
		})

	case tickMsg:
		// Advance the spectrum springs one step toward the producer-owned target,
		// then re-issue the tick to keep the repaint clock running. The tick is the
		// SOLE owner that steps the springs; RenderProgress only updates the target
		// (m.renderState.BarHeights). This avoids a tick-vs-p.Send double-update
		// race. This clock is distinct from spinner.TickMsg; never re-issue one
		// from the other.
		m.advanceSpectrumSprings()
		return m, tickCmd()

	case spinner.TickMsg:
		// Advance the spinner's own animation clock. Kept separate from the
		// tickMsg repaint clock; the spinner re-issues its own tick.
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		return m, cmd

	case progress.FrameMsg:
		// Advance the progress bar's spring animation. SetPercent (in the
		// progress cases above) seeds the spring; this FrameMsg loop animates
		// the fill toward the target between data updates.
		var cmd tea.Cmd
		m.progressBar, cmd = m.progressBar.Update(msg)
		return m, cmd

	case progressQuitMsg:
		return m, tea.Quit

	case tea.KeyPressMsg:
		// Any key dismisses the completed view; otherwise only the quit binding
		// (q / ctrl+c) exits during processing.
		if m.complete != nil {
			return m, tea.Quit
		}
		if key.Matches(msg, keys.Quit) {
			return m, tea.Quit
		}
	}

	return m, nil
}

// View renders the UI
func (m *Model) View() tea.View {
	var content string
	if m.phase == PhaseComplete {
		content = m.renderFinalProgress() + "\n" + m.renderComplete()
	} else {
		content = m.renderProgress()
	}

	// Alternate screen buffer prevents ghost box edges when the view height
	// changes between passes (replaces v1 tea.WithAltScreen()).
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

// CompletionSummary returns the final completion summary for printing after alt screen exits.
// Returns empty string if encoding is not complete.
func (m *Model) CompletionSummary() string {
	if m.complete == nil {
		return ""
	}
	return m.renderFinalProgress() + "\n" + m.renderComplete()
}

// renderFinalProgress renders the progress UI in its final completed state
func (m *Model) renderFinalProgress() string {
	var s strings.Builder

	// Title
	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(theme.FireYellow).
		Render("Jivefire 🔥")

	s.WriteString(title)
	s.WriteString("\n")
	s.WriteString(lipgloss.NewStyle().Foreground(theme.FireOrange).Render("Pass 2: Rendering & Encoding"))
	s.WriteString("\n\n")

	// Progress bar at 100%
	progressBar := m.progressBar.ViewAs(1.0)
	s.WriteString(progressBar)
	s.WriteString("  100%")
	s.WriteString("\n\n")

	// Final timing - calculate and display final speed
	videoDuration := time.Duration(m.complete.TotalFrames) * time.Second / config.FPS
	var finalSpeed float64
	if m.complete.TotalTime > 0 {
		finalSpeed = float64(videoDuration) / float64(m.complete.TotalTime)
	}
	s.WriteString(lipgloss.NewStyle().Faint(true).Render(
		fmt.Sprintf("Time: %s  │  Speed: %.1fx realtime  │  Complete", formatDuration(m.complete.TotalTime), finalSpeed)))
	s.WriteString("\n")

	// Final spectrum - zeroed out to show clean state. Derive the width from the
	// shared box width (outer minus the 1-cell border and 2 columns of padding on
	// each side) so the spectrum fills the box consistently and never exceeds the
	// content area, which would wrap.
	spectrumWidth := max(m.boxContentWidth()-6, 10)
	s.WriteString("\n\n")
	s.WriteString(lipgloss.NewStyle().Faint(true).Render("Live Visualisation:"))
	s.WriteString("\n")
	// Create zeroed bar heights for clean display
	zeroedBars := make([]float64, 64)
	spectrum := renderSpectrum(zeroedBars, spectrumWidth)
	s.WriteString(spectrum)

	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(theme.FireOrange).
		Padding(1, 2).
		Width(m.boxContentWidth()).
		Render(s.String())
}

func (m *Model) renderProgress() string {
	var s strings.Builder

	// Title
	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(theme.FireYellow).
		Render("Jivefire 🔥")

	s.WriteString(title)
	s.WriteString("\n")

	// Phase indicator
	var phaseLabel string
	if m.phase == PhaseAnalysis {
		phaseLabel = "Pass 1: Analysing Audio"
	} else {
		phaseLabel = "Pass 2: Rendering & Encoding"
	}
	s.WriteString(lipgloss.NewStyle().Foreground(theme.FireOrange).Render(phaseLabel))
	s.WriteString("\n\n")

	// Progress bar and timing
	if m.phase == PhaseAnalysis {
		m.renderAnalysisProgress(&s)
	} else {
		m.renderRenderingProgress(&s)
	}

	// Spectrum (Pass 2 only)
	if m.phase == PhaseRendering && len(m.renderState.BarHeights) > 0 {
		s.WriteString("\n")
		m.renderSpectrumAndStats(&s)
	}

	// Help footer — a single, always-present line so the box height is stable
	// across the Pass 1 → Pass 2 transition (no footer-driven jitter). Styled to
	// match the fire palette; rendered inside the bordered box so it stays within
	// the alt screen. Omitted from the post-exit completion summary.
	s.WriteString("\n\n")
	s.WriteString(m.renderHelpFooter())

	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(theme.FireRed).
		Padding(1, 2).
		Width(m.boxContentWidth()).
		Render(s.String())
}

// renderHelpFooter renders the single-line quit affordance for the live
// in-progress UI. It is intentionally excluded from the completion summary.
func (m *Model) renderHelpFooter() string {
	return m.help.View(keys)
}

func (m *Model) renderAnalysisProgress(s *strings.Builder) {
	switch {
	case m.analysisProgress.TotalFrames > 0:
		// We have frame count, show progress bar
		percent := float64(m.analysisProgress.Frame) / float64(m.analysisProgress.TotalFrames)
		progressBar := m.progressBar.View()

		s.WriteString(progressBar)
		fmt.Fprintf(s, "  %d%%", int(percent*100))
	case m.analysisProgress.Frame > 0:
		// No total, show frame count with elapsed time. Spinner signals live work.
		s.WriteString(m.spinner.View())
		s.WriteString(" ")
		s.WriteString(lipgloss.NewStyle().Faint(true).Render("Analysing..."))
		fmt.Fprintf(s, "  %d frames  │  Elapsed: %s",
			m.analysisProgress.Frame,
			formatDuration(m.analysisProgress.Duration))
	default:
		// Dead air before any frames arrive: spinner is the only motion.
		s.WriteString(m.spinner.View())
		s.WriteString(" ")
		s.WriteString(lipgloss.NewStyle().Faint(true).Render("Starting analysis..."))
	}
}

func (m *Model) renderRenderingProgress(s *strings.Builder) {
	if m.renderState.TotalFrames == 0 {
		// Dead air before the first render frame: spinner is the only motion.
		s.WriteString(m.spinner.View())
		s.WriteString(" ")
		s.WriteString(lipgloss.NewStyle().Faint(true).Render("Starting render..."))
		return
	}

	// Progress is based on video frames (audio is encoded alongside each frame)
	percent := float64(m.renderState.Frame) / float64(m.renderState.TotalFrames)

	progressBar := m.progressBar.View()
	s.WriteString(progressBar)
	fmt.Fprintf(s, "  %d%%", int(percent*100))
	s.WriteString("\n\n")

	// Timing information. Derive elapsed from wall-clock at render time so the
	// ~60ms UI tick advances it (and the ETA/speed derived from it) between
	// p.Send data updates, rather than freezing on the stale message field.
	// Fall back to the message field only if pass2StartTime is unset.
	elapsed := time.Since(m.pass2StartTime)
	if m.pass2StartTime.IsZero() {
		elapsed = m.renderState.Elapsed
	}

	var estimatedTotal, eta time.Duration
	var speed float64

	if percent > 0 {
		estimatedTotal = time.Duration(float64(elapsed) / percent)
		eta = estimatedTotal - elapsed

		videoEncodedSoFar := time.Duration(m.renderState.Frame) * time.Second / config.FPS
		if elapsed > 0 {
			speed = float64(videoEncodedSoFar) / float64(elapsed)
		}
	}

	// Four stat gauge cards joined horizontally: Time, Speed (with a live
	// sparkline of recent speed samples), Size (live output file size) and ETA.
	// The card inner widths are chosen so the joined row plus its three
	// separators fits the 74-cell box content area without wrapping.
	timeCard := gaugeCard("⏱", lipgloss.Color("#FFFFFF"), "Time", fmt.Sprintf("%s / %s",
		formatDuration(elapsed), formatDuration(estimatedTotal)), 13)
	speedValue := fmt.Sprintf("%.1fx %s", speed, sparkline(m.speedHistory))
	speedCard := gaugeCard("⚡", theme.WarmGray, "Speed", speedValue, 12)
	sizeCard := gaugeCard("🖬", lipgloss.Color("#FF8C00"), "Size", formatSizeGlyph(m.renderState.FileSize), 11)
	etaCard := gaugeCard("🞋", lipgloss.Color("#FF2D2D"), "ETA", formatDuration(eta), 10)

	s.WriteString(lipgloss.JoinHorizontal(lipgloss.Top, timeCard, " ", speedCard, " ", sizeCard, " ", etaCard))

	// Frame counter and a compact source/codec/size summary on one line, below the
	// gauge cards.
	s.WriteString("\n")
	m.writeFrameSourceLine(s)
}

// recordSpeedSample appends the current realtime speed to the bounded history
// used by the Speed card's sparkline, dropping the oldest sample past the cap.
// Mirrors the speed computed in renderRenderingProgress.
func (m *Model) recordSpeedSample(msg RenderProgress) {
	if msg.TotalFrames == 0 {
		return
	}
	elapsed := time.Since(m.pass2StartTime)
	if m.pass2StartTime.IsZero() {
		elapsed = msg.Elapsed
	}
	if elapsed <= 0 {
		return
	}
	videoEncodedSoFar := time.Duration(msg.Frame) * time.Second / config.FPS
	speed := float64(videoEncodedSoFar) / float64(elapsed)

	m.speedHistory = append(m.speedHistory, speed)
	if len(m.speedHistory) > speedHistoryCap {
		m.speedHistory = m.speedHistory[len(m.speedHistory)-speedHistoryCap:]
	}
}

func (m *Model) renderSpectrumAndStats(s *strings.Builder) {
	s.WriteString(lipgloss.NewStyle().Foreground(theme.FireOrange).Render("Live Visualisation:"))
	s.WriteString("\n")

	// Size the spectrum to the preview box's rendered width so its left edge and
	// width line up with the preview below. renderSpectrum upsamples the 64 bars
	// across the wider column count. Draw the spring positions, not the raw target
	// heights, so bars ease toward new BarHeights over successive ticks. The
	// springs are advanced only in the tickMsg case; renderSpectrum stays pure
	// over its inputs.
	spectrum := renderSpectrum(m.spectrumPos, m.spectrumWidth())
	s.WriteString(spectrum)

	// Video preview
	if !m.noPreview {
		if m.renderState.FrameData != nil && m.renderState.Frame != m.cachedFrameNum {
			config := DefaultPreviewConfig()
			preview := DownsampleFrame(m.renderState.FrameData, config)
			m.cachedPreview = RenderPreview(preview)
			m.cachedFrameNum = m.renderState.Frame
		}

		if m.cachedPreview != "" {
			s.WriteString("\n")
			s.WriteString(m.cachedPreview)
		}
	}
}

// spectrumWidth returns the spectrum width. When the preview is shown it equals
// the preview box's rendered width (preview content + its 1-cell border on each
// side), which also equals the box content area, so the spectrum spans the
// preview edge to edge. Without a preview it falls back to the box content area.
func (m *Model) spectrumWidth() int {
	if !m.noPreview {
		return DefaultPreviewConfig().Width + 2
	}
	return max(m.boxContentWidth()-6, 10)
}

// writeFrameSourceLine writes the combined "🎞 Frame X / Y    ♪ duration · video
// · audio" line. The output file size lives in the Size gauge card instead. The
// source summary is omitted until any duration/codec data arrives.
func (m *Model) writeFrameSourceLine(s *strings.Builder) {
	labelStyle := lipgloss.NewStyle().Foreground(theme.WarmGray)
	valueStyle := lipgloss.NewStyle().Bold(true)

	frame := lipgloss.JoinHorizontal(lipgloss.Top,
		labelStyle.Render("🎞 Frame "),
		valueStyle.Render(fmt.Sprintf("%d / %d", m.renderState.Frame, m.renderState.TotalFrames)),
	)

	var parts []string
	if m.audioProfile != nil {
		parts = append(parts, fmt.Sprintf("%.1fs", m.audioProfile.Duration.Seconds()))
	}
	if m.renderState.VideoCodec != "" {
		parts = append(parts, compactCodec(m.renderState.VideoCodec))
	}
	if m.renderState.AudioCodec != "" {
		parts = append(parts, compactCodec(m.renderState.AudioCodec))
	}

	s.WriteString(frame)
	if len(parts) > 0 {
		source := lipgloss.JoinHorizontal(lipgloss.Top,
			labelStyle.Render("    ♪ "),
			valueStyle.Render(strings.Join(parts, " · ")),
		)
		s.WriteString(source)
	}
}

// Helper functions

// maxLineWidth returns the widest rendered line in s, measured in terminal
// cells. Used to size the spectrum so the spectrum+stats row fits the box.
func maxLineWidth(s string) int {
	maxw := 0
	for line := range strings.SplitSeq(s, "\n") {
		if w := lipgloss.Width(line); w > maxw {
			maxw = w
		}
	}
	return maxw
}

// compactCodec shortens a codec description for the one-line source summary by
// dropping the resolution/layout suffix: "H.264 1920×1080" → "H.264" and
// "AAC 44.1kHz stereo" → "AAC 44.1kHz". Tokens beyond a ×-bearing or layout
// token are dropped so the frame/source line never wraps the box.
func compactCodec(codec string) string {
	fields := strings.Fields(codec)
	if len(fields) == 0 {
		return codec
	}
	kept := []string{fields[0]}
	for _, f := range fields[1:] {
		// Stop at a resolution token (contains ×) or a channel-layout word.
		if strings.ContainsRune(f, '×') || f == "mono" || f == "stereo" {
			break
		}
		kept = append(kept, f)
	}
	return strings.Join(kept, " ")
}

func formatDuration(d time.Duration) string {
	if d == 0 {
		return "0s"
	}
	if d < time.Second {
		return fmt.Sprintf("%dms", d.Milliseconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

func formatBytes(bytes int64) string {
	if bytes == 0 {
		return "0 B"
	}

	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}

	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}

	units := []string{"KB", "MB", "GB"}
	return fmt.Sprintf("%.1f %s", float64(bytes)/float64(div), units[exp])
}

// formatSizeGlyph formats a byte count for the compact Size gauge card. MB and
// GB use the single-cell unit glyphs ㎆ and ㎇ with no separating space (e.g.
// "152.8㎆", "1.2㎇"); the rare small cases keep plain "KB"/"B".
func formatSizeGlyph(bytes int64) string {
	const unit = 1024
	switch {
	case bytes <= 0:
		return "0㎆"
	case bytes < unit:
		return fmt.Sprintf("%d B", bytes)
	case bytes < unit*unit:
		return fmt.Sprintf("%.1f KB", float64(bytes)/unit)
	case bytes < unit*unit*unit:
		return fmt.Sprintf("%.1f㎆", float64(bytes)/(unit*unit))
	default:
		return fmt.Sprintf("%.1f㎇", float64(bytes)/(unit*unit*unit))
	}
}
