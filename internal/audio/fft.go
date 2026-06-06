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
	// Use only first half (positive frequencies)
	halfSize := len(coeffs) / 2
	maxFreqBin := halfSize
	binsPerBar := maxFreqBin / config.NumBars

	for bar := range config.NumBars {
		start := bar * binsPerBar
		end := start + binsPerBar
		end = min(end, maxFreqBin)

		// Average magnitude in this range
		var sum float64
		for i := start; i < end; i++ {
			magnitude := cmplx.Abs(coeffs[i])
			sum += magnitude
		}

		result[bar] = sum / float64(binsPerBar)
	}
}

// BinFFT bins FFT coefficients into bars and returns normalized values (0.0-1.0)
// CAVA-style approach: work in normalized space, apply maxBarHeight scaling later
// baseScale is calculated from Pass 1 analysis for optimal visualization
// result buffer is provided by caller to avoid allocations
func BinFFT(coeffs []complex128, sensitivity float64, baseScale float64, result []float64) {
	// Raw per-bar average magnitudes; normalisation is applied on top below
	binRawMagnitudes(coeffs, result)

	// CAVA-style processing: apply sensitivity, then normalize to 0.0-1.0 range
	// baseScale provided from Pass 1 analysis: OptimalBaseScale = 0.85 / GlobalPeak

	for i := range result {
		// Apply sensitivity to raw magnitude
		scaled := result[i] * baseScale * sensitivity

		// Noise gate on raw values (before log scale)
		if scaled < 0.01 {
			result[i] = 0
		} else {
			// Log scale for better visual distribution, normalize to ~0.0-1.0
			// Log10(1 + scaled*9) gives range [0, 1] for scaled in [0, 1]
			// We scale up for better dynamic range
			result[i] = math.Log10(1 + scaled*9)

			// DON'T clip here - let overshoot detection in main loop handle it
			// This allows sensitivity adjustment to detect actual overshoots
		}
	}
}

// RearrangeFrequenciesCenterOut creates a symmetric mirror pattern with most active frequencies at CENTER
// result buffer is provided by caller to avoid allocations
func RearrangeFrequenciesCenterOut(barHeights []float64, result []float64) {
	// Left side: frequencies 0→31 placed from CENTER → LEFT EDGE (most active at center)
	// Right side: frequencies 0→31 mirrored from CENTER → RIGHT EDGE
	// Result: Most active (bass) at center, less active (highs) at edges

	n := len(barHeights)
	center := n / 2

	// Place first half of frequencies mirrored from center outward
	for i := 0; i < n/2; i++ {
		// Left side: place from center going left (most active near center)
		result[center-1-i] = barHeights[i]
		// Right side: mirror (most active near center)
		result[center+i] = barHeights[i]
	}
}

// Processor handles FFT analysis for visualization
type Processor struct {
	// Pre-computed Hanning window coefficients (avoids trig per sample)
	hanningWindow []float64
	// Reusable buffer for windowed output (avoids allocation per ProcessChunk)
	windowedBuffer []float64
}

// NewProcessor creates a new audio processor with pre-computed Hanning window
func NewProcessor() *Processor {
	// Pre-compute Hanning window coefficients once
	window := make([]float64, config.FFTSize)
	n := float64(config.FFTSize - 1)
	for i := range config.FFTSize {
		window[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/n))
	}
	return &Processor{
		hanningWindow:  window,
		windowedBuffer: make([]float64, config.FFTSize),
	}
}

// ProcessChunk performs FFT on a chunk of audio samples.
// Uses pre-computed Hanning window coefficients for better performance.
func (p *Processor) ProcessChunk(samples []float64) []complex128 {
	// Pad if needed
	chunk := samples
	if len(chunk) < config.FFTSize {
		padded := make([]float64, config.FFTSize)
		copy(padded, chunk)
		chunk = padded
	}

	// Apply Hanning window using pre-computed coefficients (faster than trig per sample)
	for i := range config.FFTSize {
		p.windowedBuffer[i] = chunk[i] * p.hanningWindow[i]
	}

	// Convert to complex128 for gofft which works in-place, modifying the input array
	fftInput := gofft.Float64ToComplex128Array(p.windowedBuffer)

	// Compute FFT in-place
	err := gofft.FFT(fftInput)
	if err != nil {
		// Should never happen with power-of-2 size
		panic("FFT failed: " + err.Error())
	}

	return fftInput
}
