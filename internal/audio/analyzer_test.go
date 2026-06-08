package audio

import (
	"math"
	"testing"

	"github.com/linuxmatters/jivefire/internal/config"
)

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
	profile := mustAnalyze(t)

	// Optimal baseScale should be calculated as: 0.85 / GlobalPeak
	expectedBaseScale := 0.85 / profile.GlobalPeak

	if profile.OptimalBaseScale != expectedBaseScale {
		t.Errorf("OptimalBaseScale mismatch: expected %.6f, got %.6f",
			expectedBaseScale, profile.OptimalBaseScale)
	}

	// When multiplied by GlobalPeak and sensitivity 1.0, should give ~0.85
	testValue := profile.GlobalPeak * profile.OptimalBaseScale * 1.0
	if testValue < 0.84 || testValue > 0.86 {
		t.Errorf("OptimalBaseScale validation failed: GlobalPeak * OptimalBaseScale = %.6f (expected ~0.85)", testValue)
	}

	t.Logf("OptimalBaseScale correctly calculated: %.6f", profile.OptimalBaseScale)
	t.Logf("Verification: GlobalPeak (%.6f) × OptimalBaseScale (%.6f) = %.6f",
		profile.GlobalPeak, profile.OptimalBaseScale, testValue)
}

func TestDynamicRangeCalculation(t *testing.T) {
	profile := mustAnalyze(t)

	expectedDynamicRange := profile.GlobalPeak / profile.GlobalRMS

	// Allow small floating point error
	diff := profile.DynamicRange - expectedDynamicRange
	if diff < -0.01 || diff > 0.01 {
		t.Errorf("DynamicRange (%.2f) doesn't match GlobalPeak/GlobalRMS (%.2f)",
			profile.DynamicRange, expectedDynamicRange)
	}

	t.Logf("DynamicRange correctly calculated: %.2f (Peak %.6f / RMS %.6f)",
		profile.DynamicRange, profile.GlobalPeak, profile.GlobalRMS)
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
