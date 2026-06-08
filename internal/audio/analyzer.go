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

	// Bar-scaling factor derived from GlobalPeak (see AnalyzeAudio).
	OptimalBaseScale float64

	// Audio metadata
	SampleRate int
	Duration   float64 // Seconds
}

// ProgressCallback is called with progress updates during analysis.
type ProgressCallback func(frame int, currentRMS, currentPeak float64, barHeights []float64, duration time.Duration)

// AnalyzeAudio performs Pass 1: stream through audio and collect statistics.
func AnalyzeAudio(filename string, progressCb ProgressCallback) (*Profile, error) {
	reader, err := NewStreamingReader(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to open audio: %w", err)
	}
	defer reader.Close()

	// NumFrames and Duration are derived from the actual sample count below.
	profile := &Profile{
		SampleRate: reader.SampleRate(),
	}

	// Calculate frame size from the file's actual sample rate so each frame
	// maps to 1/FPS seconds of audio regardless of input rate.
	samplesPerFrame := reader.SampleRate() / config.FPS
	if samplesPerFrame <= 0 {
		return nil, fmt.Errorf("input sample rate too low for %d FPS: %d Hz", config.FPS, reader.SampleRate())
	}

	processor := NewProcessor()

	var sumRMS float64
	var maxPeak float64

	// Sliding buffer for FFT: we advance by samplesPerFrame but need FFTSize for FFT.
	fftBuffer := make([]float64, config.FFTSize)
	frameBuf := make([]float64, samplesPerFrame)

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
		// ProcessChunk reads fftBuffer in place (applying the pre-computed Hanning
		// window), so no intermediate copy is needed.
		coeffs := processor.ProcessChunk(fftBuffer)

		analysis := analyzeFrame(coeffs, fftBuffer, barHeights)

		if analysis.PeakMagnitude > maxPeak {
			maxPeak = analysis.PeakMagnitude
		}
		sumRMS += analysis.RMSLevel

		frameNum++

		// Throttle progress callbacks to every third frame.
		if progressCb != nil && frameNum%3 == 0 {
			elapsed := time.Since(startTime)
			progressCb(frameNum, analysis.RMSLevel, analysis.PeakMagnitude, barHeights, elapsed)
		}

		nRead, err := ReadNextFrame(reader, frameBuf)
		if err != nil {
			if errors.Is(err, io.EOF) {
				if progressCb != nil {
					elapsed := time.Since(startTime)
					progressCb(frameNum, analysis.RMSLevel, analysis.PeakMagnitude, barHeights, elapsed)
				}
				break
			}
			return nil, fmt.Errorf("error reading audio at frame %d: %w", frameNum, err)
		}

		// Shift buffer left by samplesPerFrame, append new samples.
		copy(fftBuffer, fftBuffer[samplesPerFrame:])
		copy(fftBuffer[config.FFTSize-samplesPerFrame:], frameBuf[:nRead])
		if nRead < samplesPerFrame {
			clear(fftBuffer[config.FFTSize-samplesPerFrame+nRead:])
		}
	}

	// Duration tracks the number of frames advanced, not total samples read; each
	// frame represents samplesPerFrame of audio.
	profile.NumFrames = frameNum
	profile.Duration = float64(frameNum*samplesPerFrame) / float64(reader.SampleRate())

	profile.GlobalPeak = maxPeak
	profile.GlobalRMS = sumRMS / float64(profile.NumFrames)

	if profile.GlobalRMS > 0 {
		profile.DynamicRange = profile.GlobalPeak / profile.GlobalRMS
	} else {
		profile.DynamicRange = 0
	}

	// Choose baseScale so GlobalPeak maps to ~0.85 in normalised space, given the
	// render formula scaled = magnitude * baseScale * sensitivity at sensitivity 1.
	if profile.GlobalPeak > 0 {
		profile.OptimalBaseScale = 0.85 / profile.GlobalPeak
	} else {
		// Fall back to the original hardcoded value when no audio is detected.
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
