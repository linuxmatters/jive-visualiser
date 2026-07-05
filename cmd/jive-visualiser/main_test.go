package main

import (
	"image"
	"os"
	"testing"
	"time"

	"github.com/linuxmatters/jive-visualiser/internal/audio"
	"github.com/linuxmatters/jive-visualiser/internal/config"
	"github.com/linuxmatters/jive-visualiser/internal/encoder"
)

func TestParseEncoderFlagAcceptsRegistryNames(t *testing.T) {
	tests := map[string]encoder.HWAccelType{
		"auto":     encoder.HWAccelAuto,
		"nvenc":    encoder.HWAccelNVENC,
		"qsv":      encoder.HWAccelQSV,
		"vaapi":    encoder.HWAccelVAAPI,
		"vulkan":   encoder.HWAccelVulkan,
		"software": encoder.HWAccelNone,
	}

	for name, want := range tests {
		got, err := parseEncoderFlag(name)
		if err != nil {
			t.Fatalf("parseEncoderFlag(%q): %v", name, err)
		}
		if got != want {
			t.Fatalf("parseEncoderFlag(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestParseEncoderFlagRejectsVideoToolbox(t *testing.T) {
	_, err := parseEncoderFlag("videotoolbox")
	if err == nil {
		t.Fatal("expected VideoToolbox to be rejected")
	}

	want := "invalid --encoder value: videotoolbox (must be auto, nvenc, qsv, vaapi, vulkan, or software)"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

func TestParseEncoderFlagInvalidError(t *testing.T) {
	_, err := parseEncoderFlag("bogus")
	if err == nil {
		t.Fatal("expected invalid encoder error")
	}

	want := "invalid --encoder value: bogus (must be auto, nvenc, qsv, vaapi, vulkan, or software)"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err, want)
	}
}

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

func TestConvertAndWriteAudioWritesConsumedSamples(t *testing.T) {
	src := []float64{0.1, 0.2, 0.3, 0.4, 0.5}

	cases := []struct {
		name   string
		stereo bool
		want   []float32
	}{
		{
			name:   "mono",
			stereo: false,
			want:   []float32{0.1, 0.2, 0.3},
		},
		{
			name:   "stereo",
			stereo: true,
			want:   []float32{0.1, 0.1, 0.2, 0.2, 0.3, 0.3},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got []float32
			write := func(samples []float32) error {
				got = append([]float32(nil), samples...)
				return nil
			}
			monoBuf := make([]float32, len(src))
			stereoBuf := make([]float32, len(src)*2)

			if err := convertAndWriteAudio(write, src, 3, tc.stereo, monoBuf, stereoBuf); err != nil {
				t.Fatalf("convertAndWriteAudio: %v", err)
			}

			if len(got) != len(tc.want) {
				t.Fatalf("wrote %d samples, want %d", len(got), len(tc.want))
			}
			for i, want := range tc.want {
				if got[i] != want {
					t.Errorf("sample %d = %v, want %v", i, got[i], want)
				}
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

func TestPass2ProgressMessageFieldsAndPreviewCopy(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, config.Width, config.Height))
	src.Pix[0] = 99

	runner := &pass2Runner{
		cfg: pass2Config{
			noPreview: false,
		},
		enc:               &encoder.Encoder{},
		numFrames:         42,
		audioCodecInfo:    "AAC 44.1㎑ mono",
		sensitivity:       1.25,
		rearrangedHeights: []float64{1, 2, 3},
		barHeightsCopy:    make([]float64, 3),
		previewImgs: [2]*image.RGBA{
			image.NewRGBA(image.Rect(0, 0, config.Width, config.Height)),
			image.NewRGBA(image.Rect(0, 0, config.Width, config.Height)),
		},
	}

	msg := runner.renderProgressMessage(4, src, 2*time.Second, 2048)

	if msg.Frame != 5 {
		t.Errorf("Frame = %d, want 5", msg.Frame)
	}
	if msg.TotalFrames != 42 {
		t.Errorf("TotalFrames = %d, want 42", msg.TotalFrames)
	}
	if msg.Elapsed != 2*time.Second {
		t.Errorf("Elapsed = %v, want 2s", msg.Elapsed)
	}
	if msg.FileSize != 2048 {
		t.Errorf("FileSize = %d, want 2048", msg.FileSize)
	}
	if msg.Sensitivity != 1.25 {
		t.Errorf("Sensitivity = %v, want 1.25", msg.Sensitivity)
	}
	if msg.VideoCodec != "H.264 1280×720" {
		t.Errorf("VideoCodec = %q, want H.264 1280×720", msg.VideoCodec)
	}
	if msg.AudioCodec != "AAC 44.1㎑ mono" {
		t.Errorf("AudioCodec = %q, want AAC 44.1㎑ mono", msg.AudioCodec)
	}
	if msg.EncoderName != "libx264" {
		t.Errorf("EncoderName = %q, want libx264", msg.EncoderName)
	}
	if msg.FrameData == nil {
		t.Fatal("FrameData is nil")
	}
	if msg.FrameData == src {
		t.Fatal("FrameData points at the source frame")
	}
	if got := msg.FrameData.Pix[0]; got != 99 {
		t.Errorf("FrameData first byte = %d, want 99", got)
	}
	src.Pix[0] = 12
	if got := msg.FrameData.Pix[0]; got != 99 {
		t.Errorf("FrameData changed after source mutation: %d, want 99", got)
	}
	if runner.previewIdx != 1 {
		t.Errorf("previewIdx = %d, want 1", runner.previewIdx)
	}
	for i, want := range []float64{1, 2, 3} {
		if msg.BarHeights[i] != want {
			t.Errorf("BarHeights[%d] = %v, want %v", i, msg.BarHeights[i], want)
		}
	}
}

func TestPass2ProgressMessageOmitsPreviewWhenDisabled(t *testing.T) {
	runner := &pass2Runner{
		cfg: pass2Config{
			noPreview: true,
		},
		enc:               &encoder.Encoder{},
		barHeightsCopy:    make([]float64, 1),
		rearrangedHeights: []float64{1},
	}
	msg := runner.renderProgressMessage(0, image.NewRGBA(image.Rect(0, 0, 1, 1)), time.Second, 0)
	if msg.FrameData != nil {
		t.Fatal("FrameData is set when preview is disabled")
	}
}

func TestPass2ProgressDueUsesInterval(t *testing.T) {
	runner := &pass2Runner{lastProgressUpdate: time.Now()}
	if runner.progressDue(30 * time.Millisecond) {
		t.Fatal("progress is due before interval")
	}

	runner.lastProgressUpdate = time.Now().Add(-60 * time.Millisecond)
	if !runner.progressDue(30 * time.Millisecond) {
		t.Fatal("progress is not due after interval")
	}
}

func TestPass2CurrentOutputFileSize(t *testing.T) {
	outputFile := t.TempDir() + "/out.mp4"
	if err := os.WriteFile(outputFile, []byte("12345"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	runner := &pass2Runner{cfg: pass2Config{outputFile: outputFile}}
	if got := runner.currentOutputFileSize(); got != 5 {
		t.Errorf("currentOutputFileSize() = %d, want 5", got)
	}

	runner.cfg.outputFile = outputFile + ".missing"
	if got := runner.currentOutputFileSize(); got != 0 {
		t.Errorf("currentOutputFileSize() for missing file = %d, want 0", got)
	}
}

func TestPass2RenderCompleteMessageFields(t *testing.T) {
	warnings := []string{"could not load embedded font"}
	runner := &pass2Runner{
		profile: &audio.Profile{
			SampleRate: 48000,
			Duration:   3,
		},
		cfg: pass2Config{
			outputFile:        "episode.mp4",
			thumbnailDuration: 700 * time.Millisecond,
		},
		enc:         &encoder.Encoder{},
		numFrames:   90,
		totalVis:    100 * time.Millisecond,
		totalEncode: 200 * time.Millisecond,
		totalAudio:  300 * time.Millisecond,
		warnings:    warnings,
	}

	msg := runner.renderCompleteMessage(123456, 5*time.Second)

	if msg.OutputFile != "episode.mp4" {
		t.Errorf("OutputFile = %q, want episode.mp4", msg.OutputFile)
	}
	if msg.FileSize != 123456 {
		t.Errorf("FileSize = %d, want 123456", msg.FileSize)
	}
	if msg.TotalFrames != 90 {
		t.Errorf("TotalFrames = %d, want 90", msg.TotalFrames)
	}
	if msg.VisTime != 100*time.Millisecond {
		t.Errorf("VisTime = %v, want 100ms", msg.VisTime)
	}
	if msg.EncodeTime != 200*time.Millisecond {
		t.Errorf("EncodeTime = %v, want 200ms", msg.EncodeTime)
	}
	if msg.AudioTime != 300*time.Millisecond {
		t.Errorf("AudioTime = %v, want 300ms", msg.AudioTime)
	}
	if msg.TotalTime != 5*time.Second {
		t.Errorf("TotalTime = %v, want 5s", msg.TotalTime)
	}
	if msg.ThumbnailTime != 700*time.Millisecond {
		t.Errorf("ThumbnailTime = %v, want 700ms", msg.ThumbnailTime)
	}
	if msg.SamplesProcessed != 144000 {
		t.Errorf("SamplesProcessed = %d, want 144000", msg.SamplesProcessed)
	}
	if msg.EncoderName != "libx264" {
		t.Errorf("EncoderName = %q, want libx264", msg.EncoderName)
	}
	if msg.EncoderIsHW {
		t.Fatal("EncoderIsHW = true, want false")
	}
	if len(msg.AssetWarnings) != 1 || msg.AssetWarnings[0] != warnings[0] {
		t.Errorf("AssetWarnings = %v, want %v", msg.AssetWarnings, warnings)
	}
}
