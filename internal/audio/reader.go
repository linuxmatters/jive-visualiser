package audio

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
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

	// drained marks that the decoder has been flushed at end-of-stream, so the
	// delay buffer is not drained twice.
	drained bool

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

	// Sample extraction only handles mono and stereo layouts; planar sources
	// with more channels would read past the first channel plane.
	if d.channels != 1 && d.channels != 2 {
		d.Close()
		return nil, fmt.Errorf("unsupported channel count: %d", d.channels)
	}

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
	result := make([]float64, numSamples)
	n, err := d.ReadInto(result)
	if err != nil {
		return nil, err
	}
	return result[:n], nil
}

// ReadInto fills dst with the next samples as float64, decoding more from the
// stream as needed, and returns the number of samples written. Samples are
// copied straight into dst with no intermediate allocation. Stereo input is
// automatically downmixed to mono. At end of stream it returns the final
// partial count, then io.EOF once the sample buffer is exhausted.
func (d *StreamingReader) ReadInto(dst []float64) (int, error) {
	numSamples := len(dst)

	// First, try to satisfy request from buffer
	if len(d.sampleBuffer) >= numSamples {
		copy(dst, d.sampleBuffer[:numSamples])
		d.sampleBuffer = d.sampleBuffer[numSamples:]
		return numSamples, nil
	}

	// Need to decode more samples
	for len(d.sampleBuffer) < numSamples {
		// Read packet
		ret, err := ffmpeg.AVReadFrame(d.formatCtx, d.packet)
		if err != nil {
			if errors.Is(err, ffmpeg.AVErrorEOF) {
				// End of stream: flush the decoder once to recover any frames
				// held in its delay buffer, then drain the sample buffer.
				if !d.drained {
					d.drained = true
					if err := d.flushDecoder(); err != nil {
						return 0, err
					}
				}
				if len(d.sampleBuffer) > 0 {
					n := min(numSamples, len(d.sampleBuffer))
					copy(dst, d.sampleBuffer[:n])
					d.sampleBuffer = d.sampleBuffer[n:]
					return n, nil
				}
				return 0, io.EOF
			}
			return 0, fmt.Errorf("failed to read packet: %w", err)
		}
		if ret < 0 {
			return 0, fmt.Errorf("failed to read packet: error code %d", ret)
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
			return 0, fmt.Errorf("failed to send packet to decoder: %w", err)
		}

		// Receive decoded frames
		for {
			_, err = ffmpeg.AVCodecReceiveFrame(d.codecCtx, d.frame)
			if err != nil {
				if errors.Is(err, ffmpeg.AVErrorEOF) || errors.Is(err, ffmpeg.EAgain) {
					break
				}
				return 0, fmt.Errorf("failed to receive frame: %w", err)
			}

			// Decode samples directly into the buffer tail
			if err := d.extractSamples(); err != nil {
				return 0, fmt.Errorf("failed to extract samples: %w", err)
			}

			ffmpeg.AVFrameUnref(d.frame)
		}
	}

	// Copy requested samples from buffer
	copy(dst, d.sampleBuffer[:numSamples])
	d.sampleBuffer = d.sampleBuffer[numSamples:]
	return numSamples, nil
}

// flushDecoder sends a NULL packet to enter draining mode and appends every
// remaining frame held in the decoder's delay buffer to the sample buffer.
func (d *StreamingReader) flushDecoder() error {
	if _, err := ffmpeg.AVCodecSendPacket(d.codecCtx, nil); err != nil {
		return fmt.Errorf("failed to flush decoder: %w", err)
	}

	for {
		_, err := ffmpeg.AVCodecReceiveFrame(d.codecCtx, d.frame)
		if err != nil {
			if errors.Is(err, ffmpeg.AVErrorEOF) || errors.Is(err, ffmpeg.EAgain) {
				break
			}
			return fmt.Errorf("failed to receive frame: %w", err)
		}

		if err := d.extractSamples(); err != nil {
			return fmt.Errorf("failed to extract samples: %w", err)
		}

		ffmpeg.AVFrameUnref(d.frame)
	}

	return nil
}

// decodeS16 decodes a signed 16-bit little-endian sample at byte offset i,
// normalised to [-1.0, 1.0].
func decodeS16(buf []byte, i int) float64 {
	// Same-width two's-complement reinterpretation, not a narrowing conversion.
	val := int16(binary.LittleEndian.Uint16(buf[i:])) //nolint:gosec
	return float64(val) / 32768.0
}

// decodeS32 decodes a signed 32-bit little-endian sample at byte offset i,
// normalised to [-1.0, 1.0].
func decodeS32(buf []byte, i int) float64 {
	// Same-width two's-complement reinterpretation, not a narrowing conversion.
	val := int32(binary.LittleEndian.Uint32(buf[i:])) //nolint:gosec
	return float64(val) / 2147483648.0
}

// decodeF32 decodes a 32-bit IEEE 754 float sample at byte offset i.
func decodeF32(buf []byte, i int) float64 {
	return float64(math.Float32frombits(binary.LittleEndian.Uint32(buf[i:])))
}

// sampleDecoder selects the appropriate decode function and bytes-per-sample
// for the given sample format code.
func sampleDecoder(sampleFormat ffmpeg.AVSampleFormat) (func([]byte, int) float64, int, error) {
	switch sampleFormat {
	case ffmpeg.AVSampleFmtS16, ffmpeg.AVSampleFmtS16P:
		return decodeS16, 2, nil
	case ffmpeg.AVSampleFmtS32, ffmpeg.AVSampleFmtS32P:
		return decodeS32, 4, nil
	case ffmpeg.AVSampleFmtFlt, ffmpeg.AVSampleFmtFltp:
		return decodeF32, 4, nil
	default:
		return nil, 0, fmt.Errorf("unsupported sample format: %d", sampleFormat)
	}
}

// extractSamples decodes float64 samples from the current frame straight onto
// the tail of d.sampleBuffer, avoiding a per-frame temporary allocation.
// Stereo is automatically downmixed to mono.
func (d *StreamingReader) extractSamples() error {
	nbSamples := d.frame.NbSamples()
	// Frame format is always a valid AVSampleFormat enum, within int32 range.
	sampleFormat := ffmpeg.AVSampleFormat(d.frame.Format()) //nolint:gosec
	channels := d.channels

	decode, bps, err := sampleDecoder(sampleFormat)
	if err != nil {
		return err
	}

	// Determine if format is planar (one sample plane per channel)
	var isPlanar bool
	switch sampleFormat {
	case ffmpeg.AVSampleFmtS16P, ffmpeg.AVSampleFmtS32P, ffmpeg.AVSampleFmtFltp:
		isPlanar = true
	}

	switch {
	case isPlanar && channels == 2:
		leftPtr := d.frame.Data().Get(0)
		rightPtr := d.frame.Data().Get(1)
		if leftPtr == nil || rightPtr == nil {
			return fmt.Errorf("missing channel data")
		}
		leftBuf := unsafe.Slice((*byte)(leftPtr), nbSamples*bps)
		rightBuf := unsafe.Slice((*byte)(rightPtr), nbSamples*bps)
		samples := d.growSampleBuffer(nbSamples)
		for i := range nbSamples {
			samples[i] = (decode(leftBuf, i*bps) + decode(rightBuf, i*bps)) / 2
		}

	case isPlanar && channels == 1:
		dataPtr := d.frame.Data().Get(0)
		if dataPtr == nil {
			return fmt.Errorf("no data in frame")
		}
		buf := unsafe.Slice((*byte)(dataPtr), nbSamples*bps)
		samples := d.growSampleBuffer(nbSamples)
		for i := range nbSamples {
			samples[i] = decode(buf, i*bps)
		}

	default:
		// Interleaved formats
		dataPtr := d.frame.Data().Get(0)
		if dataPtr == nil {
			return fmt.Errorf("no data in frame")
		}
		stride := bps * channels
		buf := unsafe.Slice((*byte)(dataPtr), nbSamples*stride)
		samples := d.growSampleBuffer(nbSamples)
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

	return nil
}

// growSampleBuffer extends d.sampleBuffer by n elements and returns the newly
// added tail region for in-place writing, reusing spare capacity where possible.
func (d *StreamingReader) growSampleBuffer(n int) []float64 {
	start := len(d.sampleBuffer)
	d.sampleBuffer = slices.Grow(d.sampleBuffer, n)[:start+n]
	return d.sampleBuffer[start:]
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
