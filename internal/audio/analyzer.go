package audio

import (
	"errors"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/linuxmatters/jivefire/internal/config"
)

// FrameAnalysis holds statistics for a single frame.
type FrameAnalysis struct {
	// Peak FFT magnitude across all bars
	PeakMagnitude float64

	// RMS level of audio chunk
	RMSLevel float64
}

// Profile holds complete audio analysis results.
type Profile struct {
	// Total number of frames in audio
	NumFrames int

	// Global statistics
	GlobalPeak   float64 // Highest peak magnitude across all frames
	GlobalRMS    float64 // Average RMS across all frames
	DynamicRange float64 // Ratio of GlobalPeak to GlobalRMS

	// Calculated optimal parameters
	OptimalBaseScale float64 // Replaces hardcoded 0.0075

	// Audio metadata
	SampleRate int
	Duration   float64 // Seconds
}

// ProgressCallback is called with progress updates during analysis.
type ProgressCallback func(frame, totalFrames int, currentRMS, currentPeak float64, barHeights []float64, duration time.Duration)

// AnalyzeAudio performs Pass 1: stream through audio and collect statistics.
func AnalyzeAudio(filename string, progressCb ProgressCallback) (*Profile, error) {
	// Open streaming reader
	reader, err := NewStreamingReader(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open audio: %w", err)
	}
	defer reader.Close()

	profile := &Profile{
		NumFrames:  0, // Will be set after we count actual samples
		SampleRate: reader.SampleRate(),
		Duration:   0, // Will be calculated from actual sample count
	}

	// Calculate frame size
	samplesPerFrame := config.SampleRate / config.FPS

	// Create FFT processor
	processor := NewProcessor()

	// Analyze each frame
	var sumRMS float64
	var maxPeak float64

	// Sliding buffer for FFT: we advance by samplesPerFrame but need FFTSize for FFT
	fftBuffer := make([]float64, config.FFTSize)
	frameBuf := make([]float64, samplesPerFrame)

	// Pre-fill buffer with first chunk
	n, err := FillFFTBuffer(reader, fftBuffer)
	if err != nil {
		return nil, fmt.Errorf("error reading initial chunk: %w", err)
	}
	if n == 0 {
		return nil, fmt.Errorf("no audio data in file")
	}

	// Pre-allocate bar magnitudes buffer for progress callbacks
	barHeights := make([]float64, config.NumBars)

	startTime := time.Now()
	frameNum := 0

	for {
		// Pass fftBuffer directly to ProcessChunk - it applies the pre-computed Hanning window
		// No need for intermediate allocation since analyzeFrame only reads the buffer
		coeffs := processor.ProcessChunk(fftBuffer)

		// Analyze frequency bins
		analysis := analyzeFrame(coeffs, fftBuffer, barHeights)

		// Track global statistics
		if analysis.PeakMagnitude > maxPeak {
			maxPeak = analysis.PeakMagnitude
		}
		sumRMS += analysis.RMSLevel

		frameNum++

		// Send progress update via callback (throttle to every 3 frames for performance)
		if progressCb != nil && frameNum%3 == 0 {
			elapsed := time.Since(startTime)
			// No total frames estimate available during first pass
			progressCb(frameNum, 0, analysis.RMSLevel, analysis.PeakMagnitude, barHeights, elapsed)
		}

		// Advance sliding buffer for next frame
		nRead, err := ReadNextFrame(reader, frameBuf)
		if err != nil {
			if errors.Is(err, io.EOF) {
				// Send final progress update
				if progressCb != nil {
					elapsed := time.Since(startTime)
					progressCb(frameNum, frameNum, analysis.RMSLevel, analysis.PeakMagnitude, barHeights, elapsed)
				}
				break
			}
			return nil, fmt.Errorf("error reading audio at frame %d: %w", frameNum, err)
		}

		// Shift buffer left by samplesPerFrame, append new samples
		copy(fftBuffer, fftBuffer[samplesPerFrame:])
		copy(fftBuffer[config.FFTSize-samplesPerFrame:], frameBuf[:nRead])
		if nRead < samplesPerFrame {
			clear(fftBuffer[config.FFTSize-samplesPerFrame+nRead:])
		}
	}

	// Set actual frame count and duration
	// Duration is based on the number of frames, not total samples read
	// Each frame represents samplesPerFrame of actual audio advancement
	profile.NumFrames = frameNum
	profile.Duration = float64(frameNum*samplesPerFrame) / float64(reader.SampleRate())

	// Calculate global statistics
	profile.GlobalPeak = maxPeak
	profile.GlobalRMS = sumRMS / float64(profile.NumFrames)

	// Avoid division by zero
	if profile.GlobalRMS > 0 {
		profile.DynamicRange = profile.GlobalPeak / profile.GlobalRMS
	} else {
		profile.DynamicRange = 0
	}

	// Calculate optimal baseScale
	// Goal: GlobalPeak should map to ~0.8-0.9 in normalized space
	// Current formula: scaled = magnitude * baseScale * sensitivity
	// We want: GlobalPeak * baseScale * 1.0 ≈ 0.85
	if profile.GlobalPeak > 0 {
		profile.OptimalBaseScale = 0.85 / profile.GlobalPeak
	} else {
		// Fallback to original hardcoded value if no audio detected
		profile.OptimalBaseScale = 0.0075
	}

	return profile, nil
}

// analyzeFrame extracts statistics from FFT coefficients and audio chunk.
// barMagnitudes is an optional buffer that receives per-bar average magnitudes
// for progress display; pass nil when bar magnitudes are not needed.
func analyzeFrame(coeffs []complex128, audioChunk []float64, barMagnitudes []float64) FrameAnalysis {
	analysis := FrameAnalysis{}

	// Calculate RMS of audio chunk
	var sumSquares float64
	for _, sample := range audioChunk {
		sumSquares += sample * sample
	}
	analysis.RMSLevel = math.Sqrt(sumSquares / float64(len(audioChunk)))

	// Bin frequencies into per-bar raw average magnitudes (shared with BinFFT).
	// Write into the caller's buffer when supplied; otherwise use a local scratch.
	bins := barMagnitudes
	if bins == nil {
		bins = make([]float64, config.NumBars)
	}
	binRawMagnitudes(coeffs, bins)

	// Track peak across raw bar magnitudes
	for _, avgMagnitude := range bins {
		if avgMagnitude > analysis.PeakMagnitude {
			analysis.PeakMagnitude = avgMagnitude
		}
	}

	return analysis
}
