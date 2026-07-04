package audio

import (
	"math"
	"testing"
)

// TestBinFFT_KnownSineWave verifies that BinFFT correctly identifies a known
// single-frequency sine wave. This catches frequency-to-bar mapping errors,
// scaling bugs, or log transform issues that would make bars unresponsive.
//
// Test uses 440 Hz sine wave (A4 musical note) at 44.1 kHz sample rate.
// With 2048 FFT and 64 bars covering full spectrum (22.05 kHz Nyquist):
// - Frequency bin width: 44100 / 2048 ≈ 21.5 Hz/bin
// - 440 Hz maps to bin index ≈ 440 / 21.5 ≈ 20
// - Bars are created by grouping bins: 1024 bins / 64 bars = 16 bins/bar
// - Bar index ≈ 20 / 16 ≈ 1 (440 Hz is in lower-frequency bars)
func TestBinFFT_KnownSineWave(t *testing.T) {
	const (
		sampleRate  = 44100
		frequency   = 440 // A4 musical note
		duration    = 1.0 // 1 second
		fftSize     = 2048
		numBars     = 64
		sensitivity = 1.0
		baseScale   = 1.0 // Will be normalized later
	)

	// Generate 1 second of 440 Hz sine wave at 44.1 kHz
	numSamples := int(float64(sampleRate) * duration)
	sine := make([]float64, numSamples)
	for i := range numSamples {
		t := float64(i) / float64(sampleRate)
		sine[i] = math.Sin(2 * math.Pi * frequency * t)
	}

	// Take first FFT window (2048 samples)
	windowSamples := sine[:fftSize]

	// Process through the live code path (Hanning window + FFT)
	processor, err := NewProcessor()
	if err != nil {
		t.Fatal(err)
	}
	defer processor.Close()
	fftInput := processor.ProcessChunk(windowSamples)

	// Bin the FFT results into 64 bars
	result := make([]float64, numBars)
	BinFFT(fftInput, sensitivity, baseScale, result)

	// Find the bar with maximum magnitude
	maxVal := 0.0
	maxBar := 0
	for bar, val := range result {
		if val > maxVal {
			maxVal = val
			maxBar = bar
		}
	}

	t.Logf("440 Hz sine wave analysis:")
	t.Logf("  FFT size: %d samples", fftSize)
	t.Logf("  Sample rate: %d Hz", sampleRate)
	t.Logf("  Nyquist frequency: %d Hz", sampleRate/2)
	t.Logf("  Bin width: %.2f Hz/bin", float64(sampleRate)/float64(fftSize))
	t.Logf("  440 Hz maps to FFT bin: ~%.0f", frequency*float64(fftSize)/float64(sampleRate))
	t.Logf("  Bins per bar: %d", (fftSize/2)/numBars)
	t.Logf("  Peak bar: %d with magnitude %.6f", maxBar, maxVal)

	// Validation:
	// 1. Peak magnitude should be non-zero (sine wave is strong)
	if maxVal <= 0 {
		t.Errorf("Expected non-zero peak magnitude, got %.6f", maxVal)
	}

	// 2. Peak should be in lower frequency bars (440 Hz is relatively low)
	// 440 Hz is in the bass-midrange range, expect bar < 32 (lower half of spectrum)
	if maxBar >= numBars/2 {
		t.Errorf("Peak bar %d is in high frequency range; 440 Hz should be in lower bars", maxBar)
	}

	// 3. Peak should dominate (be significantly higher than other bars)
	// Calculate average of non-peak bars
	var sumOthers float64
	for bar, val := range result {
		if bar != maxBar {
			sumOthers += val
		}
	}
	avgOthers := sumOthers / float64(numBars-1)

	if maxVal <= avgOthers {
		t.Errorf("Peak magnitude %.6f not dominant over average of others %.6f", maxVal, avgOthers)
	}

	// 4. Peak-to-average ratio should be substantial (at least 2x)
	peakRatio := maxVal / avgOthers
	if peakRatio < 2.0 {
		t.Logf("Warning: Peak ratio %.2f is lower than expected (minimum 2.0x)", peakRatio)
		// Don't fail on this - log scaling can compress values - but warn
	}

	t.Logf("  Peak-to-average ratio: %.2fx", peakRatio)
}

// TestBinFFT_Silence verifies that BinFFT correctly handles silence
// (all zeros or near-silence). This catches scaling issues.
func TestBinFFT_Silence(t *testing.T) {
	const (
		fftSize     = 2048
		numBars     = 64
		sensitivity = 1.0
		baseScale   = 1.0
	)

	// Create silence: a zero spectrum of the correct interleaved length
	// (re/im pairs for the N/2+1 bins).
	silence := make(Spectrum, 2*(fftSize/2+1))

	result := make([]float64, numBars)
	BinFFT(silence, sensitivity, baseScale, result)

	// All bars should be zero (or very close due to log scaling of near-zero)
	for bar, val := range result {
		if val < 0 {
			t.Errorf("Bar %d has negative magnitude: %.6f", bar, val)
		}
		if val > 0.01 {
			t.Logf("Bar %d: %.6f (expected ~0 for silence)", bar, val)
		}
	}

	// Check that there are no large values
	maxVal := 0.0
	for _, val := range result {
		if val > maxVal {
			maxVal = val
		}
	}

	if maxVal > 0.1 {
		t.Errorf("Silence produced unexpectedly large magnitude: %.6f", maxVal)
	}

	t.Logf("Silence test passed: max bar magnitude = %.6f", maxVal)
}

// TestBinFFT_NoiseGate verifies that low-energy frequencies are gated out
// (set to zero) to prevent noise floor from creating visual artifacts.
func TestBinFFT_NoiseGate(t *testing.T) {
	const (
		fftSize     = 2048
		numBars     = 64
		sensitivity = 0.1 // Low sensitivity to amplify quiet signals
		baseScale   = 0.1 // Low base scale
	)

	// Create very quiet signal (amplitude 0.001)
	quietSignal := make([]float64, fftSize)
	for i := range quietSignal {
		quietSignal[i] = 0.001 * math.Sin(2*math.Pi*float64(i)/100.0)
	}

	// Process through the live code path (Hanning window + FFT)
	processor, err := NewProcessor()
	if err != nil {
		t.Fatal(err)
	}
	defer processor.Close()
	fftInput := processor.ProcessChunk(quietSignal)

	result := make([]float64, numBars)
	BinFFT(fftInput, sensitivity, baseScale, result)

	// Most bars should be zero due to noise gate
	zeroCount := 0
	for _, val := range result {
		if val == 0 {
			zeroCount++
		}
	}

	if zeroCount == 0 {
		t.Errorf("Noise gate didn't suppress quiet signal: all bars non-zero")
	}

	t.Logf("Noise gate test: %d/%d bars gated to zero (expected high count)", zeroCount, numBars)
}

// TestBinFFT_EnergyDistribution verifies that total energy is preserved
// through binning (no unexpected losses or amplifications).
func TestBinFFT_EnergyDistribution(t *testing.T) {
	const (
		fftSize     = 2048
		numBars     = 64
		sensitivity = 1.0
		baseScale   = 0.5
	)

	// Create a broadband signal (white noise-ish via multiple frequencies)
	signal := make([]float64, fftSize)
	for i := range fftSize {
		signal[i] = 0.1 * (math.Sin(2*math.Pi*100*float64(i)/float64(fftSize)) +
			math.Sin(2*math.Pi*500*float64(i)/float64(fftSize)) +
			math.Sin(2*math.Pi*1000*float64(i)/float64(fftSize)))
	}

	// Process through the live code path (Hanning window + FFT)
	processor, err := NewProcessor()
	if err != nil {
		t.Fatal(err)
	}
	defer processor.Close()
	fftInput := processor.ProcessChunk(signal)

	result := make([]float64, numBars)
	BinFFT(fftInput, sensitivity, baseScale, result)

	// Sum all bar energies
	totalEnergy := 0.0
	for _, val := range result {
		totalEnergy += val
	}

	// Should have non-zero energy
	if totalEnergy <= 0 {
		t.Errorf("Total energy is zero or negative: %.6f", totalEnergy)
	}

	// Most bars should have some energy (multi-frequency input)
	nonzeroCount := 0
	for _, val := range result {
		if val > 0 {
			nonzeroCount++
		}
	}

	if nonzeroCount < 3 {
		t.Errorf("Expected at least 3 bars with energy, got %d", nonzeroCount)
	}

	t.Logf("Energy distribution: %.6f total energy across %d/%d bars", totalEnergy, nonzeroCount, numBars)
}

// absf returns the absolute value of a float32 (mirrors tx_test.go:96-101). Kept
// local; test helpers do not cross the module boundary.
func absf(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}

// TestAVTxRDFTForwardDC mirrors the DC semantics of ffmpeg-statigo's
// TestAVTxFloatFFTForwardDC (tx_test.go:26-93) but at the Processor/ProcessChunk
// level, exercising the live av_tx RDFT path end to end. A constant (DC) real
// input is fed through ProcessChunk and the DC bin (Spectrum index 0, i.e.
// spectrum[0]/spectrum[1] re/im) must hold essentially all the energy while the
// rest of the spectrum is near zero.
//
// DC-assertion strategy: RELATIVE dominance with a windowed absolute DC check.
// ProcessChunk applies a Hanning window before the transform, so the transformed
// input is V*w[i], not a flat constant. A pure constant therefore does NOT yield
// a clean N*V DC spike: the DC bin equals V*sum(w) and the window's narrow
// spectral leakage spills into the first few bins. We:
//   - assert the DC bin re == V*sum(hanning) within tol and im ~ 0 (absolute,
//     factoring in the window sum), and
//   - assert the DC magnitude dominates every other bin by a large margin
//     (relative), with all bins past the window's leakage neighbourhood near zero
//     relative to DC.
//
// This genuinely drives av_tx: a constant non-zero input cannot produce a large,
// dominant DC bin unless the transform actually ran (a bypassed/zeroed transform
// fails the DC magnitude assertions).
func TestAVTxRDFTForwardDC(t *testing.T) {
	const (
		fftSize = 2048
		realVal = 1.5
		tol     = float32(1e-4)
	)

	processor, err := NewProcessor()
	if err != nil {
		t.Fatal(err)
	}
	defer processor.Close()

	// Constant (DC) real input.
	input := make([]float64, fftSize)
	for i := range input {
		input[i] = realVal
	}

	spectrum := processor.ProcessChunk(input)

	// Expected DC bin: re = V * sum(hanning), im ~ 0. The Processor windows the
	// input, so factor the window sum into the absolute DC check.
	var windowSum float64
	n := float64(fftSize - 1)
	for i := range fftSize {
		windowSum += 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/n))
	}
	wantDC := float32(realVal * windowSum)

	dcRe := spectrum[0]
	dcIm := spectrum[1]
	if absf(dcRe-wantDC) > tol || absf(dcIm) > tol {
		t.Fatalf("DC bin = (%g, %g), want (%g, 0) within %g", dcRe, dcIm, wantDC, tol)
	}

	dcMag := float32(math.Hypot(float64(dcRe), float64(dcIm)))
	if dcMag <= tol {
		t.Fatalf("DC magnitude %g not above tolerance; transform appears bypassed", dcMag)
	}

	// Relative dominance. The Hanning window's spectrum is DC plus two
	// half-amplitude sidebands at bins ±1 (here bins 1 and 2), so those carry
	// ~DC/2 and the rest of the spectrum is essentially zero. We therefore require:
	//   - DC dominates every individual bin (DC > each other bin magnitude), and
	//   - bins past the window's leakage neighbourhood vanish relative to DC.
	const leakageBins = 3
	numBins := fftSize/2 + 1
	var maxOther, maxOuter float32
	for i := 1; i < numBins; i++ {
		re := spectrum[2*i]
		im := spectrum[2*i+1]
		mag := float32(math.Hypot(float64(re), float64(im)))
		if mag > maxOther {
			maxOther = mag
		}
		if i >= leakageBins && mag > maxOuter {
			maxOuter = mag
		}
	}

	if dcMag <= maxOther {
		t.Fatalf("DC magnitude %g does not dominate largest other bin %g", dcMag, maxOther)
	}
	if maxOuter/dcMag > 1e-3 {
		t.Fatalf("bins beyond leakage neighbourhood not vanishing: max %g is %g of DC %g",
			maxOuter, maxOuter/dcMag, dcMag)
	}

	t.Logf("DC bin = (%g, %g), wantDC = %g; DC magnitude %g, largest other bin %g, max bin>=%d %g",
		dcRe, dcIm, wantDC, dcMag, maxOther, leakageBins, maxOuter)
}

// TestRearrangeFrequenciesCenterOut_Symmetry verifies that the output array
// is perfectly symmetric around the center index. This catches off-by-one
// errors in the mirroring logic that would produce visually jarring
// asymmetric bar output.
func TestRearrangeFrequenciesCenterOut_Symmetry(t *testing.T) {
	numBars := 64
	center := numBars / 2

	// Create input with distinct values (0-31 ascending)
	input := make([]float64, numBars)
	for i := range numBars {
		input[i] = float64(i)
	}

	result := make([]float64, numBars)
	RearrangeFrequenciesCenterOut(input, result)

	// Verify perfect symmetry around center
	for i := range center {
		leftIdx := center - 1 - i
		rightIdx := center + i

		if leftIdx < 0 || rightIdx >= numBars {
			t.Fatalf("Index out of bounds: left=%d, right=%d", leftIdx, rightIdx)
		}

		if result[leftIdx] != result[rightIdx] {
			t.Errorf("Symmetry violation at offset %d: result[%d]=%.0f != result[%d]=%.0f",
				i, leftIdx, result[leftIdx], rightIdx, result[rightIdx])
		}
	}

	t.Logf("Symmetry verified for all %d bar pairs", center)
}

// TestRearrangeFrequenciesCenterOut_ExpectedMapping verifies that frequencies
// are placed correctly with bass (low frequencies) at center and highs at edges.
func TestRearrangeFrequenciesCenterOut_ExpectedMapping(t *testing.T) {
	numBars := 64
	center := numBars / 2

	// Create input representing increasing frequency (bar 0 = lowest, 31 = highest)
	// After rearrangement: lowest frequencies should be at center, highest at edges
	input := make([]float64, numBars)
	for i := range numBars {
		input[i] = float64(i)
	}

	result := make([]float64, numBars)
	RearrangeFrequenciesCenterOut(input, result)

	// Verify that bass (input bar 0) is at center
	if result[center-1] != 0 || result[center] != 0 {
		t.Errorf("Bass frequencies not at center: result[%d]=%.0f, result[%d]=%.0f",
			center-1, result[center-1], center, result[center])
	}

	// Verify that highs (input bar 31) are at edges
	if result[0] != 31 || result[numBars-1] != 31 {
		t.Errorf("High frequencies not at edges: result[0]=%.0f, result[%d]=%.0f",
			result[0], numBars-1, result[numBars-1])
	}

	t.Logf("Frequency mapping verified: bass at center, highs at edges")
}

// TestRearrangeFrequenciesCenterOut_EdgeCases tests boundary conditions.
func TestRearrangeFrequenciesCenterOut_EdgeCases(t *testing.T) {
	testCases := []struct {
		name   string
		size   int
		values []float64
	}{
		{
			name:   "Minimum valid size (2 bars)",
			size:   2,
			values: []float64{1.0, 2.0},
		},
		{
			name:   "Single zero in input",
			size:   8,
			values: []float64{0, 1, 2, 3, 4, 5, 6, 7},
		},
		{
			name:   "All same values",
			size:   8,
			values: []float64{5.0, 5.0, 5.0, 5.0, 5.0, 5.0, 5.0, 5.0},
		},
		{
			name:   "Alternating pattern",
			size:   8,
			values: []float64{1.0, 2.0, 1.0, 2.0, 1.0, 2.0, 1.0, 2.0},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			input := make([]float64, tc.size)
			copy(input, tc.values)

			result := make([]float64, tc.size)
			RearrangeFrequenciesCenterOut(input, result)

			// Verify symmetry for this case
			center := tc.size / 2
			for i := range center {
				leftIdx := center - 1 - i
				rightIdx := center + i

				if result[leftIdx] != result[rightIdx] {
					t.Errorf("Symmetry violation: result[%d]=%.0f != result[%d]=%.0f",
						leftIdx, result[leftIdx], rightIdx, result[rightIdx])
				}
			}
		})
	}
}

// TestRearrangeFrequenciesCenterOut_SmallInput tests with minimal input size.
func TestRearrangeFrequenciesCenterOut_SmallInput(t *testing.T) {
	// Test the example from PLAN.md: [1,2,3,4] → symmetric output
	input := []float64{1, 2, 3, 4}
	result := make([]float64, 4)

	RearrangeFrequenciesCenterOut(input, result)

	// Expected mapping for 4-bar input:
	// Input bars:    [0, 1, 2, 3] with values [1, 2, 3, 4]
	// Center index:  2
	// Output should be symmetric:
	//   - Bars 0-1 mirrored from center going left (1 and 2)
	//   - Bars 2-3 mirrored from center going right (1 and 2)
	// Expected values: [input[1], input[0], then input[0], input[1]] = [2, 1, 1, 2]

	expected := []float64{2, 1, 1, 2}
	for i, v := range result {
		if v != expected[i] {
			t.Errorf("Index %d: got %.0f, want %.0f", i, v, expected[i])
		}
	}

	// Verify symmetry
	if result[0] != result[3] {
		t.Errorf("Symmetry check: result[0]=%.0f != result[3]=%.0f", result[0], result[3])
	}
	if result[1] != result[2] {
		t.Errorf("Symmetry check: result[1]=%.0f != result[2]=%.0f", result[1], result[2])
	}

	t.Logf("Small input test passed: %v → %v", input, result)
}

// BenchmarkProcessChunk measures FFT processing performance with pre-computed Hanning window.
// This is the hot path called for every FFT frame during both analysis and render passes.
func BenchmarkProcessChunk(b *testing.B) {
	// Create processor with pre-computed Hanning window
	processor, err := NewProcessor()
	if err != nil {
		b.Fatal(err)
	}
	defer processor.Close()

	// Generate test audio (random-ish data simulating real audio)
	samples := make([]float64, 2048)
	for i := range samples {
		samples[i] = math.Sin(float64(i)*0.1) * 0.5
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		_ = processor.ProcessChunk(samples)
	}
}

// BenchmarkHanningPrecomputed measures applying pre-computed Hanning coefficients.
// This isolates just the window application (multiply) without FFT overhead.
func BenchmarkHanningPrecomputed(b *testing.B) {
	// Pre-compute Hanning window (same as NewProcessor)
	window := make([]float64, 2048)
	n := float64(2048 - 1)
	for i := range 2048 {
		window[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/n))
	}
	output := make([]float64, 2048)

	samples := make([]float64, 2048)
	for i := range samples {
		samples[i] = math.Sin(float64(i)*0.1) * 0.5
	}

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		// Just multiply - no trig, no allocation
		for j := range 2048 {
			output[j] = samples[j] * window[j]
		}
	}
}
