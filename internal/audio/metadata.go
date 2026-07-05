package audio

import (
	"errors"
	"fmt"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

// errNoDuration is returned when a stream reports no usable duration (a missing
// PTS or a non-positive value), so a sample count cannot be derived.
var errNoDuration = errors.New("stream has no usable duration")

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

	// Total samples derived from stream duration (in time_base units). A missing
	// PTS (AVNoptsValue) or a non-positive duration would yield a nonsense sample
	// count, so reject it rather than propagate garbage.
	rawDuration := audioStream.Duration()
	if rawDuration == int64(ffmpeg.AVNoptsValue) || rawDuration <= 0 {
		return nil, fmt.Errorf("audio stream reports no usable duration: %w", errNoDuration)
	}

	sampleRate := codecpar.SampleRate()
	duration := float64(rawDuration) * float64(audioStream.TimeBase().Num()) / float64(audioStream.TimeBase().Den())
	numSamples := int64(duration * float64(sampleRate))

	return &Metadata{
		NumSamples: numSamples,
		SampleRate: sampleRate,
	}, nil
}
