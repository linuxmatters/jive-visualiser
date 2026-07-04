package audio

import (
	"bytes"
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/linuxmatters/jive-visualiser/internal/config"
)

// Sine fixture parameters. The frequency sits exactly on FFT bin 20 of a
// 2048-point transform at 44.1 kHz, so every analysis window sees an
// identical spectrum and the measured values below are deterministic.
const (
	sineAmplitude = 0.5
	sineFrequency = 20.0 * float64(config.SampleRate) / float64(config.FFTSize)
	sineSeconds   = 1
)

// Pre-computed fixture expectations for the sine WAV. These are literal
// values recorded from one reference run of the analysis pipeline, plus the
// analytical RMS of a 0.5-amplitude sine (0.5/sqrt(2)). They are NOT derived
// from the code's formulas at test time, so a formula change in the analyser
// (for example the 0.85 headroom constant) fails these tests.
const (
	sineExpectedPeak         = 41.6907  // measured raw bar magnitude
	sineExpectedRMS          = 0.35355  // 0.5 / sqrt(2), analytical
	sineExpectedBaseScale    = 0.020388 // 0.85 headroom / 41.6907 peak, pre-computed
	sineExpectedDynamicRange = 118.55   // 41.6907 peak / 0.351687 measured RMS, pre-computed
)

// writeSineWAV writes a mono 16-bit PCM WAV containing the fixture sine and
// returns its path.
func writeSineWAV(t *testing.T) string {
	t.Helper()

	// Constant expression, so the uint32 conversions below are checked at
	// compile time.
	const numSamples = config.SampleRate * sineSeconds
	const dataSize = uint32(numSamples * 2)
	samples := make([]int16, numSamples)
	for i := range samples {
		v := sineAmplitude * math.Sin(2*math.Pi*sineFrequency*float64(i)/float64(config.SampleRate))
		samples[i] = int16(v * 32767)
	}

	var buf bytes.Buffer
	write := func(v any) {
		if err := binary.Write(&buf, binary.LittleEndian, v); err != nil {
			t.Fatalf("writing WAV field: %v", err)
		}
	}
	buf.WriteString("RIFF")
	write(36 + dataSize)
	buf.WriteString("WAVEfmt ")
	write(uint32(16))                    // fmt chunk size
	write(uint16(1))                     // PCM
	write(uint16(1))                     // mono
	write(uint32(config.SampleRate))     // sample rate
	write(uint32(config.SampleRate * 2)) // byte rate
	write(uint16(2))                     // block align
	write(uint16(16))                    // bits per sample
	buf.WriteString("data")
	write(dataSize)
	write(samples)

	path := filepath.Join(t.TempDir(), "sine.wav")
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("writing sine fixture: %v", err)
	}
	return path
}

// analyzeSineFixture runs the analyser over the deterministic sine fixture.
func analyzeSineFixture(t *testing.T) *Profile {
	t.Helper()
	profile, err := AnalyzeAudio(writeSineWAV(t), nil)
	if err != nil {
		t.Fatalf("Failed to analyse sine fixture: %v", err)
	}
	return profile
}

func mustAnalyze(t *testing.T) *Profile {
	t.Helper()
	profile, err := AnalyzeAudio("../../testdata/LMP0.mp3", nil)
	if err != nil {
		t.Fatalf("Failed to analyse audio: %v", err)
	}
	return profile
}

func TestAnalyzeAudio(t *testing.T) {
	profile := mustAnalyze(t)

	if profile.NumFrames <= 0 {
		t.Errorf("Expected positive number of frames, got %d", profile.NumFrames)
	}

	if profile.SampleRate <= 0 {
		t.Errorf("Expected positive sample rate, got %d", profile.SampleRate)
	}

	if profile.Duration <= 0 {
		t.Errorf("Expected positive duration, got %.2f", profile.Duration)
	}

	if profile.GlobalPeak <= 0 {
		t.Errorf("Expected positive GlobalPeak, got %.6f", profile.GlobalPeak)
	}

	if profile.GlobalRMS <= 0 {
		t.Errorf("Expected positive GlobalRMS, got %.6f", profile.GlobalRMS)
	}

	if profile.DynamicRange <= 0 {
		t.Errorf("Expected positive DynamicRange, got %.2f", profile.DynamicRange)
	}

	if profile.OptimalBaseScale <= 0 {
		t.Errorf("Expected positive OptimalBaseScale, got %.6f", profile.OptimalBaseScale)
	}

	t.Logf("Analysis complete:")
	t.Logf("  Duration: %.1f seconds", profile.Duration)
	t.Logf("  Frames: %d", profile.NumFrames)
	t.Logf("  Global Peak: %.6f", profile.GlobalPeak)
	t.Logf("  Global RMS: %.6f", profile.GlobalRMS)
	t.Logf("  Dynamic Range: %.2f", profile.DynamicRange)
	t.Logf("  Optimal Scale: %.6f", profile.OptimalBaseScale)
}

func TestAnalyzeAudioInvalidFile(t *testing.T) {
	_, err := AnalyzeAudio("nonexistent.mp3", nil)
	if err == nil {
		t.Error("Expected error for nonexistent file, got nil")
	}
}

func TestOptimalBaseScaleCalculation(t *testing.T) {
	profile := analyzeSineFixture(t)

	// The fixture spectrum is deterministic, so the global peak must match the
	// recorded reference value.
	if !withinRelative(profile.GlobalPeak, sineExpectedPeak, 0.002) {
		t.Errorf("GlobalPeak mismatch: expected ~%.6f, got %.6f",
			sineExpectedPeak, profile.GlobalPeak)
	}

	// Assert against the pre-computed literal, not a formula recomputed here.
	if !withinRelative(profile.OptimalBaseScale, sineExpectedBaseScale, 0.002) {
		t.Errorf("OptimalBaseScale mismatch: expected ~%.6f, got %.6f",
			sineExpectedBaseScale, profile.OptimalBaseScale)
	}

	t.Logf("GlobalPeak: %.6f, OptimalBaseScale: %.6f",
		profile.GlobalPeak, profile.OptimalBaseScale)
}

func TestDynamicRangeCalculation(t *testing.T) {
	profile := analyzeSineFixture(t)

	// A 0.5-amplitude sine has RMS 0.5/sqrt(2) ~= 0.35355. The last few frames
	// include zero padding at end of file, hence the loose tolerance.
	if !withinRelative(profile.GlobalRMS, sineExpectedRMS, 0.01) {
		t.Errorf("GlobalRMS mismatch: expected ~%.5f, got %.6f",
			sineExpectedRMS, profile.GlobalRMS)
	}

	// Assert against the pre-computed literal, not a formula recomputed here.
	if !withinRelative(profile.DynamicRange, sineExpectedDynamicRange, 0.01) {
		t.Errorf("DynamicRange mismatch: expected ~%.4f, got %.4f",
			sineExpectedDynamicRange, profile.DynamicRange)
	}

	t.Logf("DynamicRange: %.4f (Peak %.6f / RMS %.6f)",
		profile.DynamicRange, profile.GlobalPeak, profile.GlobalRMS)
}

// withinRelative reports whether got is within tol (relative) of want.
func withinRelative(got, want, tol float64) bool {
	return math.Abs(got-want) <= tol*math.Abs(want)
}

func TestAnalyzeFrameDirectly(t *testing.T) {
	// 440 Hz sine wave at 0.5 amplitude.
	testSamples := make([]float64, config.FFTSize)
	for i := range testSamples {
		testSamples[i] = 0.5 * math.Sin(2*math.Pi*440*float64(i)/float64(config.SampleRate))
	}

	processor, err := NewProcessor()
	if err != nil {
		t.Fatal(err)
	}
	defer processor.Close()
	coeffs := processor.ProcessChunk(testSamples)

	analysis := analyzeFrame(coeffs, testSamples, nil)

	if analysis.PeakMagnitude <= 0 {
		t.Errorf("Expected positive PeakMagnitude, got %.6f", analysis.PeakMagnitude)
	}

	if analysis.RMSLevel <= 0 {
		t.Errorf("Expected positive RMSLevel, got %.6f", analysis.RMSLevel)
	}

	// For a 0.5 amplitude sine wave, RMS should be approximately 0.5/sqrt(2) ≈ 0.353
	expectedRMS := 0.5 / math.Sqrt(2)
	if analysis.RMSLevel < expectedRMS-0.01 || analysis.RMSLevel > expectedRMS+0.01 {
		t.Errorf("RMS mismatch: expected ~%.3f, got %.3f", expectedRMS, analysis.RMSLevel)
	}

	t.Logf("Direct frame analysis:")
	t.Logf("  Peak Magnitude: %.6f", analysis.PeakMagnitude)
	t.Logf("  RMS Level: %.6f (expected ~%.3f)", analysis.RMSLevel, expectedRMS)
}
