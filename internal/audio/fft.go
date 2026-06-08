package audio

import (
	"math"
	"math/cmplx"

	"github.com/argusdusty/gofft"
	"github.com/linuxmatters/jivefire/internal/config"
)

// binRawMagnitudes bins FFT coefficients into per-bar raw average magnitudes.
// It writes config.NumBars values into result. The spectrum runs up to the
// Nyquist frequency (~22kHz at 44.1kHz) to capture cymbals, hi-hats, and the
// musical "air" in stings and bumpers. Each bar averages cmplx.Abs over its
// frequency range, dividing by binsPerBar. Callers apply any normalisation on
// top of these raw values.
func binRawMagnitudes(coeffs []complex128, result []float64) {
	// Use only the first half of the spectrum (positive frequencies).
	halfSize := len(coeffs) / 2
	maxFreqBin := halfSize
	binsPerBar := maxFreqBin / config.NumBars

	for bar := range config.NumBars {
		start := bar * binsPerBar
		end := start + binsPerBar
		end = min(end, maxFreqBin)

		var sum float64
		for i := start; i < end; i++ {
			magnitude := cmplx.Abs(coeffs[i])
			sum += magnitude
		}

		result[bar] = sum / float64(binsPerBar)
	}
}

// BinFFT bins FFT coefficients into bars and writes normalised values (0.0-1.0)
// into the caller-provided result buffer. It works in normalised space (the
// maxBarHeight pixel scaling is applied later); baseScale comes from Pass 1
// analysis (OptimalBaseScale = 0.85 / GlobalPeak).
func BinFFT(coeffs []complex128, sensitivity float64, baseScale float64, result []float64) {
	binRawMagnitudes(coeffs, result)

	for i := range result {
		scaled := result[i] * baseScale * sensitivity

		// Noise gate on the raw value, before the log scale.
		if scaled < 0.01 {
			result[i] = 0
		} else {
			// Log10(1 + scaled*9) maps scaled in [0,1] to ~[0,1] for better visual
			// dynamic range. Deliberately unclipped so the main loop's overshoot
			// detection can drive sensitivity.
			result[i] = math.Log10(1 + scaled*9)
		}
	}
}

// RearrangeFrequenciesCenterOut mirrors the bars symmetrically about the centre,
// writing into the caller-provided result buffer. Low frequencies (bass) land at
// the centre and high frequencies fan out to the edges, so a quieter input still
// looks balanced.
func RearrangeFrequenciesCenterOut(barHeights []float64, result []float64) {
	n := len(barHeights)
	center := n / 2

	for i := 0; i < n/2; i++ {
		result[center-1-i] = barHeights[i] // centre → left edge
		result[center+i] = barHeights[i]   // centre → right edge (mirror)
	}
}

// Processor handles FFT analysis for visualisation.
type Processor struct {
	// Pre-computed Hanning window coefficients (avoids trig per sample)
	hanningWindow []float64
	// Reusable complex buffer for the in-place FFT (avoids allocation per ProcessChunk)
	fftInput []complex128
}

// NewProcessor creates a new audio processor with a pre-computed Hanning window.
func NewProcessor() *Processor {
	window := make([]float64, config.FFTSize)
	n := float64(config.FFTSize - 1)
	for i := range config.FFTSize {
		window[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/n))
	}
	return &Processor{
		hanningWindow: window,
		fftInput:      make([]complex128, config.FFTSize),
	}
}

// ProcessChunk performs FFT on a chunk of audio samples.
// Uses pre-computed Hanning window coefficients for better performance.
// The returned slice is a buffer reused across calls; callers must fully
// consume it before the next ProcessChunk call.
func (p *Processor) ProcessChunk(samples []float64) []complex128 {
	// Clamp to the window size; short final chunks are zero-padded by the loop below.
	n := min(len(samples), config.FFTSize)

	// Apply the Hanning window and fold the real→complex conversion into the
	// same pass, writing directly into the reusable in-place FFT buffer.
	for i := range n {
		p.fftInput[i] = complex(samples[i]*p.hanningWindow[i], 0)
	}
	// Zero-pad any remainder so samples beyond the input are treated as silence.
	for i := n; i < config.FFTSize; i++ {
		p.fftInput[i] = 0
	}

	err := gofft.FFT(p.fftInput)
	if err != nil {
		// Cannot happen with a power-of-2 size.
		panic("FFT failed: " + err.Error())
	}

	return p.fftInput
}
