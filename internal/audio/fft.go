package audio

import (
	"fmt"
	"math"
	"unsafe"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jive-visualiser/internal/config"
)

// avComplexFloat mirrors C's AVComplexFloat: two contiguous 32-bit floats with
// no padding (8 bytes). The generated ffmpeg.AVComplexFloat is an opaque pointer
// wrapper with no value fields, so reading the RDFT output buffer through this
// layout matches the on-wire struct av_tx writes, as tx_test.go does.
type avComplexFloat struct {
	re, im float32
}

// Spectrum is a flat float32 view of an RDFT forward (R2C) output: interleaved
// re/im pairs for the N/2+1 complex bins (length 2*(N/2+1)). Bin i lives at
// indices [2*i] (re) and [2*i+1] (im). ProcessChunk returns a Spectrum backed by
// a reused buffer; callers must consume it before the next ProcessChunk call.
type Spectrum []float32

// Compile-time assertion that the positive-frequency bin count (FFTSize/2)
// divides evenly by NumBars. binRawMagnitudes assumes every bar covers exactly
// binsPerBar bins; if the division ever truncated it would silently drop the
// spectrum tail. This declares an array whose length is negative when the
// remainder is non-zero, which fails to compile.
var _ [0 - (config.FFTSize/2)%config.NumBars]struct{}

// binRawMagnitudes bins FFT coefficients into per-bar raw average magnitudes.
// It writes config.NumBars values into result. The spectrum runs up to the
// Nyquist frequency (~22kHz at 44.1kHz) to capture cymbals, hi-hats, and the
// musical "air" in stings and bumpers. Each bar averages the per-bin magnitude
// (hypot of the re/im pair) over its frequency range, dividing by binsPerBar.
// Callers apply any normalisation on top of these raw values.
func binRawMagnitudes(spectrum Spectrum, result []float64) {
	// Bin only the positive-frequency half (bins 0 .. N/2-1); the Nyquist bin
	// (index N/2) is discarded, matching the pre-swap []complex128 behaviour.
	maxFreqBin := config.FFTSize / 2
	binsPerBar := maxFreqBin / config.NumBars

	for bar := range config.NumBars {
		start := bar * binsPerBar
		end := start + binsPerBar
		end = min(end, maxFreqBin)

		var sum float64
		for i := start; i < end; i++ {
			re := float64(spectrum[2*i])
			im := float64(spectrum[2*i+1])
			sum += math.Hypot(re, im)
		}

		result[bar] = sum / float64(binsPerBar)
	}
}

// BinFFT bins FFT coefficients into bars and writes normalised values (0.0-1.0)
// into the caller-provided result buffer. It works in normalised space (the
// maxBarHeight pixel scaling is applied later); baseScale comes from Pass 1
// analysis (OptimalBaseScale = 0.85 / GlobalPeak).
func BinFFT(spectrum Spectrum, sensitivity float64, baseScale float64, result []float64) {
	binRawMagnitudes(spectrum, result)

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
	// Reusable float32 spectrum buffer (interleaved re/im for N/2+1 bins),
	// returned by ProcessChunk to avoid allocation per call.
	spectrum Spectrum

	// av_tx RDFT lifecycle. All raw C state stays inside Processor so the
	// unsafe.Pointer/context boundary never leaks to consumers.
	ctx    *ffmpeg.AVTXContext // transform context from AVTxInit
	fn     ffmpeg.AVTxFn       // forward transform function pointer
	inBuf  unsafe.Pointer      // C buffer: config.FFTSize real float32 samples
	outBuf unsafe.Pointer      // C buffer: config.FFTSize/2+1 AVComplexFloat bins
}

// rdftRealStride is the byte stride of one real input sample (float32).
const rdftRealStride = int(unsafe.Sizeof(float32(0)))

// rdftComplexStride is the byte stride of one AVComplexFloat output bin: two
// contiguous 32-bit floats with no padding, matching C's AVComplexFloat (8 bytes).
const rdftComplexStride = 2 * int(unsafe.Sizeof(float32(0)))

// NewProcessor creates a new audio processor with a pre-computed Hanning window
// and an av_tx RDFT lifecycle. It returns an error if the C transform context or
// its buffers cannot be allocated.
func NewProcessor() (*Processor, error) {
	window := make([]float64, config.FFTSize)
	n := float64(config.FFTSize - 1)
	for i := range config.FFTSize {
		window[i] = 0.5 * (1 - math.Cos(2*math.Pi*float64(i)/n))
	}
	p := &Processor{
		hanningWindow: window,
		spectrum:      make(Spectrum, 2*(config.FFTSize/2+1)),
	}

	// scale, ctx, and fn are locals, not Processor fields, then copied into p.
	// cgo rejects a Go pointer into a struct that itself holds Go pointers (the
	// slices above): av_tx_init takes the addresses of ctx/fn (it writes the
	// transform function pointer through &fn) and reads &scale. scale needs no
	// lifetime beyond this call; ctx/fn are stored for the transform's lifetime.
	scale := float32(1.0)
	var ctx *ffmpeg.AVTXContext
	var fn ffmpeg.AVTxFn
	if _, err := ffmpeg.AVTxInit(&ctx, &fn, ffmpeg.AVTxFloatRdft, 0 /*forward*/, config.FFTSize, unsafe.Pointer(&scale), 0); err != nil {
		return nil, fmt.Errorf("av_tx_init (RDFT): %w", err)
	}
	p.ctx = ctx
	p.fn = fn

	// AVMalloc returns memory aligned to FFmpeg's max SIMD alignment (>= 32 bytes),
	// satisfying av_tx_fn's default requirement so AV_TX_UNALIGNED is not needed.
	// RDFT forward (R2C) input is a flat real array of config.FFTSize float32;
	// output is config.FFTSize/2+1 AVComplexFloat bins.
	inBytes := uint64(rdftRealStride * config.FFTSize)             //nolint:gosec // positive constants
	outBytes := uint64(rdftComplexStride * (config.FFTSize/2 + 1)) //nolint:gosec // positive constants
	p.inBuf = ffmpeg.AVMalloc(inBytes)
	if p.inBuf == nil {
		p.Close()
		return nil, fmt.Errorf("av_malloc RDFT input buffer (%d bytes) failed", inBytes)
	}
	p.outBuf = ffmpeg.AVMalloc(outBytes)
	if p.outBuf == nil {
		p.Close()
		return nil, fmt.Errorf("av_malloc RDFT output buffer (%d bytes) failed", outBytes)
	}

	return p, nil
}

// Close releases the av_tx context and C buffers. It is nil-guarded and
// idempotent, so a partially constructed Processor and repeated calls are safe.
func (p *Processor) Close() {
	if p.ctx != nil {
		// Pass a local to AVTxUninit: cgo rejects &p.ctx because Processor holds
		// Go pointers (the slices). The uninit nils the local; we mirror that on p.
		ctx := p.ctx
		ffmpeg.AVTxUninit(&ctx)
		p.ctx = nil
	}
	if p.inBuf != nil {
		ffmpeg.AVFree(p.inBuf)
		p.inBuf = nil
	}
	if p.outBuf != nil {
		ffmpeg.AVFree(p.outBuf)
		p.outBuf = nil
	}
}

// ProcessChunk performs FFT on a chunk of audio samples.
// Uses pre-computed Hanning window coefficients for better performance.
// The returned slice is a buffer reused across calls; callers must fully
// consume it before the next ProcessChunk call.
func (p *Processor) ProcessChunk(samples []float64) Spectrum {
	// Clamp to the window size; short final chunks are zero-padded by the loop below.
	n := min(len(samples), config.FFTSize)

	// Apply the Hanning window and write the windowed real samples directly into
	// the C input buffer as float32. RDFT forward (R2C) takes a flat real array.
	in := unsafe.Slice((*float32)(p.inBuf), config.FFTSize)
	for i := range n {
		in[i] = float32(samples[i] * p.hanningWindow[i])
	}
	// Zero-pad any remainder so samples beyond the input are treated as silence.
	for i := n; i < config.FFTSize; i++ {
		in[i] = 0
	}

	// Forward R2C transform: stride is one real sample in bytes.
	ffmpeg.AVTxCall(p.fn, p.ctx, p.outBuf, p.inBuf, rdftRealStride)

	// RDFT forward emits N/2+1 complex bins with DC at index 0, ascending in
	// frequency. Copy the re/im pairs straight into the interleaved float32
	// spectrum the consumers read; binRawMagnitudes bins indices 0 .. N/2-1 and
	// discards the Nyquist bin (index N/2).
	out := unsafe.Slice((*avComplexFloat)(p.outBuf), config.FFTSize/2+1)
	for i := range out {
		p.spectrum[2*i] = out[i].re
		p.spectrum[2*i+1] = out[i].im
	}

	return p.spectrum
}
