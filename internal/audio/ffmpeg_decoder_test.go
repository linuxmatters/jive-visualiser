package audio

import (
	"math"
	"testing"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

func TestDecodeS16(t *testing.T) {
	tests := []struct {
		name string
		buf  []byte
		want float64
	}{
		{"zero", []byte{0x00, 0x00}, 0.0},
		{"max positive", []byte{0xFF, 0x7F}, float64(math.MaxInt16) / 32768.0},
		{"max negative", []byte{0x00, 0x80}, -1.0},
		{"one", []byte{0x01, 0x00}, 1.0 / 32768.0},
		{"minus one", []byte{0xFF, 0xFF}, -1.0 / 32768.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decodeS16(tt.buf, 0)
			if math.Abs(got-tt.want) > 1e-10 {
				t.Errorf("decodeS16() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDecodeS16WithOffset(t *testing.T) {
	// Sample at offset 2: little-endian 0x0100 = 256
	buf := []byte{0xAA, 0xBB, 0x00, 0x01}
	got := decodeS16(buf, 2)
	want := float64(256) / 32768.0
	if math.Abs(got-want) > 1e-10 {
		t.Errorf("decodeS16(offset=2) = %v, want %v", got, want)
	}
}

func TestDecodeS32(t *testing.T) {
	tests := []struct {
		name string
		buf  []byte
		want float64
	}{
		{"zero", []byte{0x00, 0x00, 0x00, 0x00}, 0.0},
		{"max positive", []byte{0xFF, 0xFF, 0xFF, 0x7F}, float64(math.MaxInt32) / 2147483648.0},
		{"max negative", []byte{0x00, 0x00, 0x00, 0x80}, -1.0},
		{"one", []byte{0x01, 0x00, 0x00, 0x00}, 1.0 / 2147483648.0},
		{"minus one", []byte{0xFF, 0xFF, 0xFF, 0xFF}, -1.0 / 2147483648.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decodeS32(tt.buf, 0)
			if math.Abs(got-tt.want) > 1e-10 {
				t.Errorf("decodeS32() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDecodeF32(t *testing.T) {
	tests := []struct {
		name string
		buf  []byte
		want float64
	}{
		{"zero", []byte{0x00, 0x00, 0x00, 0x00}, 0.0},
		{"one", []byte{0x00, 0x00, 0x80, 0x3F}, 1.0},
		{"minus one", []byte{0x00, 0x00, 0x80, 0xBF}, -1.0},
		{"half", []byte{0x00, 0x00, 0x00, 0x3F}, 0.5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := decodeF32(tt.buf, 0)
			if math.Abs(got-tt.want) > 1e-6 {
				t.Errorf("decodeF32() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDecodeF32WithOffset(t *testing.T) {
	// 1.0f at offset 4
	buf := []byte{0xDE, 0xAD, 0xBE, 0xEF, 0x00, 0x00, 0x80, 0x3F}
	got := decodeF32(buf, 4)
	if math.Abs(got-1.0) > 1e-6 {
		t.Errorf("decodeF32(offset=4) = %v, want 1.0", got)
	}
}

func TestSampleDecoder(t *testing.T) {
	validFormats := []ffmpeg.AVSampleFormat{
		ffmpeg.AVSampleFmtS16,
		ffmpeg.AVSampleFmtS32,
		ffmpeg.AVSampleFmtFlt,
		ffmpeg.AVSampleFmtS16P,
		ffmpeg.AVSampleFmtS32P,
		ffmpeg.AVSampleFmtFltp,
	}
	for _, format := range validFormats {
		fn, bps, err := sampleDecoder(format)
		if err != nil {
			t.Errorf("sampleDecoder(%d) returned error: %v", format, err)
		}
		if fn == nil {
			t.Errorf("sampleDecoder(%d) returned nil function", format)
		}
		if bps == 0 {
			t.Errorf("sampleDecoder(%d) returned 0 bps", format)
		}
	}

	_, _, err := sampleDecoder(99)
	if err == nil {
		t.Error("sampleDecoder(99) should return error")
	}
}
