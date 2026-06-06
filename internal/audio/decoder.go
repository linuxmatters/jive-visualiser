package audio

// Decoder defines the interface for all audio format decoders
type Decoder interface {
	// ReadChunk reads the next chunk of samples as float64
	// Returns nil when EOF is reached
	ReadChunk(numSamples int) ([]float64, error)

	// SampleRate returns the audio sample rate in Hz
	SampleRate() int

	// Close closes the decoder and releases resources
	Close() error
}
