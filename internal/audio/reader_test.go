package audio

import (
	"errors"
	"io"
	"testing"
)

func TestNewStreamingReader(t *testing.T) {
	reader, err := NewStreamingReader("../../testdata/LMP0.mp3")
	if err != nil {
		t.Fatalf("Failed to create streaming reader: %v", err)
	}
	defer reader.Close()

	// Get metadata to verify file was opened correctly
	metadata, err := GetMetadata("../../testdata/LMP0.mp3")
	if err != nil {
		t.Fatalf("Failed to get metadata: %v", err)
	}

	if metadata.NumSamples <= 0 {
		t.Errorf("Expected positive number of samples, got %d", metadata.NumSamples)
	}

	if reader.SampleRate() <= 0 {
		t.Errorf("Expected positive sample rate, got %d", reader.SampleRate())
	}

	t.Logf("Successfully opened MP3 file: %d samples at %d Hz", metadata.NumSamples, reader.SampleRate())
}

func TestNewStreamingReaderInvalidFile(t *testing.T) {
	_, err := NewStreamingReader("nonexistent.mp3")
	if err == nil {
		t.Error("Expected error for nonexistent file, got nil")
	}
}

func TestStreamingReaderReadInto(t *testing.T) {
	reader, err := NewStreamingReader("../../testdata/LMP0.mp3")
	if err != nil {
		t.Fatalf("Failed to create streaming reader: %v", err)
	}
	defer reader.Close()

	// Read a chunk of 2048 samples (MP3 may return less due to frame boundaries)
	buf := make([]float64, 2048)
	n, err := reader.ReadInto(buf)
	if err != nil {
		t.Fatalf("Failed to read chunk: %v", err)
	}
	chunk := buf[:n]

	if len(chunk) == 0 {
		t.Errorf("Expected non-empty chunk, got %d samples", len(chunk))
	}

	if len(chunk) > 2048 {
		t.Errorf("Chunk size exceeds requested: got %d, requested 2048", len(chunk))
	}

	// Check that values are normalized float64 (between -1.0 and 1.0)
	for i, sample := range chunk {
		if sample < -1.0 || sample > 1.0 {
			t.Errorf("Sample %d out of range: %f (should be between -1.0 and 1.0)", i, sample)
		}
	}

	t.Logf("Successfully read chunk of %d samples", len(chunk))
}

func TestStreamingReaderMultipleChunks(t *testing.T) {
	reader, err := NewStreamingReader("../../testdata/LMP0.mp3")
	if err != nil {
		t.Fatalf("Failed to create streaming reader: %v", err)
	}
	defer reader.Close()

	chunkSize := 2048
	totalRead := int64(0)
	chunkCount := 0
	buf := make([]float64, chunkSize)

	for {
		n, err := reader.ReadInto(buf)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Error reading chunk %d: %v", chunkCount, err)
		}
		chunk := buf[:n]

		if len(chunk) > chunkSize {
			t.Errorf("Chunk %d is larger than requested: %d > %d", chunkCount, len(chunk), chunkSize)
		}

		totalRead += int64(len(chunk))
		chunkCount++

		// Limit test to avoid reading entire file
		if chunkCount >= 10 {
			break
		}
	}

	if totalRead == 0 {
		t.Error("No samples were read")
	}

	t.Logf("Successfully read %d chunks (%d total samples)", chunkCount, totalRead)
}

func TestStreamingReaderEOF(t *testing.T) {
	reader, err := NewStreamingReader("../../testdata/LMP0.mp3")
	if err != nil {
		t.Fatalf("Failed to create streaming reader: %v", err)
	}
	defer reader.Close()

	// Get total sample count via metadata
	metadata, err := GetMetadata("../../testdata/LMP0.mp3")
	if err != nil {
		t.Fatalf("Failed to get metadata: %v", err)
	}

	// Read all samples
	chunkSize := 4096
	buf := make([]float64, chunkSize)

	for {
		_, err := reader.ReadInto(buf)
		if errors.Is(err, io.EOF) {
			// Expected EOF
			break
		}
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
	}

	// Try reading again - should get EOF immediately
	_, err = reader.ReadInto(buf)
	if !errors.Is(err, io.EOF) {
		t.Errorf("Expected EOF on second read past end, got: %v", err)
	}

	t.Logf("EOF handling works correctly after reading %d samples", metadata.NumSamples)
}

func TestStreamingReaderClose(t *testing.T) {
	reader, err := NewStreamingReader("../../testdata/LMP0.mp3")
	if err != nil {
		t.Fatalf("Failed to create streaming reader: %v", err)
	}

	err = reader.Close()
	if err != nil {
		t.Errorf("Failed to close reader: %v", err)
	}

	// Second close should not panic
	err = reader.Close()
	if err != nil {
		t.Logf("Second close returned error (acceptable): %v", err)
	}
}

func TestStreamingReaderMultipleReads(t *testing.T) {
	// Test that we can read samples in chunks and get consistent results
	// First pass: read in 2048 sample chunks
	reader1, err := NewStreamingReader("../../testdata/LMP0.mp3")
	if err != nil {
		t.Fatalf("Failed to create first reader: %v", err)
	}
	defer reader1.Close()

	var samples1 []float64
	chunkSize := 2048
	buf1 := make([]float64, chunkSize)
	for {
		n, err := reader1.ReadInto(buf1)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Error reading from first reader: %v", err)
		}
		chunk := buf1[:n]
		samples1 = append(samples1, chunk...)
	}

	// Second pass: read in 4096 sample chunks
	reader2, err := NewStreamingReader("../../testdata/LMP0.mp3")
	if err != nil {
		t.Fatalf("Failed to create second reader: %v", err)
	}
	defer reader2.Close()

	var samples2 []float64
	chunkSize2 := 4096
	buf2 := make([]float64, chunkSize2)
	for {
		n, err := reader2.ReadInto(buf2)
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatalf("Error reading from second reader: %v", err)
		}
		chunk := buf2[:n]
		samples2 = append(samples2, chunk...)
	}

	// Compare counts
	if len(samples1) != len(samples2) {
		t.Errorf("Sample count mismatch: first pass=%d, second pass=%d", len(samples1), len(samples2))
	}

	// Compare first 100 samples (should be identical)
	compareCount := 100
	compareCount = min(compareCount, len(samples1))

	for i := range compareCount {
		if samples1[i] != samples2[i] {
			t.Errorf("Sample %d mismatch: pass1=%f, pass2=%f", i, samples1[i], samples2[i])
		}
	}

	t.Logf("Multiple reads consistent: %d samples verified", compareCount)
}
