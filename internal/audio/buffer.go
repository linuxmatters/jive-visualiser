package audio

import (
	"errors"
	"fmt"
	"io"
)

// readIntoBuffer fills buf from reader via repeated ReadInto calls. Returns the
// number of samples read and the raw error from the underlying reader, including
// io.EOF, so callers can apply their own end-of-file convention.
func readIntoBuffer(reader *StreamingReader, buf []float64) (int, error) {
	var total int
	for total < len(buf) {
		n, err := reader.ReadInto(buf[total:])
		if err != nil {
			if errors.Is(err, io.EOF) {
				return total, io.EOF
			}
			return total, fmt.Errorf("reading audio chunk: %w", err)
		}
		total += n
	}
	return total, nil
}

// FillFFTBuffer reads up to len(buf) samples from reader via repeated ReadChunk
// calls. Returns the number of samples read. Returns (0, nil) on immediate EOF,
// allowing callers to decide whether that is an error.
func FillFFTBuffer(reader *StreamingReader, buf []float64) (int, error) {
	total, err := readIntoBuffer(reader, buf)
	if err != nil && !errors.Is(err, io.EOF) {
		return total, err
	}
	return total, nil
}

// ReadNextFrame reads up to len(buf) samples from reader into the provided
// buffer. Returns the number of samples read. Returns (0, io.EOF) when no
// samples are available. Returns (n, nil) for partial frames at end of file.
func ReadNextFrame(reader *StreamingReader, buf []float64) (int, error) {
	total, err := readIntoBuffer(reader, buf)
	if err != nil {
		if errors.Is(err, io.EOF) {
			if total == 0 {
				return 0, io.EOF
			}
			return total, nil
		}
		return 0, err
	}
	return total, nil
}
