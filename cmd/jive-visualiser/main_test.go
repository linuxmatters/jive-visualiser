package main

import (
	"testing"

	"github.com/linuxmatters/jive-visualiser/internal/config"
)

// TestPrefillWritesWholeBuffer asserts the whole FFT prefill (all n samples
// FillFFTBuffer returned) reaches the encoder, not just one frame's worth.
// Truncating to samplesPerFrame dropped ~13 ms of audio at 44.1 kHz. It pins
// convertAndWriteAudio, the function the runPass2 call site uses, so a
// regression in that path fails the suite.
func TestPrefillWritesWholeBuffer(t *testing.T) {
	// 44.1 kHz: samplesPerFrame (1470) is smaller than the FFT prefill, the
	// case where truncation loses audio.
	convBufLen := audioConvBufLen(44100 / config.FPS)

	src := make([]float64, config.FFTSize)
	for i := range src {
		src[i] = float64(i) / float64(len(src))
	}

	cases := []struct {
		name   string
		stereo bool
		want   int
	}{
		{"mono", false, config.FFTSize},
		{"stereo", true, config.FFTSize * 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got int
			var gotLast float32
			write := func(s []float32) error {
				got = len(s)
				gotLast = s[len(s)-1]
				return nil
			}
			monoBuf := make([]float32, convBufLen)
			stereoBuf := make([]float32, convBufLen*2)
			if err := convertAndWriteAudio(write, src, config.FFTSize, tc.stereo, monoBuf, stereoBuf); err != nil {
				t.Fatalf("convertAndWriteAudio: %v", err)
			}
			if got != tc.want {
				t.Errorf("prefill wrote %d samples, want %d", got, tc.want)
			}
			if wantLast := float32(src[config.FFTSize-1]); gotLast != wantLast {
				t.Errorf("last written sample = %v, want %v", gotLast, wantLast)
			}
		})
	}
}

// TestAudioConvBufLen pins the conversion buffer sizing: the buffers must
// hold the whole FFT prefill, and grow with samplesPerFrame when that is the
// larger of the two.
func TestAudioConvBufLen(t *testing.T) {
	// 44.1 kHz: samplesPerFrame is 1470, below FFTSize.
	if got := audioConvBufLen(1470); got < config.FFTSize {
		t.Errorf("audioConvBufLen(1470) = %d, want at least %d", got, config.FFTSize)
	}
	// High sample rate: samplesPerFrame exceeds FFTSize.
	if got := audioConvBufLen(3200); got != 3200 {
		t.Errorf("audioConvBufLen(3200) = %d, want 3200", got)
	}
}
