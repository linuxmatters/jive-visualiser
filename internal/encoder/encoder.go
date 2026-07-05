package encoder

import (
	"errors"
	"fmt"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

// checkFFmpeg provides consistent error handling for FFmpeg API calls.
// It checks both the Go error (binding issues) and the return code (FFmpeg errors).
// The op parameter should describe the operation, e.g. "allocate output context".
func checkFFmpeg(ret int, err error, op string) error {
	if err != nil {
		return fmt.Errorf("%s: %w", op, err)
	}
	if ret < 0 {
		return fmt.Errorf("%s: %w", op, ffmpeg.WrapErr(ret))
	}
	return nil
}

// Config holds the encoder configuration
type Config struct {
	OutputPath    string      // Path to output MP4 file
	Width         int         // Video width in pixels
	Height        int         // Video height in pixels
	Framerate     int         // Frames per second
	SampleRate    int         // Audio sample rate (required for audio encoding)
	AudioChannels int         // Output audio channels: 1 (mono) or 2 (stereo), defaults to 1
	HWAccel       HWAccelType // Hardware acceleration type (default: auto-detect)
}

// Encoder wraps FFmpeg encoding functionality
type Encoder struct {
	config Config

	// Output muxer (MP4 container)
	formatCtx *ffmpeg.AVFormatContext
	pkt       *ffmpeg.AVPacket

	// Video stream and encoder
	video *videoEncoder

	// Audio stream and encoder
	audio *audioEncoder
}

// New creates a new encoder instance
func New(config Config) (*Encoder, error) {
	// Validate configuration
	if config.Width <= 0 || config.Height <= 0 {
		return nil, fmt.Errorf("invalid dimensions: %dx%d", config.Width, config.Height)
	}
	if config.Framerate <= 0 {
		return nil, fmt.Errorf("invalid framerate: %d", config.Framerate)
	}
	if config.OutputPath == "" {
		return nil, fmt.Errorf("output path cannot be empty")
	}
	if config.AudioChannels != 0 && config.AudioChannels != 1 && config.AudioChannels != 2 {
		return nil, fmt.Errorf("invalid audio channel count: %d", config.AudioChannels)
	}

	return &Encoder{config: config}, nil
}

// Initialize sets up the FFmpeg encoder pipeline
func (e *Encoder) Initialize() (err error) {
	var ret int

	// Suppress FFmpeg log output so it does not corrupt the TUI.
	ffmpeg.AVLogSetLevel(ffmpeg.AVLogQuiet)
	defer func() {
		if err == nil {
			return
		}
		e.freeResources()
	}()

	outputPath := ffmpeg.ToCStr(e.config.OutputPath)
	defer outputPath.Free()

	ret, err = ffmpeg.AVFormatAllocOutputContext2(&e.formatCtx, nil, nil, outputPath)
	if err := checkFFmpeg(ret, err, "allocate output context"); err != nil {
		return err
	}

	e.pkt = ffmpeg.AVPacketAlloc()
	if e.pkt == nil {
		return fmt.Errorf("failed to allocate reusable packet")
	}

	video, err := newVideoEncoder(e.formatCtx, e.config)
	if err != nil {
		return err
	}
	e.video = video

	var pb *ffmpeg.AVIOContext
	ret, err = ffmpeg.AVIOOpen(&pb, outputPath, ffmpeg.AVIOFlagWrite)
	if err := checkFFmpeg(ret, err, "open output file"); err != nil {
		return err
	}
	e.formatCtx.SetPb(pb)

	if e.config.SampleRate > 0 {
		audio, err := newAudioEncoder(e.formatCtx, e.config.SampleRate, e.outputChannels())
		if err != nil {
			return fmt.Errorf("failed to initialize audio encoder: %w", err)
		}
		e.audio = audio
	}

	ret, err = ffmpeg.AVFormatWriteHeader(e.formatCtx, nil)
	if err := checkFFmpeg(ret, err, "write header"); err != nil {
		return err
	}

	return nil
}

// EncoderName returns the name of the video encoder being used
func (e *Encoder) EncoderName() string {
	return e.video.encoderName()
}

// IsHardware reports whether encoding ran on a hardware-backed encoder.
func (e *Encoder) IsHardware() bool {
	return e.video.isHardware()
}

// outputChannels returns the configured audio channel count, defaulting to mono.
func (e *Encoder) outputChannels() int {
	if e.config.AudioChannels == 0 {
		return 1
	}
	return e.config.AudioChannels
}

// WriteFrameRGBA encodes and writes a single RGBA frame.
func (e *Encoder) WriteFrameRGBA(rgbaData []byte) error {
	expectedSize := e.config.Width * e.config.Height * 4
	if len(rgbaData) != expectedSize {
		return fmt.Errorf("invalid RGBA frame size: got %d, expected %d", len(rgbaData), expectedSize)
	}
	return e.video.writeFrameRGBA(rgbaData, e.pkt, e.writeVideoPacket)
}

// WriteAudioSamples writes pre-decoded audio samples to the encoder.
// Samples should be float32, mono or stereo interleaved depending on AudioChannels config.
// For mono: just the samples. For stereo: L0, R0, L1, R1, ...
// This method handles FIFO buffering and encodes complete AAC frames.
func (e *Encoder) WriteAudioSamples(samples []float32) error {
	if e.audio == nil {
		return nil // No audio configured
	}
	return e.audio.writeSamples(samples, e.pkt, e.writeAudioPacket)
}

// FlushAudioEncoder flushes any remaining samples in the FIFO and encoder.
// Call this after all audio samples have been written.
func (e *Encoder) FlushAudioEncoder() error {
	if e.audio == nil {
		return nil // No audio configured
	}
	return e.audio.flush(e.pkt, e.writeAudioPacket)
}

// Close finalizes the output file and frees resources.
// Finalisation failures (flush, drain, trailer) are collected and returned as
// a joined error; resource freeing continues regardless. A second call is a
// no-op returning nil because freed handles are nilled on the first call.
func (e *Encoder) Close() error {
	var errs []error

	// Flush the video encoder before writing the trailer. A failed flush
	// truncates the output, so report it.
	if err := e.video.flush(e.pkt, e.writeVideoPacket); err != nil {
		errs = append(errs, err)
	}

	if e.formatCtx != nil {
		ret, err := ffmpeg.AVWriteTrailer(e.formatCtx)
		if err := checkFFmpeg(ret, err, "write trailer"); err != nil {
			errs = append(errs, err)
		}

		// A failed close can drop buffered writes after a successful trailer,
		// leaving a truncated file; surface it.
		if err := e.closeOutputFile(); err != nil {
			errs = append(errs, err)
		}
	}

	e.freeResources()

	return errors.Join(errs...)
}

func (e *Encoder) writeVideoPacket(pkt *ffmpeg.AVPacket) error {
	return e.writePacket(pkt, e.video.codec, e.video.stream, "write video packet")
}

func (e *Encoder) writeAudioPacket(pkt *ffmpeg.AVPacket) error {
	return e.writePacket(pkt, e.audio.codec, e.audio.stream, "write audio packet")
}

func (e *Encoder) writePacket(
	pkt *ffmpeg.AVPacket,
	codec *ffmpeg.AVCodecContext,
	stream *ffmpeg.AVStream,
	op string,
) error {
	pkt.SetStreamIndex(stream.Index())
	ffmpeg.AVPacketRescaleTs(pkt, codec.TimeBase(), stream.TimeBase())

	ret, err := ffmpeg.AVInterleavedWriteFrame(e.formatCtx, pkt)
	ffmpeg.AVPacketUnref(pkt)

	return checkFFmpeg(ret, err, op)
}

func (e *Encoder) closeOutputFile() error {
	if e.formatCtx == nil || e.formatCtx.Pb() == nil {
		return nil
	}
	ret, err := ffmpeg.AVIOClose(e.formatCtx.Pb())
	e.formatCtx.SetPb(nil)
	return checkFFmpeg(ret, err, "close output file")
}

func (e *Encoder) freeResources() {
	if e.audio != nil {
		e.audio.close()
		e.audio = nil
	}
	e.video.close()

	if e.formatCtx != nil {
		_ = e.closeOutputFile()
		ffmpeg.AVFormatFreeContext(e.formatCtx)
		e.formatCtx = nil
	}
	if e.pkt != nil {
		ffmpeg.AVPacketFree(&e.pkt)
		e.pkt = nil
	}
}
