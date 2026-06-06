package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"unsafe"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

// StreamingReader provides chunk-based audio reading via FFmpeg's
// libavformat/libavcodec, supporting any audio format FFmpeg can decode.
type StreamingReader struct {
	formatCtx   *ffmpeg.AVFormatContext
	codecCtx    *ffmpeg.AVCodecContext
	streamIndex int
	packet      *ffmpeg.AVPacket
	frame       *ffmpeg.AVFrame
	sampleRate  int
	channels    int

	// Buffer for leftover samples from previous decode
	sampleBuffer []float64
}

// NewStreamingReader creates a streaming audio reader for the given file.
// Uses FFmpeg for broad format support (MP3, FLAC, WAV, OGG, AAC, etc.)
func NewStreamingReader(filename string) (*StreamingReader, error) {
	d := &StreamingReader{
		sampleBuffer: make([]float64, 0, 8192),
	}

	// Open input file and find audio stream
	formatCtx, streamIndex, err := openAudioFormatCtx(filename)
	if err != nil {
		return nil, err
	}
	d.formatCtx = formatCtx
	d.streamIndex = streamIndex

	audioStream := d.formatCtx.Streams().Get(uintptr(d.streamIndex)) //nolint:gosec // stream index is non-negative

	// Find decoder
	decoder := ffmpeg.AVCodecFindDecoder(audioStream.Codecpar().CodecId())
	if decoder == nil {
		d.Close()
		return nil, fmt.Errorf("audio decoder not found for codec ID %d", audioStream.Codecpar().CodecId())
	}

	// Allocate codec context
	d.codecCtx = ffmpeg.AVCodecAllocContext3(decoder)
	if d.codecCtx == nil {
		d.Close()
		return nil, fmt.Errorf("failed to allocate codec context")
	}

	// Copy codec parameters
	ret, err := ffmpeg.AVCodecParametersToContext(d.codecCtx, audioStream.Codecpar())
	if err != nil {
		d.Close()
		return nil, fmt.Errorf("failed to copy codec parameters: %w", err)
	}
	if ret < 0 {
		d.Close()
		return nil, fmt.Errorf("failed to copy codec parameters: error code %d", ret)
	}

	// Open codec
	ret, err = ffmpeg.AVCodecOpen2(d.codecCtx, decoder, nil)
	if err != nil {
		d.Close()
		return nil, fmt.Errorf("failed to open codec: %w", err)
	}
	if ret < 0 {
		d.Close()
		return nil, fmt.Errorf("failed to open codec: error code %d", ret)
	}

	// Store audio properties
	d.sampleRate = d.codecCtx.SampleRate()
	d.channels = d.codecCtx.ChLayout().NbChannels()

	// Validate format support
	sampleFmt := d.codecCtx.SampleFmt()
	supportedFormats := map[ffmpeg.AVSampleFormat]bool{
		ffmpeg.AVSampleFmtS16:  true, // 16-bit signed interleaved
		ffmpeg.AVSampleFmtS32:  true, // 32-bit signed interleaved
		ffmpeg.AVSampleFmtFlt:  true, // 32-bit float interleaved
		ffmpeg.AVSampleFmtS16P: true, // 16-bit signed planar
		ffmpeg.AVSampleFmtS32P: true, // 32-bit signed planar
		ffmpeg.AVSampleFmtFltp: true, // 32-bit float planar
	}
	if !supportedFormats[sampleFmt] {
		d.Close()
		return nil, fmt.Errorf("unsupported sample format: %d", sampleFmt)
	}

	// Allocate packet and frame
	d.packet = ffmpeg.AVPacketAlloc()
	if d.packet == nil {
		d.Close()
		return nil, fmt.Errorf("failed to allocate packet")
	}

	d.frame = ffmpeg.AVFrameAlloc()
	if d.frame == nil {
		d.Close()
		return nil, fmt.Errorf("failed to allocate frame")
	}

	return d, nil
}

// ReadChunk reads the next chunk of samples as float64.
// Stereo input is automatically downmixed to mono.
// Returns io.EOF when no more samples are available.
func (d *StreamingReader) ReadChunk(numSamples int) ([]float64, error) {
	// First, try to satisfy request from buffer
	if len(d.sampleBuffer) >= numSamples {
		result := make([]float64, numSamples)
		copy(result, d.sampleBuffer[:numSamples])
		d.sampleBuffer = d.sampleBuffer[numSamples:]
		return result, nil
	}

	// Need to decode more samples
	for len(d.sampleBuffer) < numSamples {
		// Read packet
		ret, err := ffmpeg.AVReadFrame(d.formatCtx, d.packet)
		if err != nil {
			if errors.Is(err, ffmpeg.AVErrorEOF) {
				// End of file - return what we have
				if len(d.sampleBuffer) > 0 {
					result := make([]float64, len(d.sampleBuffer))
					copy(result, d.sampleBuffer)
					d.sampleBuffer = d.sampleBuffer[:0]
					return result, nil
				}
				return nil, io.EOF
			}
			return nil, fmt.Errorf("failed to read packet: %w", err)
		}
		if ret < 0 {
			return nil, fmt.Errorf("failed to read packet: error code %d", ret)
		}

		// Skip non-audio packets
		if d.packet.StreamIndex() != d.streamIndex {
			ffmpeg.AVPacketUnref(d.packet)
			continue
		}

		// Send packet to decoder
		_, err = ffmpeg.AVCodecSendPacket(d.codecCtx, d.packet)
		ffmpeg.AVPacketUnref(d.packet)
		if err != nil {
			return nil, fmt.Errorf("failed to send packet to decoder: %w", err)
		}

		// Receive decoded frames
		for {
			_, err = ffmpeg.AVCodecReceiveFrame(d.codecCtx, d.frame)
			if err != nil {
				if errors.Is(err, ffmpeg.AVErrorEOF) || errors.Is(err, ffmpeg.EAgain) {
					break
				}
				return nil, fmt.Errorf("failed to receive frame: %w", err)
			}

			// Extract samples and add to buffer
			samples, err := d.extractSamples()
			if err != nil {
				return nil, fmt.Errorf("failed to extract samples: %w", err)
			}
			d.sampleBuffer = append(d.sampleBuffer, samples...)

			ffmpeg.AVFrameUnref(d.frame)
		}
	}

	// Return requested samples from buffer
	result := make([]float64, numSamples)
	copy(result, d.sampleBuffer[:numSamples])
	d.sampleBuffer = d.sampleBuffer[numSamples:]
	return result, nil
}

// unsafeByteSlice reinterprets a pointer as a byte slice of the given length.
func unsafeByteSlice(ptr unsafe.Pointer, length int) []byte {
	return (*[1 << 30]byte)(ptr)[:length:length]
}

// decodeS16 decodes a signed 16-bit little-endian sample at byte offset i,
// normalised to [-1.0, 1.0].
func decodeS16(buf []byte, i int) float64 {
	val := int16(binary.LittleEndian.Uint16(buf[i:]))
	return float64(val) / 32768.0
}

// decodeS32 decodes a signed 32-bit little-endian sample at byte offset i,
// normalised to [-1.0, 1.0].
func decodeS32(buf []byte, i int) float64 {
	val := int32(binary.LittleEndian.Uint32(buf[i:]))
	return float64(val) / 2147483648.0
}

// decodeF32 decodes a 32-bit IEEE 754 float sample at byte offset i.
func decodeF32(buf []byte, i int) float64 {
	return float64(math.Float32frombits(binary.LittleEndian.Uint32(buf[i:])))
}

// sampleDecoder selects the appropriate decode function and bytes-per-sample
// for the given sample format code.
func sampleDecoder(sampleFormat int) (func([]byte, int) float64, int, error) {
	switch sampleFormat {
	case 1, 6: // S16, S16P
		return decodeS16, 2, nil
	case 2, 7: // S32, S32P
		return decodeS32, 4, nil
	case 3, 8: // Flt, Fltp
		return decodeF32, 4, nil
	default:
		return nil, 0, fmt.Errorf("unsupported sample format: %d", sampleFormat)
	}
}

// extractSamples extracts float64 samples from the current frame.
// Stereo is automatically downmixed to mono.
func (d *StreamingReader) extractSamples() ([]float64, error) {
	nbSamples := d.frame.NbSamples()
	sampleFormat := d.frame.Format()
	channels := d.channels

	decode, bps, err := sampleDecoder(sampleFormat)
	if err != nil {
		return nil, err
	}

	samples := make([]float64, nbSamples)

	// Determine if format is planar (one sample plane per channel)
	var isPlanar bool
	switch ffmpeg.AVSampleFormat(sampleFormat) {
	case ffmpeg.AVSampleFmtS16P, ffmpeg.AVSampleFmtS32P, ffmpeg.AVSampleFmtFltp:
		isPlanar = true
	}

	switch {
	case isPlanar && channels == 2:
		leftPtr := d.frame.Data().Get(0)
		rightPtr := d.frame.Data().Get(1)
		if leftPtr == nil || rightPtr == nil {
			return nil, fmt.Errorf("missing channel data")
		}
		leftBuf := unsafeByteSlice(leftPtr, nbSamples*bps)
		rightBuf := unsafeByteSlice(rightPtr, nbSamples*bps)
		for i := range nbSamples {
			samples[i] = (decode(leftBuf, i*bps) + decode(rightBuf, i*bps)) / 2
		}

	case isPlanar && channels == 1:
		dataPtr := d.frame.Data().Get(0)
		if dataPtr == nil {
			return nil, fmt.Errorf("no data in frame")
		}
		buf := unsafeByteSlice(dataPtr, nbSamples*bps)
		for i := range nbSamples {
			samples[i] = decode(buf, i*bps)
		}

	default:
		// Interleaved formats
		dataPtr := d.frame.Data().Get(0)
		if dataPtr == nil {
			return nil, fmt.Errorf("no data in frame")
		}
		stride := bps * channels
		buf := unsafeByteSlice(dataPtr, nbSamples*stride)
		if channels == 1 {
			for i := range nbSamples {
				samples[i] = decode(buf, i*stride)
			}
		} else {
			for i := range nbSamples {
				samples[i] = (decode(buf, i*stride) + decode(buf, i*stride+bps)) / 2
			}
		}
	}

	return samples, nil
}

// SampleRate returns the audio sample rate in Hz.
func (d *StreamingReader) SampleRate() int {
	return d.sampleRate
}

// Close releases all FFmpeg resources.
func (d *StreamingReader) Close() error {
	if d.frame != nil {
		ffmpeg.AVFrameFree(&d.frame)
	}
	if d.packet != nil {
		ffmpeg.AVPacketFree(&d.packet)
	}
	if d.codecCtx != nil {
		ffmpeg.AVCodecFreeContext(&d.codecCtx)
	}
	if d.formatCtx != nil {
		ffmpeg.AVFormatCloseInput(&d.formatCtx)
	}
	return nil
}
