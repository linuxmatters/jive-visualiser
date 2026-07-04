package audio

// SlideFFTWindow advances the FFT window by one video frame of audio.
// fftBuffer holds the FFT window (FFTSize samples) and newSamples holds one
// frame of audio (samplesPerFrame samples), of which nRead are valid.
//
// When a frame holds fewer samples than the FFT window, the window slides:
// the buffer shifts left by samplesPerFrame and the new samples append at
// the end. When a frame holds at least a full window (high sample rates),
// the window is replaced with the most recent FFTSize samples instead, so
// no out-of-range slice of fftBuffer is ever taken.
//
// Short final reads (nRead below the expected count) zero-pad the tail so
// stale samples never feed the FFT.
func SlideFFTWindow(fftBuffer, newSamples []float64, nRead int) {
	fftSize := len(fftBuffer)
	samplesPerFrame := len(newSamples)

	if samplesPerFrame >= fftSize {
		// Replace the window with the last fftSize valid samples.
		if nRead < fftSize {
			copy(fftBuffer, newSamples[:nRead])
			clear(fftBuffer[nRead:])
		} else {
			copy(fftBuffer, newSamples[nRead-fftSize:nRead])
		}
		return
	}

	// Shift the buffer left by samplesPerFrame and append the new samples,
	// zero-padding a short final read so stale samples never feed the FFT.
	copy(fftBuffer, fftBuffer[samplesPerFrame:])
	if nRead < samplesPerFrame {
		copy(fftBuffer[fftSize-samplesPerFrame:], newSamples[:nRead])
		clear(fftBuffer[fftSize-samplesPerFrame+nRead:])
	} else {
		copy(fftBuffer[fftSize-samplesPerFrame:], newSamples[:nRead])
	}
}
