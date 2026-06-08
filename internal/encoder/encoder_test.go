package encoder

import (
	"os"
	"testing"
)

// newTestFIFO allocates an avAudioFIFO for the given channel count with the AAC
// encoder frame size as the initial capacity, and registers free() as a cleanup
// so the suite does not leak C memory.
func newTestFIFO(t *testing.T, channels int) *avAudioFIFO {
	t.Helper()
	const initialNbSamples = 1024 // AAC encoder frame size, samples per channel
	fifo, err := newAVAudioFIFO(channels, initialNbSamples)
	if err != nil {
		t.Fatalf("newAVAudioFIFO(%d, %d) failed: %v", channels, initialNbSamples, err)
	}
	t.Cleanup(fifo.free)
	return fifo
}

// fifoSize is a test helper that reads size() and fails on error.
func fifoSize(t *testing.T, f *avAudioFIFO) int {
	t.Helper()
	n, err := f.size()
	if err != nil {
		t.Fatalf("size() failed: %v", err)
	}
	return n
}

// fifoWrite is a test helper that writes interleaved samples and fails on error.
func fifoWrite(t *testing.T, f *avAudioFIFO, samples []float32) {
	t.Helper()
	if err := f.write(samples); err != nil {
		t.Fatalf("write(%d samples) failed: %v", len(samples), err)
	}
}

// fifoRead is a test helper that reads nbSamples per channel and returns a copy.
// read() aliases the shared C scratch plane, so the values are copied out before
// the caller's next write/read can overwrite them.
func fifoRead(t *testing.T, f *avAudioFIFO, nbSamples int) []float32 {
	t.Helper()
	view, err := f.read(nbSamples)
	if err != nil {
		t.Fatalf("read(%d) failed: %v", nbSamples, err)
	}
	out := make([]float32, len(view))
	copy(out, view)
	return out
}

// TestAVAudioFIFO_WriteReadMono writes interleaved mono samples, reads a full
// frame, and asserts the values round-trip exactly through the packed float32
// FIFO.
func TestAVAudioFIFO_WriteReadMono(t *testing.T) {
	f := newTestFIFO(t, 1)

	samples := []float32{1.0, 2.0, 3.0, 4.0, 5.0}
	fifoWrite(t, f, samples)

	if got := fifoSize(t, f); got != len(samples) {
		t.Errorf("size() = %d, want %d", got, len(samples))
	}

	got := fifoRead(t, f, len(samples))
	if len(got) != len(samples) {
		t.Fatalf("read returned %d samples, want %d", len(got), len(samples))
	}
	for i := range samples {
		if got[i] != samples[i] {
			t.Errorf("sample %d = %v, want %v", i, got[i], samples[i])
		}
	}

	if got := fifoSize(t, f); got != 0 {
		t.Errorf("after full read, size() = %d, want 0", got)
	}
}

// TestAVAudioFIFO_WriteReadStereo writes interleaved L/R samples and asserts the
// read preserves interleaving (channels are not swapped) and exact values.
func TestAVAudioFIFO_WriteReadStereo(t *testing.T) {
	f := newTestFIFO(t, 2)

	// Interleaved L0,R0,L1,R1,... with distinct, signed values per channel so a
	// channel swap would be visible.
	samples := []float32{-1.0, 1.0, -2.0, 2.0, -3.0, 3.0, -4.0, 4.0}
	perChannel := len(samples) / 2
	fifoWrite(t, f, samples)

	if got := fifoSize(t, f); got != perChannel {
		t.Errorf("size() = %d, want %d (samples per channel)", got, perChannel)
	}

	got := fifoRead(t, f, perChannel)
	if len(got) != len(samples) {
		t.Fatalf("read returned %d samples, want %d", len(got), len(samples))
	}
	for i := range samples {
		if got[i] != samples[i] {
			t.Errorf("interleaved sample %d = %v, want %v (L/R swap?)", i, got[i], samples[i])
		}
	}
}

// TestAVAudioFIFO_ReadMoreThanAvailable mirrors the old _PopMoreThanAvailable
// intent: a read request larger than the buffered count returns only what is
// available and drains the FIFO.
func TestAVAudioFIFO_ReadMoreThanAvailable(t *testing.T) {
	f := newTestFIFO(t, 1)

	samples := []float32{1.0, 2.0, 3.0}
	fifoWrite(t, f, samples)

	if got := fifoSize(t, f); got != 3 {
		t.Errorf("size() = %d, want 3", got)
	}

	// Request more than available; AVAudioFifoRead returns the available count.
	got := fifoRead(t, f, 10)
	if len(got) != 3 {
		t.Fatalf("read(10) with 3 available returned %d samples, want 3", len(got))
	}
	for i := range samples {
		if got[i] != samples[i] {
			t.Errorf("sample %d = %v, want %v", i, got[i], samples[i])
		}
	}

	if got := fifoSize(t, f); got != 0 {
		t.Errorf("after over-read, size() = %d, want 0", got)
	}
}

// TestAVAudioFIFO_EmptyBuffer verifies operations on an empty FIFO do not panic
// and report zero, mirroring the old _EmptyBuffer intent.
func TestAVAudioFIFO_EmptyBuffer(t *testing.T) {
	f := newTestFIFO(t, 1)

	if got := fifoSize(t, f); got != 0 {
		t.Errorf("empty FIFO size() = %d, want 0", got)
	}

	// Read on empty returns zero samples, no error.
	if got := fifoRead(t, f, 1024); len(got) != 0 {
		t.Errorf("read(1024) on empty FIFO returned %d samples, want 0", len(got))
	}

	// Writing an empty slice is a no-op and leaves the FIFO empty.
	fifoWrite(t, f, nil)
	if got := fifoSize(t, f); got != 0 {
		t.Errorf("after empty write, size() = %d, want 0", got)
	}
}

// TestAVAudioFIFO_FrameAlignedDrain writes more than one encoder frame, drains
// frame-aligned reads, and asserts a partial remainder survives for the flush
// case. Stereo exercises the interleaved packed layout.
func TestAVAudioFIFO_FrameAlignedDrain(t *testing.T) {
	const (
		channels     = 2
		frame        = 1024 // encoder frame size, samples per channel
		extraPerChan = 300  // partial remainder left after one full frame
		totalPerChan = frame + extraPerChan
	)
	f := newTestFIFO(t, channels)

	// Interleaved L/R: left = +index, right = -index, so a swap is detectable.
	samples := make([]float32, totalPerChan*channels)
	for i := range totalPerChan {
		samples[i*2] = float32(i)
		samples[i*2+1] = -float32(i)
	}
	fifoWrite(t, f, samples)

	if got := fifoSize(t, f); got != totalPerChan {
		t.Fatalf("size() = %d, want %d", got, totalPerChan)
	}

	// Drain one full encoder frame.
	got := fifoRead(t, f, frame)
	if len(got) != frame*channels {
		t.Fatalf("read(%d) returned %d samples, want %d", frame, len(got), frame*channels)
	}
	for i := range frame {
		if got[i*2] != float32(i) || got[i*2+1] != -float32(i) {
			t.Fatalf("frame sample %d = (%v,%v), want (%v,%v)",
				i, got[i*2], got[i*2+1], float32(i), -float32(i))
		}
	}

	// The partial remainder is left for the flush path.
	if got := fifoSize(t, f); got != extraPerChan {
		t.Fatalf("after one frame drain, size() = %d, want %d", got, extraPerChan)
	}

	rem := fifoRead(t, f, extraPerChan)
	if len(rem) != extraPerChan*channels {
		t.Fatalf("remainder read returned %d samples, want %d", len(rem), extraPerChan*channels)
	}
	for i := range extraPerChan {
		want := float32(frame + i)
		if rem[i*2] != want || rem[i*2+1] != -want {
			t.Fatalf("remainder sample %d = (%v,%v), want (%v,%v)",
				i, rem[i*2], rem[i*2+1], want, -want)
		}
	}

	if got := fifoSize(t, f); got != 0 {
		t.Errorf("after draining remainder, size() = %d, want 0", got)
	}
}

// TestAVAudioFIFO_SequentialOperations exercises multiple write/read rounds and
// checks size() after each, mirroring the old _SequentialOperations intent.
func TestAVAudioFIFO_SequentialOperations(t *testing.T) {
	f := newTestFIFO(t, 1)

	for round := range 5 {
		samples := make([]float32, 10)
		for i := range samples {
			samples[i] = float32(round*10 + i)
		}
		fifoWrite(t, f, samples)

		if got := fifoSize(t, f); got != 10 {
			t.Errorf("round %d: after write, size() = %d, want 10", round, got)
		}

		first := fifoRead(t, f, 5)
		if len(first) != 5 {
			t.Fatalf("round %d: read(5) returned %d samples, want 5", round, len(first))
		}
		for i := range 5 {
			if want := float32(round*10 + i); first[i] != want {
				t.Errorf("round %d: sample %d = %v, want %v", round, i, first[i], want)
			}
		}

		if got := fifoSize(t, f); got != 5 {
			t.Errorf("round %d: after read(5), size() = %d, want 5", round, got)
		}

		second := fifoRead(t, f, 5)
		for i := range 5 {
			if want := float32(round*10 + 5 + i); second[i] != want {
				t.Errorf("round %d: sample %d = %v, want %v", round, i+5, second[i], want)
			}
		}

		if got := fifoSize(t, f); got != 0 {
			t.Errorf("round %d: after draining, size() = %d, want 0", round, got)
		}
	}
}

// TestEncoderPOC is a proof-of-concept test that encodes a single black frame via RGBA path
func TestEncoderPOC(t *testing.T) {
	outputPath := "../../testdata/poc-video.mp4"
	defer os.Remove(outputPath)

	config := Config{
		OutputPath: outputPath,
		Width:      1280,
		Height:     720,
		Framerate:  30,
		HWAccel:    HWAccelNone, // Force software encoding
	}

	enc, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	err = enc.Initialize()
	if err != nil {
		t.Fatalf("Failed to initialize encoder: %v", err)
	}
	defer enc.Close()

	// Create a single black frame (RGBA format)
	frameSize := config.Width * config.Height * 4 // RGBA = 4 bytes per pixel
	blackFrame := make([]byte, frameSize)
	// Set alpha channel to opaque
	for i := 3; i < frameSize; i += 4 {
		blackFrame[i] = 255
	}

	// Write one frame using RGBA path
	err = enc.WriteFrameRGBA(blackFrame)
	if err != nil {
		t.Fatalf("Failed to write RGBA frame: %v", err)
	}

	// Close encoder (writes trailer)
	err = enc.Close()
	if err != nil {
		t.Fatalf("Failed to close encoder: %v", err)
	}

	// Verify output file exists and has non-zero size
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("Output file not created: %v", err)
	}

	if info.Size() == 0 {
		t.Fatalf("Output file is empty")
	}

	t.Logf("Successfully created video: %s (%d bytes)", outputPath, info.Size())
}

// TestEncoderRGBA tests the RGBA frame writing path
func TestEncoderRGBA(t *testing.T) {
	outputPath := "../../testdata/poc-rgba-video.mp4"
	defer os.Remove(outputPath) // Clean up after test

	config := Config{
		OutputPath: outputPath,
		Width:      1280,
		Height:     720,
		Framerate:  30,
	}

	enc, err := New(config)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	err = enc.Initialize()
	if err != nil {
		t.Fatalf("Failed to initialize encoder: %v", err)
	}
	defer enc.Close()

	// Create a single frame with red color (RGBA format)
	frameSize := config.Width * config.Height * 4 // RGBA = 4 bytes per pixel
	redFrame := make([]byte, frameSize)
	for i := 0; i < frameSize; i += 4 {
		redFrame[i] = 255   // R = 255 (red)
		redFrame[i+1] = 0   // G = 0
		redFrame[i+2] = 0   // B = 0
		redFrame[i+3] = 255 // A = 255 (opaque)
	}

	// Write one frame using RGBA path
	err = enc.WriteFrameRGBA(redFrame)
	if err != nil {
		t.Fatalf("Failed to write RGBA frame: %v", err)
	}

	// Close encoder (writes trailer)
	err = enc.Close()
	if err != nil {
		t.Fatalf("Failed to close encoder: %v", err)
	}

	// Verify output file exists and has non-zero size
	info, err := os.Stat(outputPath)
	if err != nil {
		t.Fatalf("Output file not created: %v", err)
	}

	if info.Size() == 0 {
		t.Fatalf("Output file is empty")
	}

	t.Logf("Successfully created RGBA video: %s (%d bytes)", outputPath, info.Size())
}
