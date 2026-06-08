package audio

import (
	"errors"
	"fmt"
	"io"
	"slices"
	"unsafe"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

// swrOutBufferSamples is the initial capacity, in samples, of the reusable
// libswresample output buffer. Decoder frames are typically ≤4096 samples; the
// buffer grows on demand if a frame ever exceeds this, so no per-chunk
// allocation occurs in the steady state.
const swrOutBufferSamples = 8192

// StreamingReader provides chunk-based audio reading via FFmpeg's
// libavformat/libavcodec, supporting any audio format FFmpeg can decode.
//
// Decoded samples of any input format and channel layout are converted to
// packed mono float64 in [-1.0, 1.0] by libswresample, which applies the
// correct downmix coefficients for multi-channel sources.
type StreamingReader struct {
	formatCtx   *ffmpeg.AVFormatContext
	codecCtx    *ffmpeg.AVCodecContext
	streamIndex int
	packet      *ffmpeg.AVPacket
	frame       *ffmpeg.AVFrame
	sampleRate  int
	channels    int

	// swr converts each decoded frame to packed mono float64. outLayoutFrame
	// owns the mono output channel layout passed to swr; outPlanes is the
	// reusable C-allocated output buffer, sized in samples by outCap.
	swr            *ffmpeg.SwrContext
	outLayoutFrame *ffmpeg.AVFrame
	outPlanes      []unsafe.Pointer
	outCap         int
	// inPlanes is a reusable scratch slice of the decoded frame's plane
	// pointers, refilled per frame to avoid an allocation in the read loop.
	inPlanes []unsafe.Pointer

	// drained marks that the decoder has been flushed at end-of-stream, so the
	// delay buffer is not drained twice.
	drained bool

	// Buffer for leftover samples from previous decode.
	sampleBuffer []float64
}

// NewStreamingReader creates a streaming audio reader for the given file.
// Uses FFmpeg for broad format support (MP3, FLAC, WAV, OGG, AAC, etc.)
func NewStreamingReader(filename string) (*StreamingReader, error) {
	d := &StreamingReader{
		sampleBuffer: make([]float64, 0, 8192),
	}

	formatCtx, streamIndex, err := openAudioFormatCtx(filename)
	if err != nil {
		return nil, err
	}
	d.formatCtx = formatCtx
	d.streamIndex = streamIndex

	audioStream := d.formatCtx.Streams().Get(uintptr(d.streamIndex)) //nolint:gosec // stream index is non-negative

	decoder := ffmpeg.AVCodecFindDecoder(audioStream.Codecpar().CodecId())
	if decoder == nil {
		d.Close()
		return nil, fmt.Errorf("audio decoder not found for codec ID %d", audioStream.Codecpar().CodecId())
	}

	d.codecCtx = ffmpeg.AVCodecAllocContext3(decoder)
	if d.codecCtx == nil {
		d.Close()
		return nil, fmt.Errorf("failed to allocate codec context")
	}

	ret, err := ffmpeg.AVCodecParametersToContext(d.codecCtx, audioStream.Codecpar())
	if err != nil {
		d.Close()
		return nil, fmt.Errorf("failed to copy codec parameters: %w", err)
	}
	if ret < 0 {
		d.Close()
		return nil, fmt.Errorf("failed to copy codec parameters: error code %d", ret)
	}

	ret, err = ffmpeg.AVCodecOpen2(d.codecCtx, decoder, nil)
	if err != nil {
		d.Close()
		return nil, fmt.Errorf("failed to open codec: %w", err)
	}
	if ret < 0 {
		d.Close()
		return nil, fmt.Errorf("failed to open codec: error code %d", ret)
	}

	d.sampleRate = d.codecCtx.SampleRate()
	d.channels = d.codecCtx.ChLayout().NbChannels()

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

	if err := d.initResampler(); err != nil {
		d.Close()
		return nil, err
	}

	return d, nil
}

// initResampler configures libswresample to convert the decoder's channel
// layout, sample format, and rate into packed mono float64 at the same rate,
// and allocates the reusable output buffer. swr handles every sample format,
// planar or packed, and any channel count, applying correct downmix
// coefficients in place of a hand-rolled stereo average.
func (d *StreamingReader) initResampler() error {
	// Own the mono output channel layout via a throwaway frame's embedded
	// AVChannelLayout, mirroring the encoder's use of AVChannelLayoutDefault.
	d.outLayoutFrame = ffmpeg.AVFrameAlloc()
	if d.outLayoutFrame == nil {
		return fmt.Errorf("failed to allocate output layout frame")
	}
	outLayout := d.outLayoutFrame.ChLayout()
	ffmpeg.AVChannelLayoutDefault(outLayout, 1)

	ret, err := ffmpeg.SwrAllocSetOpts2(
		&d.swr,
		outLayout, ffmpeg.AVSampleFmtDbl, d.sampleRate,
		d.codecCtx.ChLayout(), d.codecCtx.SampleFmt(), d.sampleRate,
		0, nil,
	)
	if err != nil {
		return fmt.Errorf("failed to configure resampler: %w", err)
	}
	if ret < 0 || d.swr == nil {
		return fmt.Errorf("failed to configure resampler: error code %d", ret)
	}

	if ret, err := ffmpeg.SwrInit(d.swr); err != nil {
		return fmt.Errorf("failed to initialise resampler: %w", err)
	} else if ret < 0 {
		return fmt.Errorf("failed to initialise resampler: error code %d", ret)
	}

	if err := d.growOutputBuffer(swrOutBufferSamples); err != nil {
		return err
	}

	return nil
}

// growOutputBuffer (re)allocates the reusable mono float64 output buffer to hold
// at least n samples. It is called once at setup and only again if a decoded
// frame ever exceeds the current capacity, so the steady-state read loop makes
// no allocations.
func (d *StreamingReader) growOutputBuffer(n int) error {
	if n <= d.outCap && d.outPlanes != nil {
		return nil
	}
	if d.outPlanes != nil {
		ffmpeg.AVSamplesFreePlanes(d.outPlanes)
		d.outPlanes = nil
	}
	planes, _, ret, err := ffmpeg.AVSamplesAlloc(1, n, ffmpeg.AVSampleFmtDbl, 0)
	if err != nil {
		return fmt.Errorf("failed to allocate resampler output buffer: %w", err)
	}
	if ret < 0 {
		return fmt.Errorf("failed to allocate resampler output buffer: error code %d", ret)
	}
	d.outPlanes = planes
	d.outCap = n
	return nil
}

// ReadChunk reads the next chunk of samples as float64.
// Multi-channel input is automatically downmixed to mono.
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
// copied straight into dst with no intermediate allocation. Multi-channel input
// is automatically downmixed to mono. At end of stream it returns the final
// partial count, then io.EOF once the sample buffer is exhausted.
func (d *StreamingReader) ReadInto(dst []float64) (int, error) {
	numSamples := len(dst)

	// Satisfy from the buffer when possible.
	if len(d.sampleBuffer) >= numSamples {
		copy(dst, d.sampleBuffer[:numSamples])
		d.sampleBuffer = d.sampleBuffer[numSamples:]
		return numSamples, nil
	}

	// Decode more packets until the buffer holds enough samples.
	for len(d.sampleBuffer) < numSamples {
		ret, err := ffmpeg.AVReadFrame(d.formatCtx, d.packet)
		if err != nil {
			if errors.Is(err, ffmpeg.AVErrorEOF) {
				// End of stream: flush the decoder once to recover any frames
				// held in its delay buffer, drain the resampler, then drain the
				// sample buffer.
				if !d.drained {
					d.drained = true
					if err := d.flushDecoder(); err != nil {
						return 0, err
					}
					if err := d.drainResampler(); err != nil {
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

		if d.packet.StreamIndex() != d.streamIndex {
			ffmpeg.AVPacketUnref(d.packet)
			continue
		}

		_, err = ffmpeg.AVCodecSendPacket(d.codecCtx, d.packet)
		ffmpeg.AVPacketUnref(d.packet)
		if err != nil {
			return 0, fmt.Errorf("failed to send packet to decoder: %w", err)
		}

		for {
			_, err = ffmpeg.AVCodecReceiveFrame(d.codecCtx, d.frame)
			if err != nil {
				if errors.Is(err, ffmpeg.AVErrorEOF) || errors.Is(err, ffmpeg.EAgain) {
					break
				}
				return 0, fmt.Errorf("failed to receive frame: %w", err)
			}

			if err := d.extractSamples(); err != nil {
				return 0, fmt.Errorf("failed to extract samples: %w", err)
			}

			ffmpeg.AVFrameUnref(d.frame)
		}
	}

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

// extractSamples converts the current decoded frame to packed mono float64 via
// libswresample and appends the result onto the tail of d.sampleBuffer. The
// resampler reads the frame's plane pointers directly, so a single call handles
// any sample format, planar or packed, and any channel layout.
func (d *StreamingReader) extractSamples() error {
	nbSamples := d.frame.NbSamples()
	if nbSamples == 0 {
		return nil
	}

	// Upper bound on the output sample count for this input, accounting for any
	// samples still buffered inside swr. Grow the reusable output buffer if the
	// frame is unusually large.
	outCount, err := ffmpeg.SwrGetOutSamples(d.swr, nbSamples)
	if err != nil {
		return fmt.Errorf("failed to size resampler output: %w", err)
	}
	if err := d.growOutputBuffer(outCount); err != nil {
		return err
	}

	in := d.framePlanes()
	_, err = d.convertAndAppend(in, nbSamples, outCount)
	return err
}

// drainResampler flushes any samples buffered inside swr at end-of-stream by
// converting with a nil input until it yields nothing further. With output rate
// equal to input rate swr holds no internal delay, but draining is performed
// unconditionally so a future rate change cannot silently drop trailing samples.
func (d *StreamingReader) drainResampler() error {
	for {
		outCount, err := ffmpeg.SwrGetOutSamples(d.swr, 0)
		if err != nil {
			return fmt.Errorf("failed to size resampler drain: %w", err)
		}
		if outCount <= 0 {
			return nil
		}
		if err := d.growOutputBuffer(outCount); err != nil {
			return err
		}
		got, err := d.convertAndAppend(nil, 0, outCount)
		if err != nil {
			return err
		}
		if got == 0 {
			return nil
		}
	}
}

// convertAndAppend runs one swr conversion of inCount input samples (in may be
// nil to flush) into the reusable output buffer, then appends the produced mono
// float64 samples onto d.sampleBuffer. Returns the number of samples produced.
func (d *StreamingReader) convertAndAppend(in []unsafe.Pointer, inCount, outCount int) (int, error) {
	got, err := ffmpeg.SwrConvert(d.swr, d.outPlanes, outCount, in, inCount)
	if err != nil {
		return 0, fmt.Errorf("failed to resample frame: %w", err)
	}
	if got < 0 {
		return 0, fmt.Errorf("failed to resample frame: error code %d", got)
	}
	if got == 0 {
		return 0, nil
	}

	// The output buffer is packed mono AVSampleFmtDbl, i.e. a contiguous run of
	// float64, so reinterpret the first plane as a []float64 and copy onto the
	// sample-buffer tail. This is the only unsafe access in the read path.
	out := unsafe.Slice((*float64)(d.outPlanes[0]), got)
	dst := d.growSampleBuffer(got)
	copy(dst, out)
	return got, nil
}

// framePlanes refills the reusable inPlanes slice with the current frame's plane
// pointers: one plane per channel for planar formats, a single plane for packed.
func (d *StreamingReader) framePlanes() []unsafe.Pointer {
	nbPlanes := 1
	if planar, _ := ffmpeg.AVSampleFmtIsPlanar(ffmpeg.AVSampleFormat(d.frame.Format())); planar > 0 { //nolint:gosec // frame format is a valid AVSampleFormat enum
		nbPlanes = d.frame.ChLayout().NbChannels()
	}
	d.inPlanes = slices.Grow(d.inPlanes[:0], nbPlanes)[:nbPlanes]
	data := d.frame.ExtendedData()
	for i := range nbPlanes {
		d.inPlanes[i] = data.Get(uintptr(i)) //nolint:gosec // plane index is bounded by channel count
	}
	return d.inPlanes
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
	if d.outPlanes != nil {
		ffmpeg.AVSamplesFreePlanes(d.outPlanes)
		d.outPlanes = nil
	}
	if d.swr != nil {
		ffmpeg.SwrFree(&d.swr)
	}
	if d.outLayoutFrame != nil {
		ffmpeg.AVFrameFree(&d.outLayoutFrame)
	}
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
