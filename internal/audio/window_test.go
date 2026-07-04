package audio

import (
	"slices"
	"testing"
)

// ramp returns [start, start+1, ..., start+n-1] as float64s.
func ramp(start, n int) []float64 {
	s := make([]float64, n)
	for i := range s {
		s[i] = float64(start + i)
	}
	return s
}

func TestSlideFFTWindow(t *testing.T) {
	t.Run("normal slide", func(t *testing.T) {
		fftBuffer := ramp(0, 8)
		newSamples := ramp(100, 3)
		SlideFFTWindow(fftBuffer, newSamples, 3)
		want := []float64{3, 4, 5, 6, 7, 100, 101, 102}
		if !slices.Equal(fftBuffer, want) {
			t.Errorf("got %v, want %v", fftBuffer, want)
		}
	})

	t.Run("short read zero-pads tail", func(t *testing.T) {
		fftBuffer := ramp(0, 8)
		newSamples := ramp(100, 3)
		SlideFFTWindow(fftBuffer, newSamples, 1)
		want := []float64{3, 4, 5, 6, 7, 100, 0, 0}
		if !slices.Equal(fftBuffer, want) {
			t.Errorf("got %v, want %v", fftBuffer, want)
		}
	})

	t.Run("samplesPerFrame >= FFTSize", func(t *testing.T) {
		// 96 kHz at 30 FPS gives 3200 samples per frame with FFTSize 2048.
		const fftSize, samplesPerFrame = 2048, 3200

		fftBuffer := make([]float64, fftSize)
		newSamples := ramp(0, samplesPerFrame)
		SlideFFTWindow(fftBuffer, newSamples, samplesPerFrame)
		want := ramp(samplesPerFrame-fftSize, fftSize)
		if !slices.Equal(fftBuffer, want) {
			t.Errorf("full read: window is not the last %d samples", fftSize)
		}

		// Short read below fftSize zero-pads the tail.
		nRead := 100
		SlideFFTWindow(fftBuffer, newSamples, nRead)
		if !slices.Equal(fftBuffer[:nRead], newSamples[:nRead]) {
			t.Errorf("short read: head does not match new samples")
		}
		for i := nRead; i < fftSize; i++ {
			if fftBuffer[i] != 0 {
				t.Fatalf("short read: fftBuffer[%d] = %v, want 0", i, fftBuffer[i])
			}
		}
	})
}
