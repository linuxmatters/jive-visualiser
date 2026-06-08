package audio

import (
	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

// Metadata holds information about an audio file.
type Metadata struct {
	NumSamples int64
	SampleRate int
}

// GetMetadata uses ffmpeg to extract accurate audio file metadata.
func GetMetadata(filename string) (*Metadata, error) {
	inputCtx, audioStreamIdx, err := openAudioFormatCtx(filename)
	if err != nil {
		return nil, err
	}
	defer ffmpeg.AVFormatCloseInput(&inputCtx)

	audioStream := inputCtx.Streams().Get(uintptr(audioStreamIdx)) //nolint:gosec // stream index is non-negative
	codecpar := audioStream.Codecpar()

	// Total samples derived from stream duration (in time_base units).
	sampleRate := codecpar.SampleRate()
	duration := float64(audioStream.Duration()) * float64(audioStream.TimeBase().Num()) / float64(audioStream.TimeBase().Den())
	numSamples := int64(duration * float64(sampleRate))

	return &Metadata{
		NumSamples: numSamples,
		SampleRate: sampleRate,
	}, nil
}
