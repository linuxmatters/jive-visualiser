package encoder

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"unsafe"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

type audioEncoder struct {
	stream         *ffmpeg.AVStream
	codec          *ffmpeg.AVCodecContext
	frame          *ffmpeg.AVFrame
	fifo           *avAudioFIFO
	outputChannels int
	nextPts        int64
}

// avAudioFIFO wraps FFmpeg's AVAudioFifo, confining the C handle and all
// plane-pointer marshalling behind this type so no consumer touches the C
// boundary directly. The FIFO is packed (AVSampleFmtFlt) to preserve the
// interleaved push contract. The planar split happens at drain, in
// writeMonoFloats and writeStereoFloats.
type avAudioFIFO struct {
	fifo     *ffmpeg.AVAudioFifo
	channels int

	// Persistent C scratch plane (AVMalloc-backed) used to marshal interleaved
	// float32 samples across the CGO boundary for both write and read. Packed
	// AVSampleFmtFlt has a single plane, so all interleaved samples live here.
	// Write and read never run concurrently, so one buffer is shared. scratchCap
	// is measured in float32 elements; grown on demand.
	scratch    unsafe.Pointer
	scratchCap int
}

// newAudioEncoder sets up the AAC encoder for direct sample input.
func newAudioEncoder(
	formatCtx *ffmpeg.AVFormatContext,
	sampleRate int,
	outputChannels int,
) (_ *audioEncoder, err error) {
	audioEncoderCodec := ffmpeg.AVCodecFindEncoder(ffmpeg.AVCodecIdAac)
	if audioEncoderCodec == nil {
		return nil, fmt.Errorf("AAC encoder not found")
	}

	a := &audioEncoder{
		outputChannels: outputChannels,
	}
	defer func() {
		if err != nil {
			a.close()
		}
	}()

	a.stream = ffmpeg.AVFormatNewStream(formatCtx, nil)
	if a.stream == nil {
		return nil, fmt.Errorf("failed to create audio stream")
	}
	a.stream.SetId(1)

	a.codec = ffmpeg.AVCodecAllocContext3(audioEncoderCodec)
	if a.codec == nil {
		return nil, fmt.Errorf("failed to allocate audio encoder context")
	}

	// Configure AAC encoder using config sample rate.
	a.codec.SetSampleFmt(ffmpeg.AVSampleFmtFltp) // AAC requires float planar
	a.codec.SetSampleRate(sampleRate)
	ffmpeg.AVChannelLayoutDefault(a.codec.ChLayout(), outputChannels)
	a.codec.SetBitRate(192000) // 192 kbps
	a.stream.SetTimeBase(ffmpeg.AVMakeQ(1, a.codec.SampleRate()))

	ret, err := ffmpeg.AVCodecOpen2(a.codec, audioEncoderCodec, nil)
	if err := checkFFmpeg(ret, err, "open audio encoder"); err != nil {
		return nil, err
	}

	ret, err = ffmpeg.AVCodecParametersFromContext(a.stream.Codecpar(), a.codec)
	if err := checkFFmpeg(ret, err, "copy audio encoder parameters"); err != nil {
		return nil, err
	}

	a.frame = ffmpeg.AVFrameAlloc()
	if a.frame == nil {
		return nil, fmt.Errorf("failed to allocate audio encoder frame")
	}

	// AVAudioFifo-backed FIFO (packed float32) bridges the FFT chunk size
	// (2048) to the AAC encoder frame size (1024).
	audioFIFO, err := newAVAudioFIFO(outputChannels, a.codec.FrameSize())
	if err != nil {
		return nil, err
	}
	a.fifo = audioFIFO

	a.frame.SetNbSamples(a.codec.FrameSize())
	a.frame.SetFormat(int(ffmpeg.AVSampleFmtFltp))
	ffmpeg.AVChannelLayoutDefault(a.frame.ChLayout(), outputChannels)
	a.frame.SetSampleRate(a.codec.SampleRate())

	ret, err = ffmpeg.AVFrameGetBuffer(a.frame, 0)
	if err := checkFFmpeg(ret, err, "allocate encoder frame buffer"); err != nil {
		return nil, err
	}

	return a, nil
}

func (a *audioEncoder) writeSamples(samples []float32, pkt *ffmpeg.AVPacket, writePacket func(*ffmpeg.AVPacket) error) error {
	encoderFrameSize := a.codec.FrameSize() // 1024 for AAC

	if err := a.fifo.write(samples); err != nil {
		return err
	}

	// Drain the FIFO one encoder frame at a time. AVAudioFifoSize reports
	// samples-per-channel, so compare against encoderFrameSize (1024), not
	// encoderFrameSize*channels.
	for {
		size, err := a.fifo.size()
		if err != nil {
			return err
		}
		if size < encoderFrameSize {
			break
		}

		frameSamples, err := a.fifo.read(encoderFrameSize)
		if err != nil {
			return err
		}

		ret, err := ffmpeg.AVFrameMakeWritable(a.frame)
		if err := checkFFmpeg(ret, err, "make audio frame writable"); err != nil {
			return err
		}

		var writeErr error
		if a.outputChannels == 2 {
			writeErr = writeStereoFloats(a.frame, frameSamples)
		} else {
			writeErr = writeMonoFloats(a.frame, frameSamples)
		}

		if writeErr != nil {
			return fmt.Errorf("failed to write %s samples: %w",
				channelLayoutName(a.outputChannels), writeErr)
		}

		a.frame.SetPts(a.nextPts)
		a.nextPts += int64(encoderFrameSize)

		ret, err = ffmpeg.AVCodecSendFrame(a.codec, a.frame)
		if err := checkFFmpeg(ret, err, "send audio frame to encoder"); err != nil {
			return err
		}

		if err := a.receiveAndWritePackets(pkt, writePacket); err != nil {
			return err
		}
	}

	return nil
}

func (a *audioEncoder) flush(pkt *ffmpeg.AVPacket, writePacket func(*ffmpeg.AVPacket) error) error {
	encoderFrameSize := a.codec.FrameSize()

	// Drain any residual partial frame (< encoderFrameSize samples-per-channel)
	// from the AVAudioFifo and zero-pad it to a full encoder frame.
	remaining, err := a.fifo.size()
	if err != nil {
		return err
	}
	if remaining > 0 {
		samplesPerFrame := encoderFrameSize * a.outputChannels
		frameSamples := make([]float32, samplesPerFrame)
		partialSamples, err := a.fifo.read(remaining)
		if err != nil {
			return err
		}
		copy(frameSamples, partialSamples)

		ret, err := ffmpeg.AVFrameMakeWritable(a.frame)
		if err := checkFFmpeg(ret, err, "make final audio frame writable"); err != nil {
			return err
		}

		var writeErr error
		if a.outputChannels == 2 {
			writeErr = writeStereoFloats(a.frame, frameSamples)
		} else {
			writeErr = writeMonoFloats(a.frame, frameSamples)
		}

		if writeErr != nil {
			return fmt.Errorf("failed to write final samples: %w", writeErr)
		}

		a.frame.SetPts(a.nextPts)
		a.nextPts += int64(encoderFrameSize)

		ret, err = ffmpeg.AVCodecSendFrame(a.codec, a.frame)
		if err := checkFFmpeg(ret, err, "send final audio frame"); err != nil {
			return err
		}
	}

	// Send a NULL frame to enter draining mode.
	ret, err := ffmpeg.AVCodecSendFrame(a.codec, nil)
	if err := checkFFmpeg(ret, err, "drain audio encoder"); err != nil {
		return err
	}

	return a.receiveAndWritePackets(pkt, writePacket)
}

// receiveAndWritePackets receives encoded packets from the audio codec and
// writes them to the output. Reuses the shared packet; safe because the encoder
// is single-goroutine and the video and audio receive loops never run
// concurrently. Write errors are propagated, not swallowed.
func (a *audioEncoder) receiveAndWritePackets(pkt *ffmpeg.AVPacket, writePacket func(*ffmpeg.AVPacket) error) error {
	for {
		_, err := ffmpeg.AVCodecReceivePacket(a.codec, pkt)
		if err != nil {
			// EAGAIN and EOF are expected - means no more packets available.
			if errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
				break
			}
			return fmt.Errorf("receive audio packet from encoder: %w", err)
		}

		if err := writePacket(pkt); err != nil {
			return err
		}
	}

	return nil
}

func (a *audioEncoder) close() {
	if a == nil {
		return
	}
	if a.codec != nil {
		ffmpeg.AVCodecFreeContext(&a.codec)
	}
	if a.frame != nil {
		ffmpeg.AVFrameFree(&a.frame)
	}
	if a.fifo != nil {
		a.fifo.free()
		a.fifo = nil
	}
}

// newAVAudioFIFO allocates a packed float32 AVAudioFifo for the given channel
// count with an initial sample-per-channel capacity. Returns an error if the
// C allocation fails.
func newAVAudioFIFO(channels, initialNbSamples int) (*avAudioFIFO, error) {
	fifo := ffmpeg.AVAudioFifoAlloc(ffmpeg.AVSampleFmtFlt, channels, initialNbSamples)
	if fifo == nil {
		return nil, fmt.Errorf("failed to allocate AVAudioFifo")
	}
	return &avAudioFIFO{fifo: fifo, channels: channels}, nil
}

// growScratch (re)allocates the C scratch plane to hold at least n float32
// elements, mirroring the grow-on-demand pattern in reader.go:growOutputBuffer.
func (f *avAudioFIFO) growScratch(n int) error {
	if n <= 0 {
		return fmt.Errorf("growScratch: non-positive element count %d", n)
	}
	if n <= f.scratchCap && f.scratch != nil {
		return nil
	}
	if f.scratch != nil {
		ffmpeg.AVFree(f.scratch)
		f.scratch = nil
	}
	p := ffmpeg.AVMalloc(uint64(n) * uint64(unsafe.Sizeof(float32(0))))
	if p == nil {
		return fmt.Errorf("failed to allocate audio FIFO scratch")
	}
	f.scratch = p
	f.scratchCap = n
	return nil
}

// scratchSlice returns a []float32 view over the first n elements of the C
// scratch plane. The view aliases C memory and stays valid until the next
// growScratch or free.
func (f *avAudioFIFO) scratchSlice(n int) []float32 {
	return unsafe.Slice((*float32)(f.scratch), n)
}

// write copies interleaved float32 samples into the C scratch plane and writes
// them to the packed FIFO. samples is interleaved (mono, or L0,R0,L1,R1 for
// stereo); the per-channel sample count is len(samples)/channels.
func (f *avAudioFIFO) write(samples []float32) error {
	if len(samples) == 0 {
		return nil
	}
	if f.channels <= 0 {
		return fmt.Errorf("invalid audio FIFO channel count: %d", f.channels)
	}
	if len(samples)%f.channels != 0 {
		return fmt.Errorf("audio FIFO sample length %d is not divisible by channel count %d", len(samples), f.channels)
	}
	if err := f.growScratch(len(samples)); err != nil {
		return err
	}
	copy(f.scratchSlice(len(samples)), samples)

	nbSamples := len(samples) / f.channels
	ret, err := ffmpeg.AVAudioFifoWrite(
		f.fifo,
		[]unsafe.Pointer{f.scratch},
		nbSamples, f.channels, ffmpeg.AVSampleFmtFlt,
	)
	if err != nil {
		return fmt.Errorf("write audio FIFO: %w", err)
	}
	if ret < 0 {
		return fmt.Errorf("write audio FIFO: %w", ffmpeg.WrapErr(ret))
	}
	if ret != nbSamples {
		return fmt.Errorf("write audio FIFO: wrote %d of %d samples", ret, nbSamples)
	}
	return nil
}

// size returns the number of samples per channel currently buffered.
func (f *avAudioFIFO) size() (int, error) {
	ret, err := ffmpeg.AVAudioFifoSize(f.fifo)
	if err := checkFFmpeg(ret, err, "query audio FIFO size"); err != nil {
		return 0, err
	}
	return ret, nil
}

// read removes nbSamples samples per channel from the FIFO into the C scratch
// plane and returns an interleaved []float32 view over it (mono, or
// L0,R0,L1,R1 for stereo). The view aliases the scratch plane and is valid
// until the next write/read/free. Returns the actual per-channel sample count
// read.
func (f *avAudioFIFO) read(nbSamples int) ([]float32, error) {
	total := nbSamples * f.channels
	if err := f.growScratch(total); err != nil {
		return nil, err
	}
	ret, err := ffmpeg.AVAudioFifoRead(
		f.fifo,
		[]unsafe.Pointer{f.scratch},
		nbSamples, f.channels, ffmpeg.AVSampleFmtFlt,
	)
	if err != nil {
		return nil, fmt.Errorf("read audio FIFO: %w", err)
	}
	if ret < 0 {
		return nil, fmt.Errorf("read audio FIFO: %w", ffmpeg.WrapErr(ret))
	}
	return f.scratchSlice(ret * f.channels), nil
}

// free releases the C AVAudioFifo and scratch plane. Safe to call on a nil
// receiver or after the handles are already freed.
func (f *avAudioFIFO) free() {
	if f == nil {
		return
	}
	if f.fifo != nil {
		ffmpeg.AVAudioFifoFree(f.fifo)
		f.fifo = nil
	}
	if f.scratch != nil {
		ffmpeg.AVFree(f.scratch)
		f.scratch = nil
		f.scratchCap = 0
	}
}

// channelLayoutName returns the human-readable name for a channel count.
func channelLayoutName(channels int) string {
	if channels == 2 {
		return "stereo"
	}
	return "mono"
}

// writeMonoFloats writes mono float samples to a planar encoder frame.
func writeMonoFloats(frame *ffmpeg.AVFrame, samples []float32) error {
	nbSamples := len(samples)

	dataPtr := frame.Data().Get(0)
	if dataPtr == nil {
		return fmt.Errorf("frame data pointer not allocated")
	}

	data := unsafe.Slice((*byte)(dataPtr), nbSamples*4)

	for i := range nbSamples {
		binary.LittleEndian.PutUint32(data[i*4:(i+1)*4], math.Float32bits(samples[i]))
	}

	return nil
}

// writeStereoFloats writes interleaved stereo float samples to a planar encoder
// frame, splitting them into the left and right channel planes.
func writeStereoFloats(frame *ffmpeg.AVFrame, samples []float32) error {
	nbSamples := len(samples) / 2

	leftPtr := frame.Data().Get(0)
	rightPtr := frame.Data().Get(1)
	if leftPtr == nil || rightPtr == nil {
		return fmt.Errorf("frame data pointers not allocated")
	}

	leftData := unsafe.Slice((*byte)(leftPtr), nbSamples*4)
	rightData := unsafe.Slice((*byte)(rightPtr), nbSamples*4)

	for i := range nbSamples {
		binary.LittleEndian.PutUint32(leftData[i*4:(i+1)*4], math.Float32bits(samples[i*2]))
		binary.LittleEndian.PutUint32(rightData[i*4:(i+1)*4], math.Float32bits(samples[i*2+1]))
	}

	return nil
}
