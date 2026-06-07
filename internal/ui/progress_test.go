package ui

import (
	"strings"
	"testing"
	"time"

	"charm.land/bubbles/v2/progress"
	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"
)

// asModel casts the tea.Model returned by Update back to *Model, failing the
// test if the concrete type is unexpected.
func asModel(t *testing.T, m tea.Model) *Model {
	t.Helper()
	model, ok := m.(*Model)
	if !ok {
		t.Fatalf("Update returned %T, want *Model", m)
	}
	return model
}

// assertCmdNil fails if cmd is non-nil.
func assertCmdNil(t *testing.T, cmd tea.Cmd) {
	t.Helper()
	if cmd != nil {
		t.Errorf("expected nil tea.Cmd, got %T", cmd())
	}
}

// assertCmdMsg invokes a non-nil cmd and returns its message for type
// assertions. It fails if cmd is nil.
func assertCmdMsg(t *testing.T, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected non-nil tea.Cmd, got nil")
	}
	return cmd()
}

func TestUpdateAnalysisProgressStoresState(t *testing.T) {
	tests := []struct {
		name    string
		msg     AnalysisProgress
		wantCmd bool // with a known total the bar's SetPercent returns a cmd
	}{
		{
			name: "frame count without total",
			msg:  AnalysisProgress{Frame: 42, Duration: 3 * time.Second},
		},
		{
			name: "frame count with total and levels",
			msg: AnalysisProgress{
				Frame:       100,
				TotalFrames: 500,
				CurrentRMS:  0.25,
				CurrentPeak: 0.9,
				BarHeights:  []float64{0.1, 0.2, 0.3},
			},
			wantCmd: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewModel(true)
			next, cmd := m.Update(tc.msg)
			got := asModel(t, next)

			if tc.wantCmd {
				if cmd == nil {
					t.Error("expected non-nil SetPercent cmd for known total, got nil")
				}
			} else {
				assertCmdNil(t, cmd)
			}
			if got.analysisProgress.Frame != tc.msg.Frame ||
				got.analysisProgress.TotalFrames != tc.msg.TotalFrames ||
				got.analysisProgress.CurrentRMS != tc.msg.CurrentRMS ||
				got.analysisProgress.CurrentPeak != tc.msg.CurrentPeak ||
				got.analysisProgress.Duration != tc.msg.Duration ||
				len(got.analysisProgress.BarHeights) != len(tc.msg.BarHeights) {
				t.Errorf("analysisProgress = %+v, want %+v", got.analysisProgress, tc.msg)
			}
			if got.phase != PhaseAnalysis {
				t.Errorf("phase = %d, want PhaseAnalysis", got.phase)
			}
		})
	}
}

func TestUpdateRenderProgressStoresState(t *testing.T) {
	tests := []struct {
		name    string
		msg     RenderProgress
		wantCmd bool // with a known total the bar's SetPercent returns a cmd
	}{
		{
			name: "starting render",
			msg:  RenderProgress{},
		},
		{
			name: "mid render with codecs",
			msg: RenderProgress{
				Frame:       250,
				TotalFrames: 1000,
				Elapsed:     5 * time.Second,
				FileSize:    1024,
				Sensitivity: 1.5,
				VideoCodec:  "libx264",
				AudioCodec:  "aac",
			},
			wantCmd: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewModel(true)
			next, cmd := m.Update(tc.msg)
			got := asModel(t, next)

			if tc.wantCmd {
				if cmd == nil {
					t.Error("expected non-nil SetPercent cmd for known total, got nil")
				}
			} else {
				assertCmdNil(t, cmd)
			}
			if got.renderState.Frame != tc.msg.Frame ||
				got.renderState.TotalFrames != tc.msg.TotalFrames ||
				got.renderState.Elapsed != tc.msg.Elapsed ||
				got.renderState.VideoCodec != tc.msg.VideoCodec ||
				got.renderState.AudioCodec != tc.msg.AudioCodec {
				t.Errorf("renderState = %+v, want %+v", got.renderState, tc.msg)
			}
		})
	}
}

func TestUpdatePhaseTransitions(t *testing.T) {
	tests := []struct {
		name        string
		msg         tea.Msg
		wantPhase   Phase
		wantProfile bool
	}{
		{
			name: "analysis complete moves to rendering",
			msg: AnalysisComplete{
				PeakMagnitude: 1.0,
				RMSLevel:      0.5,
				DynamicRange:  2.0,
				Duration:      60 * time.Second,
				OptimalScale:  1.25,
				AnalysisTime:  2 * time.Second,
			},
			wantPhase:   PhaseRendering,
			wantProfile: true,
		},
		{
			name:        "analysis progress keeps analysis phase",
			msg:         AnalysisProgress{Frame: 1},
			wantPhase:   PhaseAnalysis,
			wantProfile: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewModel(true)
			next, cmd := m.Update(tc.msg)
			got := asModel(t, next)

			assertCmdNil(t, cmd)
			if got.phase != tc.wantPhase {
				t.Errorf("phase = %d, want %d", got.phase, tc.wantPhase)
			}
			if tc.wantProfile {
				if got.audioProfile == nil {
					t.Fatal("audioProfile = nil, want populated")
				}
				if got.pass2StartTime.IsZero() {
					t.Error("pass2StartTime not set on rendering transition")
				}
			}
		})
	}
}

func TestUpdateRenderComplete(t *testing.T) {
	m := NewModel(true)
	// Zero the delay so the scheduled tea.Tick fires immediately when invoked,
	// keeping the cmd-message assertion deterministic without a real wait.
	m.completionDelay = 0
	msg := RenderComplete{
		OutputFile:  "out.mp4",
		Duration:    60 * time.Second,
		FileSize:    2048,
		TotalFrames: 1500,
		TotalTime:   10 * time.Second,
		EncoderName: "libx264",
	}

	next, cmd := m.Update(msg)
	got := asModel(t, next)

	if got.complete == nil {
		t.Fatal("complete = nil, want populated")
	}
	if got.complete.OutputFile != msg.OutputFile {
		t.Errorf("complete.OutputFile = %q, want %q", got.complete.OutputFile, msg.OutputFile)
	}
	if got.phase != PhaseComplete {
		t.Errorf("phase = %d, want PhaseComplete", got.phase)
	}
	if !got.quitting {
		t.Error("quitting = false, want true")
	}
	if got.completionTime.IsZero() {
		t.Error("completionTime not set")
	}

	// RenderComplete schedules a delayed quit; with the delay zeroed above the
	// cmd resolves to progressQuitMsg immediately.
	if _, ok := assertCmdMsg(t, cmd).(progressQuitMsg); !ok {
		t.Error("RenderComplete cmd did not yield progressQuitMsg")
	}
}

func TestUpdateRenderCompleteCmdNonNil(t *testing.T) {
	m := NewModel(true)
	_, cmd := m.Update(RenderComplete{TotalFrames: 100, TotalTime: time.Second})
	if cmd == nil {
		t.Fatal("RenderComplete returned nil cmd, want delayed quit cmd")
	}
}

func TestInitReturnsTick(t *testing.T) {
	m := NewModel(true)
	cmd := m.Init()
	if cmd == nil {
		t.Fatal("Init returned nil cmd, want a UI tick cmd")
	}
	// Init batches the repaint tick with the spinner's own tick, so the cmd
	// yields a tea.BatchMsg. Both clocks must start: a tickMsg and a
	// spinner.TickMsg appear among the batched cmds' messages.
	batch, ok := assertCmdMsg(t, cmd).(tea.BatchMsg)
	if !ok {
		t.Fatalf("Init cmd did not yield tea.BatchMsg, got %T", batch)
	}
	var gotTick, gotSpinnerTick bool
	for _, c := range batch {
		if c == nil {
			continue
		}
		switch c().(type) {
		case tickMsg:
			gotTick = true
		case spinner.TickMsg:
			gotSpinnerTick = true
		}
	}
	if !gotTick {
		t.Error("Init batch did not start the repaint tick (tickMsg)")
	}
	if !gotSpinnerTick {
		t.Error("Init batch did not start the spinner tick (spinner.TickMsg)")
	}
}

func TestUpdateTickReschedules(t *testing.T) {
	m := NewModel(true)
	next, cmd := m.Update(tickMsg{})
	asModel(t, next)

	// The tick must be self-perpetuating: each tickMsg re-issues another tick.
	if cmd == nil {
		t.Fatal("tickMsg returned nil cmd, want a re-scheduled tick")
	}
	if _, ok := assertCmdMsg(t, cmd).(tickMsg); !ok {
		t.Error("tickMsg cmd did not re-issue a tickMsg")
	}
}

func TestUpdateAnalysisProgressSetsTarget(t *testing.T) {
	m := NewModel(true)
	// A known total triggers SetPercent, which sets the bar's target and starts
	// the spring animating toward it.
	_, cmd := m.Update(AnalysisProgress{Frame: 50, TotalFrames: 100})
	if cmd == nil {
		t.Fatal("AnalysisProgress with total returned nil cmd, want SetPercent cmd")
	}
	if m.progressBar.Percent() != 0.5 {
		t.Errorf("progress bar target = %v, want 0.5", m.progressBar.Percent())
	}
	if !m.progressBar.IsAnimating() {
		t.Error("progress bar not animating after SetPercent")
	}
}

func TestUpdateProgressFrameMsgAdvancesBar(t *testing.T) {
	m := NewModel(true)
	// Seed the bar toward a target via the render path; the returned cmd yields a
	// genuine progress.FrameMsg carrying the bar's internal id/tag.
	_, seed := m.Update(RenderProgress{Frame: 80, TotalFrames: 100})
	frame, ok := assertCmdMsg(t, seed).(progress.FrameMsg)
	if !ok {
		t.Fatalf("SetPercent cmd did not yield progress.FrameMsg, got %T", frame)
	}

	// Routing that FrameMsg through Update must drive progressBar.Update, advance
	// the spring's visible fill, and return a follow-up animation cmd. View()
	// renders the visible (animated) value, so it changes as the spring moves.
	before := m.progressBar.View()
	next, cmd := m.Update(frame)
	got := asModel(t, next)

	if got.progressBar.View() == before {
		t.Error("progress bar fill did not advance on FrameMsg")
	}
	if cmd == nil {
		t.Error("FrameMsg returned nil cmd, want a follow-up animation cmd")
	}
}

// TestRenderingTimerIsTickDriven documents that the Pass 2 elapsed/ETA timers
// are derived from time.Since(m.pass2StartTime) at render time, not frozen from
// the RenderProgress.Elapsed message field. Two renders that differ only by
// pass2StartTime (no intervening p.Send / Update) must produce different elapsed
// output, proving the ~60ms tick repaint advances the timers between data
// updates.
func TestRenderingTimerIsTickDriven(t *testing.T) {
	now := time.Now()

	render := func(start time.Time) string {
		m := NewModel(true)
		m.phase = PhaseRendering
		m.pass2StartTime = start
		// Identical render data for both renders: only pass2StartTime differs, so
		// any change in output must come from time.Since, not the message field.
		m.renderState = RenderProgress{
			Frame:       250,
			TotalFrames: 1000,
			Elapsed:     5 * time.Second, // stale field; must NOT drive the display
		}
		var s strings.Builder
		m.renderRenderingProgress(&s)
		return s.String()
	}

	// A longer-ago start time means more wall-clock elapsed at render time.
	recent := render(now.Add(-2 * time.Second))
	older := render(now.Add(-30 * time.Second))

	if recent == older {
		t.Error("rendering timer output identical across different pass2StartTime; " +
			"elapsed is data-driven, not tick-driven (should derive from time.Since)")
	}
}

func TestSpinnerShownOnlyInDeadAir(t *testing.T) {
	// The spinner's current glyph rune survives lipgloss styling, so testing for
	// it in the rendered output isolates the spinner from the progress bar (which
	// uses block runes only).
	glyph := func(m *Model) string {
		return strings.TrimSpace(stripStyles(m.spinner.View()))
	}

	t.Run("analysis dead air shows spinner", func(t *testing.T) {
		m := NewModel(true)
		m.phase = PhaseAnalysis
		// No total frames: dead-air "Starting analysis..." branch.
		var s strings.Builder
		m.renderAnalysisProgress(&s)
		if !strings.Contains(s.String(), glyph(m)) {
			t.Error("spinner glyph absent from dead-air analysis render")
		}
	})

	t.Run("analysis with progress hides spinner", func(t *testing.T) {
		m := NewModel(true)
		m.phase = PhaseAnalysis
		m.analysisProgress = AnalysisProgress{Frame: 50, TotalFrames: 100}
		var s strings.Builder
		m.renderAnalysisProgress(&s)
		if strings.Contains(s.String(), glyph(m)) {
			t.Error("spinner glyph present in analysis render with progress data")
		}
	})

	t.Run("render dead air shows spinner", func(t *testing.T) {
		m := NewModel(true)
		m.phase = PhaseRendering
		// No total frames: dead-air "Starting render..." branch.
		var s strings.Builder
		m.renderRenderingProgress(&s)
		if !strings.Contains(s.String(), glyph(m)) {
			t.Error("spinner glyph absent from dead-air rendering progress")
		}
	})

	t.Run("render with progress hides spinner", func(t *testing.T) {
		m := NewModel(true)
		m.phase = PhaseRendering
		m.pass2StartTime = time.Now()
		m.renderState = RenderProgress{Frame: 250, TotalFrames: 1000}
		var s strings.Builder
		m.renderRenderingProgress(&s)
		if strings.Contains(s.String(), glyph(m)) {
			t.Error("spinner glyph present in rendering progress with progress data")
		}
	})
}

func TestUpdateSpinnerTick(t *testing.T) {
	m := NewModel(true)
	// The spinner advances its own clock and re-issues its tick, so routing a
	// spinner.TickMsg through Update returns a non-nil follow-up cmd.
	_, cmd := m.Update(m.spinner.Tick())
	if cmd == nil {
		t.Fatal("spinner.TickMsg returned nil cmd, want a re-scheduled spinner tick")
	}
	if _, ok := assertCmdMsg(t, cmd).(spinner.TickMsg); !ok {
		t.Error("spinner.TickMsg cmd did not re-issue a spinner.TickMsg")
	}
}

// stripStyles removes ANSI escape sequences so a glyph rune can be matched
// regardless of the lipgloss colour codes wrapping it.
func stripStyles(s string) string {
	var b strings.Builder
	inEscape := false
	for _, r := range s {
		switch {
		case r == 0x1b:
			inEscape = true
		case inEscape && r == 'm':
			inEscape = false
		case inEscape:
			// drop escape body
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func TestUpdateKeyPressQuits(t *testing.T) {
	tests := []struct {
		name string
		key  tea.KeyPressMsg
	}{
		{
			name: "q quits during processing",
			key:  tea.KeyPressMsg{Code: 'q', Text: "q"},
		},
		{
			name: "ctrl+c quits during processing",
			key:  tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := NewModel(true)
			_, cmd := m.Update(tc.key)
			if _, ok := assertCmdMsg(t, cmd).(tea.QuitMsg); !ok {
				t.Errorf("key %q did not yield tea.QuitMsg", tc.key.String())
			}
		})
	}
}

func TestUpdateProgressQuitMsgQuits(t *testing.T) {
	m := NewModel(true)
	_, cmd := m.Update(progressQuitMsg{})
	if _, ok := assertCmdMsg(t, cmd).(tea.QuitMsg); !ok {
		t.Error("progressQuitMsg cmd did not yield tea.QuitMsg")
	}
}
