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
	s.WriteString("Progress: ")
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

	// Audio Profile
	s.WriteString("\n")
	m.renderAudioProfile(&s)

	// Final spectrum - zeroed out to show clean state
	spectrumWidth := m.width - 4
	if spectrumWidth < 10 {
		spectrumWidth = 100 // default if terminal size not set
	} else if spectrumWidth > 100 {
		spectrumWidth = 100
	}
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

	// Audio Profile (always shown, placeholder if not yet available)
	s.WriteString("\n")
	m.renderAudioProfile(&s)

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

		s.WriteString("Progress: ")
		s.WriteString(progressBar)
		fmt.Fprintf(s, "  %d%%", int(percent*100))
		s.WriteString("\n\n")
	case m.analysisProgress.Frame > 0:
		// No total, show frame count with elapsed time. Spinner signals live work.
		s.WriteString(m.spinner.View())
		s.WriteString(" ")
		s.WriteString(lipgloss.NewStyle().Faint(true).Render("Analysing..."))
		fmt.Fprintf(s, "  %d frames  │  Elapsed: %s\n\n",
			m.analysisProgress.Frame,
			formatDuration(m.analysisProgress.Duration))
	default:
		// Dead air before any frames arrive: spinner is the only motion.
		s.WriteString(m.spinner.View())
		s.WriteString(" ")
		s.WriteString(lipgloss.NewStyle().Faint(true).Render("Starting analysis...\n\n"))
	}
}

func (m *Model) renderRenderingProgress(s *strings.Builder) {
	if m.renderState.TotalFrames == 0 {
		// Dead air before the first render frame: spinner is the only motion.
		s.WriteString(m.spinner.View())
		s.WriteString(" ")
		s.WriteString(lipgloss.NewStyle().Faint(true).Render("Starting render...\n\n"))
		return
	}

	// Progress is based on video frames (audio is encoded alongside each frame)
	percent := float64(m.renderState.Frame) / float64(m.renderState.TotalFrames)
	currentPhase := fmt.Sprintf("Frame %d of %d", m.renderState.Frame, m.renderState.TotalFrames)

	progressBar := m.progressBar.View()
	s.WriteString("Progress: ")
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

	timingInfo := fmt.Sprintf("Time: %s / %s  │  Speed: %.1fx realtime  │  ETA: %s",
		formatDuration(elapsed),
		formatDuration(estimatedTotal),
		speed,
		formatDuration(eta))

	s.WriteString(lipgloss.NewStyle().Faint(true).Render(timingInfo))
	s.WriteString("\n")

	phaseStyle := lipgloss.NewStyle().Faint(true).Italic(true)
	s.WriteString(phaseStyle.Render(currentPhase))
}

func (m *Model) renderSpectrumAndStats(s *strings.Builder) {
	s.WriteString(lipgloss.NewStyle().Foreground(theme.FireOrange).Render("Live Visualisation:"))
	s.WriteString("\n")

	// Use full width for spectrum (64 bars matches the actual bar count)
	spectrumWidth := 64
	if m.width > 10 {
		spectrumWidth = min(m.width-10, 64)
	}
	// Draw the spring positions, not the raw target heights, so bars ease toward
	// new BarHeights over successive ticks. The springs are advanced only in the
	// tickMsg case; renderSpectrum stays pure over its inputs.
	spectrum := renderSpectrum(m.spectrumPos, spectrumWidth)

	var rightCol strings.Builder
	if m.renderState.FileSize > 0 || m.renderState.VideoCodec != "" || m.renderState.AudioCodec != "" {
		labelStyle := lipgloss.NewStyle().Foreground(theme.WarmGray)
		valueStyle := lipgloss.NewStyle().Bold(true)

		rightCol.WriteString(labelStyle.Render("File:  "))
		rightCol.WriteString(valueStyle.Render(formatBytes(m.renderState.FileSize)))
		rightCol.WriteString("\n")

		if m.renderState.VideoCodec != "" {
			rightCol.WriteString(labelStyle.Render("Video: "))
			rightCol.WriteString(valueStyle.Render(m.renderState.VideoCodec))
			rightCol.WriteString("\n")
		}

		if m.renderState.AudioCodec != "" {
			rightCol.WriteString(labelStyle.Render("Audio: "))
			rightCol.WriteString(valueStyle.Render(m.renderState.AudioCodec))
		}
	}

	s.WriteString(lipgloss.JoinHorizontal(lipgloss.Top,
		spectrum,
		"  ",
		rightCol.String()))

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

// Helper functions

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
