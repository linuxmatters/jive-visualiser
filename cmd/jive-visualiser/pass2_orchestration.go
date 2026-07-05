package main

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/linuxmatters/jive-visualiser/internal/audio"
	"github.com/linuxmatters/jive-visualiser/internal/config"
	"github.com/linuxmatters/jive-visualiser/internal/encoder"
	"github.com/linuxmatters/jive-visualiser/internal/renderer"
)

// pass2Config groups the encoding and timing parameters for runPass2 so the
// call site uses named fields and transposed arguments can't compile silently.
type pass2Config struct {
	inputFile         string
	outputFile        string
	channels          int
	noPreview         bool
	hwAccel           encoder.HWAccelType
	runtimeConfig     *config.RuntimeConfig
	meta              renderer.PodcastMeta
	thumbnailDuration time.Duration
	overallStartTime  time.Time
}

// expandMonoToStereo writes n mono samples from src into dst as interleaved
// L,R pairs (each mono sample duplicated to both channels). dst must hold at
// least 2*n elements.
func expandMonoToStereo(dst []float32, src []float64, n int) {
	for i := range n {
		s := float32(src[i])
		dst[i*2] = s
		dst[i*2+1] = s
	}
}

// convertAndWriteAudio converts n mono float64 samples from src to float32 via
// the pre-allocated buffers (duplicating each sample into interleaved L,R
// pairs when stereo) and writes them with write. When stereo, stereoBuf must
// hold at least 2*n elements (monoBuf is unused and may be nil); otherwise
// monoBuf must hold at least n (stereoBuf is unused and may be nil).
func convertAndWriteAudio(write func([]float32) error, src []float64, n int, stereo bool, monoBuf, stereoBuf []float32) error {
	if stereo {
		expandMonoToStereo(stereoBuf, src, n)
		return write(stereoBuf[:n*2])
	}
	for i := range n {
		monoBuf[i] = float32(src[i])
	}
	return write(monoBuf[:n])
}

// audioConvBufLen returns the length the audio conversion buffers need: they
// must hold the whole FFT prefill (config.FFTSize samples), which exceeds
// samplesPerFrame at common sample rates; at high rates samplesPerFrame is
// the larger of the two.
func audioConvBufLen(samplesPerFrame int) int {
	return max(samplesPerFrame, config.FFTSize)
}

// runPass2 collects any non-fatal warnings during rendering (e.g. an asset that
// failed to load and was dropped) and delivers them on the RenderComplete
// message so the caller can print them after the Bubbletea alt screen exits.
func runPass2(p *tea.Program, profile *audio.Profile, cfg pass2Config) {
	runner := newPass2Runner(p, profile, cfg)

	if err := runner.setupReader(); err != nil {
		runner.fail(err)
		return
	}
	defer runner.reader.Close()

	if err := runner.setupEncoder(); err != nil {
		runner.fail(err)
		return
	}

	// Close strategy: the encoder is closed exactly once, here, on every path
	// (success and error). reachedEnd records whether rendering finished. On an
	// error path the deferred Close only frees resources; the failure was
	// already reported. On the success path a Close failure is fatal (a dropped
	// trailer truncates the file), so it is surfaced in place of sendComplete.
	reachedEnd := false
	defer func() {
		closeErr := runner.closeEncoder()
		if !reachedEnd {
			return
		}
		if closeErr != nil {
			runner.fail(closeErr)
			return
		}
		runner.sendComplete()
	}()

	// Asset load failures are non-fatal: nil assets degrade gracefully, but warn
	// so dropped assets are not silent.
	runner.loadAssets()

	if err := runner.setupProcessorAndFrame(); err != nil {
		runner.fail(err)
		return
	}
	defer runner.processor.Close()

	runner.setupTimingAndDisplay()
	const progressUpdateInterval = 30 * time.Millisecond

	runner.setupRenderState()

	// Sliding buffer for FFT: we read samplesPerFrame but need FFTSize for FFT.
	// Derive from the file's actual sample rate so encoded audio and video
	// durations stay aligned for any input rate.
	// The reader downmixes to mono. For stereo output the encoder expects
	// interleaved L,R pairs, so duplicate each mono sample into both channels via
	// this pre-allocated buffer (no per-frame allocation).
	runner.setupAudioBuffers()

	// Write the whole prefill to the encoder. FillFFTBuffer consumed n samples
	// from the reader, so all n must reach the encoder or that audio is lost
	// (truncating to samplesPerFrame dropped ~13 ms at 44.1 kHz). The FIFO in
	// the encoder absorbs the surplus beyond frame 0. Reuse the conversion
	// buffers: WriteAudioSamples copies into the FIFO and retains no reference,
	// and the buffers are overwritten before each later use in the render loop.
	if err := runner.prefillFFT(); err != nil {
		runner.fail(err)
		return
	}

	// Process frames until we run out of audio
	frameNum := 0
	for frameNum < runner.numFrames {
		img := runner.renderFrame()
		if err := runner.writeVideoFrame(frameNum, img); err != nil {
			runner.fail(err)
			return
		}

		runner.sendProgressIfDue(frameNum, img, progressUpdateInterval)

		frameNum++

		hasAudio, err := runner.processNextAudioFrame(frameNum)
		if err != nil {
			runner.fail(err)
			return
		}
		if !hasAudio {
			break
		}
	}

	// Flush samples still in the FIFO after the last video frame is written.
	if err := runner.enc.FlushAudioEncoder(); err != nil {
		runner.fail(fmt.Errorf("flushing audio: %w", err))
		return
	}

	// The deferred close finalises the encoder and, on success, reports
	// completion or a fatal Close error.
	reachedEnd = true
}
