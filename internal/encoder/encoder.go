package encoder

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"unsafe"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jivefire/internal/yuv"
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

// avAudioFIFO wraps FFmpeg's AVAudioFifo, confining the C handle and all
// plane-pointer marshalling behind this type so no consumer touches the C
// boundary directly. The FIFO is packed (AVSampleFmtFlt) to preserve the
// interleaved push contract; the planar split happens at drain (Task 2.3).
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
	if err != nil {
		return 0, fmt.Errorf("query audio FIFO size: %w", err)
	}
	if ret < 0 {
		return 0, fmt.Errorf("query audio FIFO size: %w", ffmpeg.WrapErr(ret))
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

// Encoder wraps FFmpeg encoding functionality
type Encoder struct {
	config Config

	// Output muxer (MP4 container)
	formatCtx *ffmpeg.AVFormatContext

	// Video stream and encoder
	videoStream *ffmpeg.AVStream
	videoCodec  *ffmpeg.AVCodecContext

	// Hardware acceleration (nil for software encoding)
	hwEncoder   *HWEncoder
	hwDeviceCtx *ffmpeg.AVBufferRef

	// Hardware frames context for GPU upload (Vulkan and QSV)
	hwFramesCtx *ffmpeg.AVBufferRef

	// Pre-allocated reusable NV12 frame for parallel Go conversion (Vulkan and QSV)
	hwNV12Frame *ffmpeg.AVFrame

	// Pre-allocated reusable software YUV420P frame (libx264 path)
	swYUVFrame *ffmpeg.AVFrame

	// Pre-allocated reusable RGBA frame (NVENC path)
	rgbaFrame *ffmpeg.AVFrame

	// Pre-allocated reusable packet for the video receive loop
	pkt *ffmpeg.AVPacket

	// Persistent worker pool for per-frame RGB→YUV row conversion
	rowPool *yuv.RowPool

	// Input pixel format (RGBA for NVENC, NV12 for Vulkan/QSV, YUV420P for software)
	inputPixFmt ffmpeg.AVPixelFormat

	// Audio stream and encoder
	audioStream   *ffmpeg.AVStream
	audioCodec    *ffmpeg.AVCodecContext
	audioEncFrame *ffmpeg.AVFrame
	audioFIFO     *avAudioFIFO // AVAudioFifo-backed FIFO for frame size adjustment (FFT needs 2048, AAC expects 1024)

	// Timestamp tracking
	nextVideoPts int64
	nextAudioPts int64
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

	return &Encoder{
		config:       config,
		nextVideoPts: 0,
		nextAudioPts: 0,
	}, nil
}

// Initialize sets up the FFmpeg encoder pipeline
func (e *Encoder) Initialize() (err error) {
	var ret int

	// Suppress FFmpeg log output so it does not corrupt the TUI.
	ffmpeg.AVLogSetLevel(ffmpeg.AVLogQuiet)

	// Persistent worker pool for per-frame RGB→YUV conversion. The row
	// partition never changes, so reuse long-lived workers across all frames.
	// Stop the workers if a later setup step fails, since the caller only
	// defers Close once Initialize returns successfully.
	e.rowPool = yuv.NewRowPool(e.config.Height)
	defer func() {
		if err != nil && e.rowPool != nil {
			e.rowPool.Close()
			e.rowPool = nil
		}
	}()

	outputPath := ffmpeg.ToCStr(e.config.OutputPath)
	defer outputPath.Free()

	ret, err = ffmpeg.AVFormatAllocOutputContext2(&e.formatCtx, nil, nil, outputPath)
	if err := checkFFmpeg(ret, err, "allocate output context"); err != nil {
		return err
	}

	// Select encoder based on hardware acceleration preference
	hwAccelType := e.config.HWAccel
	if hwAccelType == "" {
		hwAccelType = HWAccelAuto // Default to auto-detection
	}

	e.hwEncoder = SelectBestEncoder(hwAccelType)

	var codec *ffmpeg.AVCodec
	if e.hwEncoder != nil {
		// Use hardware encoder
		encoderName := ffmpeg.ToCStr(e.hwEncoder.Name)
		codec = ffmpeg.AVCodecFindEncoderByName(encoderName)
		encoderName.Free()
		if codec == nil {
			return fmt.Errorf("hardware encoder %s not found", e.hwEncoder.Name)
		}

		// Create hardware device context
		// For QSV on Linux with multiple GPUs, try common Intel render nodes
		var deviceCreated bool
		if e.hwEncoder.Type == HWAccelQSV {
			// Try specific Intel GPU render nodes first
			for _, device := range []string{"/dev/dri/renderD128", "/dev/dri/renderD129", ""} {
				var deviceCStr *ffmpeg.CStr
				if device != "" {
					deviceCStr = ffmpeg.ToCStr(device)
				}
				ret, err = ffmpeg.AVHWDeviceCtxCreate(&e.hwDeviceCtx, e.hwEncoder.DeviceType, deviceCStr, nil, 0)
				if deviceCStr != nil {
					deviceCStr.Free()
				}
				if err == nil && ret >= 0 {
					deviceCreated = true
					break
				}
			}
		} else {
			ret, err = ffmpeg.AVHWDeviceCtxCreate(&e.hwDeviceCtx, e.hwEncoder.DeviceType, nil, nil, 0)
			deviceCreated = (err == nil && ret >= 0)
		}

		if !deviceCreated {
			// Fall back to software if hardware init fails
			e.hwEncoder = nil
			e.hwDeviceCtx = nil
			codec = ffmpeg.AVCodecFindEncoder(ffmpeg.AVCodecIdH264)
			if codec == nil {
				return fmt.Errorf("H.264 encoder not found")
			}
		}
	} else {
		// Use software encoder (libx264)
		codec = ffmpeg.AVCodecFindEncoder(ffmpeg.AVCodecIdH264)
		if codec == nil {
			return fmt.Errorf("H.264 encoder not found")
		}
	}

	e.videoStream = ffmpeg.AVFormatNewStream(e.formatCtx, nil)
	if e.videoStream == nil {
		return fmt.Errorf("failed to create video stream")
	}
	e.videoStream.SetId(0)

	e.videoCodec = ffmpeg.AVCodecAllocContext3(codec)
	if e.videoCodec == nil {
		return fmt.Errorf("failed to allocate codec context")
	}

	e.videoCodec.SetWidth(e.config.Width)
	e.videoCodec.SetHeight(e.config.Height)

	if err := e.configurePixelFormat(); err != nil {
		return err
	}

	timeBase := ffmpeg.AVMakeQ(1, e.config.Framerate)
	e.videoCodec.SetTimeBase(timeBase)

	framerate := ffmpeg.AVMakeQ(e.config.Framerate, 1)
	e.videoCodec.SetFramerate(framerate)

	e.videoCodec.SetGopSize(e.config.Framerate * 2) // Keyframe every 2 seconds

	e.videoStream.SetTimeBase(timeBase)

	var opts *ffmpeg.AVDictionary
	defer ffmpeg.AVDictFree(&opts)

	if e.hwEncoder != nil {
		// Hardware encoder options
		e.setHWEncoderOptions(&opts)
	} else {
		// Software encoder (x264) options optimized for visualization content
		// CRF 24 = good quality for busy visualizations
		_, _ = ffmpeg.AVDictSet(&opts, ffmpeg.ToCStr("crf"), ffmpeg.ToCStr("24"), 0)
		// Faster preset prioritizes encoding speed
		_, _ = ffmpeg.AVDictSet(&opts, ffmpeg.ToCStr("preset"), ffmpeg.ToCStr("veryfast"), 0)
		// Tune for animation content
		_, _ = ffmpeg.AVDictSet(&opts, ffmpeg.ToCStr("tune"), ffmpeg.ToCStr("animation"), 0)
		// Main profile for faster encoding and broad compatibility
		_, _ = ffmpeg.AVDictSet(&opts, ffmpeg.ToCStr("profile"), ffmpeg.ToCStr("main"), 0)
		// Single reference frame (simple vertical bar motion doesn't need multiple refs)
		_, _ = ffmpeg.AVDictSet(&opts, ffmpeg.ToCStr("ref"), ffmpeg.ToCStr("1"), 0)
		// Reduce b-frames for faster encoding (predictable bar motion)
		_, _ = ffmpeg.AVDictSet(&opts, ffmpeg.ToCStr("bf"), ffmpeg.ToCStr("1"), 0)
		// Simpler subpixel motion estimation (bars move in discrete pixels)
		_, _ = ffmpeg.AVDictSet(&opts, ffmpeg.ToCStr("subme"), ffmpeg.ToCStr("4"), 0)
	}

	ret, err = ffmpeg.AVCodecOpen2(e.videoCodec, codec, &opts)
	if err := checkFFmpeg(ret, err, "open codec"); err != nil {
		return err
	}

	ret, err = ffmpeg.AVCodecParametersFromContext(e.videoStream.Codecpar(), e.videoCodec)
	if err := checkFFmpeg(ret, err, "copy codec parameters"); err != nil {
		return err
	}

	var pb *ffmpeg.AVIOContext
	ret, err = ffmpeg.AVIOOpen(&pb, outputPath, ffmpeg.AVIOFlagWrite)
	if err := checkFFmpeg(ret, err, "open output file"); err != nil {
		return err
	}
	e.formatCtx.SetPb(pb)

	if e.config.SampleRate > 0 {
		if err := e.initializeAudioEncoder(); err != nil {
			return fmt.Errorf("failed to initialize audio encoder: %w", err)
		}
	}

	ret, err = ffmpeg.AVFormatWriteHeader(e.formatCtx, nil)
	if err := checkFFmpeg(ret, err, "write header"); err != nil {
		return err
	}

	return nil
}

// setHWEncoderOptions configures encoder-specific options for hardware encoders
func (e *Encoder) setHWEncoderOptions(opts **ffmpeg.AVDictionary) {
	if e.hwEncoder == nil {
		return
	}

	switch e.hwEncoder.Type {
	case HWAccelNVENC:
		// NVENC options optimized for fast visualisation encoding
		// Preset p1 = fastest encoding (scale runs p1=fastest to p7=slowest)
		_, _ = ffmpeg.AVDictSet(opts, ffmpeg.ToCStr("preset"), ffmpeg.ToCStr("p1"), 0)
		// Low latency tuning - reduces pipeline delay
		_, _ = ffmpeg.AVDictSet(opts, ffmpeg.ToCStr("tune"), ffmpeg.ToCStr("ull"), 0)
		// Target quality (CQ mode) - similar to CRF, lower=better (0-51)
		_, _ = ffmpeg.AVDictSet(opts, ffmpeg.ToCStr("rc"), ffmpeg.ToCStr("vbr"), 0)
		_, _ = ffmpeg.AVDictSet(opts, ffmpeg.ToCStr("cq"), ffmpeg.ToCStr("24"), 0)
		// Main profile for broad compatibility
		_, _ = ffmpeg.AVDictSet(opts, ffmpeg.ToCStr("profile"), ffmpeg.ToCStr("main"), 0)
		// No B-frames for faster encoding (visualisation has low motion)
		_, _ = ffmpeg.AVDictSet(opts, ffmpeg.ToCStr("bf"), ffmpeg.ToCStr("0"), 0)
		// Zero latency mode - no reordering delay
		_, _ = ffmpeg.AVDictSet(opts, ffmpeg.ToCStr("zerolatency"), ffmpeg.ToCStr("1"), 0)

	case HWAccelQSV:
		// Intel Quick Sync Video options
		_, _ = ffmpeg.AVDictSet(opts, ffmpeg.ToCStr("preset"), ffmpeg.ToCStr("medium"), 0)
		_, _ = ffmpeg.AVDictSet(opts, ffmpeg.ToCStr("global_quality"), ffmpeg.ToCStr("24"), 0)
		_, _ = ffmpeg.AVDictSet(opts, ffmpeg.ToCStr("profile"), ffmpeg.ToCStr("main"), 0)

	case HWAccelVulkan:
		// Vulkan Video options optimized for fast visualisation encoding
		_, _ = ffmpeg.AVDictSet(opts, ffmpeg.ToCStr("content"), ffmpeg.ToCStr("rendered"), 0)
		// Quality level (0-51, lower=better) - same as NVENC CQ
		_, _ = ffmpeg.AVDictSet(opts, ffmpeg.ToCStr("qp"), ffmpeg.ToCStr("24"), 0)
		// Low latency tuning - reduces pipeline delay
		_, _ = ffmpeg.AVDictSet(opts, ffmpeg.ToCStr("tune"), ffmpeg.ToCStr("ull"), 0)
		// Increase async depth for more parallelism (default=2)
		_, _ = ffmpeg.AVDictSet(opts, ffmpeg.ToCStr("async_depth"), ffmpeg.ToCStr("4"), 0)
		// Main profile for broad compatibility
		_, _ = ffmpeg.AVDictSet(opts, ffmpeg.ToCStr("profile"), ffmpeg.ToCStr("main"), 0)
		// Minimal B-frame depth (1 is minimum)
		_, _ = ffmpeg.AVDictSet(opts, ffmpeg.ToCStr("b_depth"), ffmpeg.ToCStr("1"), 0)

	case HWAccelVAAPI:
		// VA-API options optimized for fast visualisation encoding
		// Quality level (1-51, lower=better) - CQP rate control
		_, _ = ffmpeg.AVDictSet(opts, ffmpeg.ToCStr("qp"), ffmpeg.ToCStr("24"), 0)
		// Main profile for broad compatibility
		_, _ = ffmpeg.AVDictSet(opts, ffmpeg.ToCStr("profile"), ffmpeg.ToCStr("main"), 0)
		// Low latency: disable B-frames for faster encoding
		_, _ = ffmpeg.AVDictSet(opts, ffmpeg.ToCStr("bf"), ffmpeg.ToCStr("0"), 0)

	case HWAccelVideoToolbox:
		// Apple VideoToolbox options optimised for fast visualisation encoding
		// Note: VideoToolbox does not support constant quality (CRF/CQ) encoding.
		// It uses bitrate-based rate control only, so we cannot set quality levels
		// like other hardware encoders. The encoder will use default VBR settings.
		_, _ = ffmpeg.AVDictSet(opts, ffmpeg.ToCStr("profile"), ffmpeg.ToCStr("main"), 0)
		_, _ = ffmpeg.AVDictSet(opts, ffmpeg.ToCStr("level"), ffmpeg.ToCStr("4.1"), 0)
		// Real-time encoding hint - prioritises speed for live/visualisation use
		_, _ = ffmpeg.AVDictSet(opts, ffmpeg.ToCStr("realtime"), ffmpeg.ToCStr("1"), 0)
		// Require hardware encoding - fail if hardware unavailable
		_, _ = ffmpeg.AVDictSet(opts, ffmpeg.ToCStr("allow_sw"), ffmpeg.ToCStr("0"), 0)
	}
}

// setupHWFramesContext creates and configures the hardware frames context
// required for Vulkan and QSV video encoding. These encoders require frames to
// be uploaded to GPU memory before encoding, using NV12 format as the software
// pixel format.
func (e *Encoder) setupHWFramesContext(hwPixFmt ffmpeg.AVPixelFormat) error {
	if e.hwDeviceCtx == nil {
		return fmt.Errorf("hardware device context not available")
	}

	// Allocate hardware frames context from the device context
	hwFramesRef := ffmpeg.AVHWFrameCtxAlloc(e.hwDeviceCtx)
	if hwFramesRef == nil {
		return fmt.Errorf("failed to allocate hardware frames context")
	}
	e.hwFramesCtx = hwFramesRef

	// Cast the data pointer to AVHWFramesContext for configuration
	framesCtx := ffmpeg.ToAVHWFramesContext(hwFramesRef.Data())
	if framesCtx == nil {
		return fmt.Errorf("failed to get hardware frames context")
	}

	// Configure the frames context for hardware encoding (Vulkan or QSV)
	framesCtx.SetFormat(hwPixFmt)              // Hardware format (AVPixFmtVulkan or AVPixFmtQsv)
	framesCtx.SetSwFormat(ffmpeg.AVPixFmtNv12) // Software format for upload
	framesCtx.SetWidth(e.config.Width)
	framesCtx.SetHeight(e.config.Height)
	framesCtx.SetInitialPoolSize(20) // Pool size for frame reuse

	// Initialize the frames context
	ret, err := ffmpeg.AVHWFrameCtxInit(hwFramesRef)
	if err := checkFFmpeg(ret, err, "initialize hardware frames context"); err != nil {
		return err
	}

	// Attach frames context to the video encoder
	e.videoCodec.SetHwFramesCtx(ffmpeg.AVBufferRef_(hwFramesRef))

	// Pre-allocate reusable NV12 frame for parallel Go RGBA→NV12 conversion
	e.hwNV12Frame = ffmpeg.AVFrameAlloc()
	if e.hwNV12Frame == nil {
		return fmt.Errorf("failed to allocate reusable NV12 frame")
	}
	e.hwNV12Frame.SetWidth(e.config.Width)
	e.hwNV12Frame.SetHeight(e.config.Height)
	e.hwNV12Frame.SetFormat(int(ffmpeg.AVPixFmtNv12))

	ret, err = ffmpeg.AVFrameGetBuffer(e.hwNV12Frame, 0)
	if err := checkFFmpeg(ret, err, "allocate NV12 buffer"); err != nil {
		return err
	}

	return nil
}

// configurePixelFormat sets up pixel formats and hardware context based on encoder type.
// NVENC: accepts RGBA directly, GPU does colourspace conversion
// Vulkan/QSV/VA-API: require NV12 uploaded to GPU via hardware frames context
// Software: uses YUV420P with CPU-side RGB→YUV conversion
func (e *Encoder) configurePixelFormat() error {
	// Pre-allocate reusable packet for the video receive loop
	e.pkt = ffmpeg.AVPacketAlloc()
	if e.pkt == nil {
		return fmt.Errorf("failed to allocate reusable packet")
	}

	if e.hwEncoder == nil {
		// Software encoder (libx264)
		e.inputPixFmt = ffmpeg.AVPixFmtYuv420P
		e.videoCodec.SetPixFmt(ffmpeg.AVPixFmtYuv420P)

		// Pre-allocate reusable YUV420P frame for CPU-side conversion
		e.swYUVFrame = ffmpeg.AVFrameAlloc()
		if e.swYUVFrame == nil {
			return fmt.Errorf("failed to allocate reusable YUV frame")
		}
		e.swYUVFrame.SetWidth(e.config.Width)
		e.swYUVFrame.SetHeight(e.config.Height)
		e.swYUVFrame.SetFormat(int(ffmpeg.AVPixFmtYuv420P))

		ret, err := ffmpeg.AVFrameGetBuffer(e.swYUVFrame, 0)
		if err := checkFFmpeg(ret, err, "allocate YUV buffer"); err != nil {
			return err
		}
		return nil
	}

	switch e.hwEncoder.Type {
	case HWAccelNVENC:
		// NVENC can accept RGBA directly - GPU handles colourspace conversion
		e.inputPixFmt = ffmpeg.AVPixFmtRgba
		e.videoCodec.SetPixFmt(ffmpeg.AVPixFmtRgba)
		e.videoCodec.SetHwDeviceCtx(ffmpeg.AVBufferRef_(e.hwDeviceCtx))

		// Pre-allocate reusable RGBA frame
		e.rgbaFrame = ffmpeg.AVFrameAlloc()
		if e.rgbaFrame == nil {
			return fmt.Errorf("failed to allocate reusable RGBA frame")
		}
		e.rgbaFrame.SetWidth(e.config.Width)
		e.rgbaFrame.SetHeight(e.config.Height)
		e.rgbaFrame.SetFormat(int(ffmpeg.AVPixFmtRgba))

		ret, err := ffmpeg.AVFrameGetBuffer(e.rgbaFrame, 0)
		if err := checkFFmpeg(ret, err, "allocate RGBA buffer"); err != nil {
			return err
		}

	case HWAccelVulkan:
		// Vulkan requires hardware frames context with NV12 software format
		e.inputPixFmt = ffmpeg.AVPixFmtNv12
		e.videoCodec.SetPixFmt(ffmpeg.AVPixFmtVulkan)
		if err := e.setupHWFramesContext(ffmpeg.AVPixFmtVulkan); err != nil {
			return fmt.Errorf("failed to setup Vulkan frames context: %w", err)
		}

	case HWAccelQSV:
		// QSV requires hardware frames context with NV12 software format
		e.inputPixFmt = ffmpeg.AVPixFmtNv12
		e.videoCodec.SetPixFmt(ffmpeg.AVPixFmtQsv)
		if err := e.setupHWFramesContext(ffmpeg.AVPixFmtQsv); err != nil {
			return fmt.Errorf("failed to setup QSV frames context: %w", err)
		}

	case HWAccelVAAPI:
		// VA-API requires hardware frames context with NV12 software format
		e.inputPixFmt = ffmpeg.AVPixFmtNv12
		e.videoCodec.SetPixFmt(ffmpeg.AVPixFmtVaapi)
		if err := e.setupHWFramesContext(ffmpeg.AVPixFmtVaapi); err != nil {
			return fmt.Errorf("failed to setup VA-API frames context: %w", err)
		}

	case HWAccelVideoToolbox:
		// VideoToolbox requires hardware frames context with NV12 software format
		e.inputPixFmt = ffmpeg.AVPixFmtNv12
		e.videoCodec.SetPixFmt(ffmpeg.AVPixFmtVideotoolbox)
		if err := e.setupHWFramesContext(ffmpeg.AVPixFmtVideotoolbox); err != nil {
			return fmt.Errorf("failed to setup VideoToolbox frames context: %w", err)
		}

	default:
		return fmt.Errorf("unsupported hardware encoder type: %s", e.hwEncoder.Type)
	}

	return nil
}

// EncoderName returns the name of the video encoder being used
func (e *Encoder) EncoderName() string {
	if e.hwEncoder != nil {
		return e.hwEncoder.Name
	}
	return "libx264"
}

// IsHardware reports whether encoding ran on a hardware-backed encoder.
func (e *Encoder) IsHardware() bool {
	return e.hwEncoder != nil
}

// outputChannels returns the configured audio channel count, defaulting to mono.
func (e *Encoder) outputChannels() int {
	if e.config.AudioChannels == 0 {
		return 1
	}
	return e.config.AudioChannels
}

// initializeAudioEncoder sets up the AAC encoder for direct sample input.
// Samples are provided via WriteAudioSamples().
// Requires SampleRate to be set in Config.
func (e *Encoder) initializeAudioEncoder() error {
	audioEncoder := ffmpeg.AVCodecFindEncoder(ffmpeg.AVCodecIdAac)
	if audioEncoder == nil {
		return fmt.Errorf("AAC encoder not found")
	}

	e.audioStream = ffmpeg.AVFormatNewStream(e.formatCtx, nil)
	if e.audioStream == nil {
		return fmt.Errorf("failed to create audio stream")
	}
	e.audioStream.SetId(1)

	e.audioCodec = ffmpeg.AVCodecAllocContext3(audioEncoder)
	if e.audioCodec == nil {
		return fmt.Errorf("failed to allocate audio encoder context")
	}

	// Configure AAC encoder using config sample rate
	e.audioCodec.SetSampleFmt(ffmpeg.AVSampleFmtFltp) // AAC requires float planar
	e.audioCodec.SetSampleRate(e.config.SampleRate)

	outputChannels := e.outputChannels()
	ffmpeg.AVChannelLayoutDefault(e.audioCodec.ChLayout(), outputChannels)

	e.audioCodec.SetBitRate(192000) // 192 kbps
	e.audioStream.SetTimeBase(ffmpeg.AVMakeQ(1, e.audioCodec.SampleRate()))

	ret, err := ffmpeg.AVCodecOpen2(e.audioCodec, audioEncoder, nil)
	if err := checkFFmpeg(ret, err, "open audio encoder"); err != nil {
		return err
	}

	ret, err = ffmpeg.AVCodecParametersFromContext(e.audioStream.Codecpar(), e.audioCodec)
	if err := checkFFmpeg(ret, err, "copy audio encoder parameters"); err != nil {
		return err
	}

	e.audioEncFrame = ffmpeg.AVFrameAlloc()
	if e.audioEncFrame == nil {
		return fmt.Errorf("failed to allocate audio encoder frame")
	}

	// AVAudioFifo-backed FIFO (packed float32) bridges the FFT chunk size
	// (2048) to the AAC encoder frame size (1024).
	audioFIFO, err := newAVAudioFIFO(outputChannels, e.audioCodec.FrameSize())
	if err != nil {
		return err
	}
	e.audioFIFO = audioFIFO

	e.audioEncFrame.SetNbSamples(e.audioCodec.FrameSize())
	e.audioEncFrame.SetFormat(int(ffmpeg.AVSampleFmtFltp))
	ffmpeg.AVChannelLayoutDefault(e.audioEncFrame.ChLayout(), outputChannels)
	e.audioEncFrame.SetSampleRate(e.audioCodec.SampleRate())

	ret, err = ffmpeg.AVFrameGetBuffer(e.audioEncFrame, 0)
	if err := checkFFmpeg(ret, err, "allocate encoder frame buffer"); err != nil {
		return err
	}

	return nil
}

// WriteFrameRGBA encodes and writes a single RGBA frame
// For NVENC: sends RGBA directly to GPU (colourspace conversion on GPU)
// For Vulkan: converts RGBA→NV12 on CPU, uploads to GPU via hwframe
// For software: converts to RGB24→YUV420P on CPU then encodes
func (e *Encoder) WriteFrameRGBA(rgbaData []byte) error {
	// Validate frame size
	expectedSize := e.config.Width * e.config.Height * 4 // RGBA = 4 bytes per pixel
	if len(rgbaData) != expectedSize {
		return fmt.Errorf("invalid RGBA frame size: got %d, expected %d", len(rgbaData), expectedSize)
	}

	// For NVENC, send RGBA directly - GPU does colourspace conversion
	if e.inputPixFmt == ffmpeg.AVPixFmtRgba {
		return e.writeFrameRGBADirect(rgbaData)
	}

	// For Vulkan/QSV/VAAPI/VideoToolbox, convert RGBA→NV12 then upload to GPU;
	// configurePixelFormat sets NV12 for exactly those hardware encoders.
	if e.inputPixFmt == ffmpeg.AVPixFmtNv12 {
		return e.writeFrameHWUpload(rgbaData)
	}

	// For software encoder, convert RGBA directly to YUV420P (skipping RGB24 intermediate)
	return e.writeFrameRGBASoftware(rgbaData)
}

// writeFrameRGBASoftware converts RGBA directly to YUV420P and encodes.
// This avoids the intermediate RGB24 buffer allocation for ~3% faster software encoding.
func (e *Encoder) writeFrameRGBASoftware(rgbaData []byte) error {
	// Use pre-allocated YUV frame (configured in configurePixelFormat).
	// Make writable as the encoder may still hold a reference from the previous frame.
	yuvFrame := e.swYUVFrame
	if ret, err := ffmpeg.AVFrameMakeWritable(yuvFrame); err != nil {
		return checkFFmpeg(ret, err, "make YUV frame writable")
	}

	// Convert RGBA directly to YUV420P (skips RGB24 intermediate)
	convertRGBAToYUV(e.rowPool, rgbaData, yuvFrame, e.config.Width)

	// Set presentation timestamp
	yuvFrame.SetPts(e.nextVideoPts)
	e.nextVideoPts++

	// Send frame to encoder
	ret, err := ffmpeg.AVCodecSendFrame(e.videoCodec, yuvFrame)
	if err := checkFFmpeg(ret, err, "send frame to encoder"); err != nil {
		return err
	}

	// Receive and write encoded packets
	return e.receiveAndWriteVideoPackets()
}

// writeFrameRGBADirect sends RGBA frame directly to hardware encoder
// This avoids CPU-side colourspace conversion - GPU handles it
func (e *Encoder) writeFrameRGBADirect(rgbaData []byte) error {
	// Use pre-allocated RGBA frame (configured in configurePixelFormat).
	// Make writable as the encoder may still hold a reference from the previous frame.
	rgbaFrame := e.rgbaFrame
	if ret, err := ffmpeg.AVFrameMakeWritable(rgbaFrame); err != nil {
		return checkFFmpeg(ret, err, "make RGBA frame writable")
	}

	// Copy RGBA data to frame
	width := e.config.Width
	height := e.config.Height
	linesize := rgbaFrame.Linesize().Get(0)
	data := rgbaFrame.Data().Get(0)

	// Copy row by row (frame may have padding)
	srcStride := width * 4
	for y := range height {
		srcOffset := y * srcStride
		dstOffset := y * linesize
		copy(unsafe.Slice((*byte)(unsafe.Add(data, dstOffset)), srcStride), //nolint:gosec // offset is within allocated frame
			rgbaData[srcOffset:srcOffset+srcStride])
	}

	// Set presentation timestamp
	rgbaFrame.SetPts(e.nextVideoPts)
	e.nextVideoPts++

	// Send frame to encoder
	ret, err := ffmpeg.AVCodecSendFrame(e.videoCodec, rgbaFrame)
	if err := checkFFmpeg(ret, err, "send frame to encoder"); err != nil {
		return err
	}

	// Receive and write encoded packets
	return e.receiveAndWriteVideoPackets()
}

// writeFrameHWUpload converts RGBA to NV12, uploads to GPU, and encodes
// Pipeline: RGBA (CPU) → parallel Go conversion → NV12 (CPU) → AVHWFrameTransferData → GPU → encode
// Used by Vulkan (h264_vulkan) and QSV (h264_qsv) encoders
// Uses pre-allocated reusable NV12 frame and parallel Go conversion (8.4× faster than SwsScaleFrame)
func (e *Encoder) writeFrameHWUpload(rgbaData []byte) error {
	width := e.config.Width

	// Use pre-allocated NV12 frame (already configured in setupHWFramesContext)
	nv12Frame := e.hwNV12Frame

	// Convert RGBA → NV12 using parallel Go conversion (much faster than SwsScaleFrame)
	convertRGBAToNV12(e.rowPool, rgbaData, nv12Frame, width)

	// Allocate hardware frame from pool
	// Note: hwFrame must be allocated per-call as it's returned to pool after encoding
	hwFrame := ffmpeg.AVFrameAlloc()
	if hwFrame == nil {
		return fmt.Errorf("failed to allocate hardware frame")
	}
	defer ffmpeg.AVFrameFree(&hwFrame)

	// Get buffer from hardware frames context pool
	ret, err := ffmpeg.AVHWFrameGetBuffer(e.hwFramesCtx, hwFrame, 0)
	if err := checkFFmpeg(ret, err, "get hardware frame buffer"); err != nil {
		return err
	}

	// Upload NV12 frame to GPU memory
	ret, err = ffmpeg.AVHWFrameTransferData(hwFrame, nv12Frame, 0)
	if err := checkFFmpeg(ret, err, "upload frame to GPU"); err != nil {
		return err
	}

	// Copy frame properties for encoder
	hwFrame.SetPts(e.nextVideoPts)
	e.nextVideoPts++

	// Send hardware frame to encoder
	ret, err = ffmpeg.AVCodecSendFrame(e.videoCodec, hwFrame)
	if err := checkFFmpeg(ret, err, "send frame to hardware encoder"); err != nil {
		return err
	}

	// Receive and write encoded packets
	return e.receiveAndWriteVideoPackets()
}

// receiveAndWriteVideoPackets receives encoded packets from video codec and writes to output
func (e *Encoder) receiveAndWriteVideoPackets() error {
	pkt := e.pkt
	for {
		_, err := ffmpeg.AVCodecReceivePacket(e.videoCodec, pkt)
		if err != nil {
			// EAGAIN and EOF are expected - means no more packets available
			if errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
				break
			}
			return fmt.Errorf("receive packet: %w", err)
		}

		// Set stream index and rescale timestamps
		pkt.SetStreamIndex(e.videoStream.Index())
		ffmpeg.AVPacketRescaleTs(pkt, e.videoCodec.TimeBase(), e.videoStream.TimeBase())

		// Write packet to output. AVInterleavedWriteFrame consumes the packet's
		// reference; unref afterwards to reset it for reuse on the next iteration.
		ret, err := ffmpeg.AVInterleavedWriteFrame(e.formatCtx, pkt)
		ffmpeg.AVPacketUnref(pkt)

		if err := checkFFmpeg(ret, err, "write packet"); err != nil {
			return err
		}
	}

	return nil
}

// receiveAndWriteAudioPackets receives encoded packets from the audio codec and
// writes them to the output. Reuses the shared e.pkt packet; safe because the
// encoder is single-goroutine and the video and audio receive loops never run
// concurrently. Write errors are propagated, not swallowed.
func (e *Encoder) receiveAndWriteAudioPackets() error {
	pkt := e.pkt
	for {
		_, err := ffmpeg.AVCodecReceivePacket(e.audioCodec, pkt)
		if err != nil {
			// EAGAIN and EOF are expected - means no more packets available
			if errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
				break
			}
			return fmt.Errorf("receive audio packet from encoder: %w", err)
		}

		// Set stream index and rescale timestamps
		pkt.SetStreamIndex(e.audioStream.Index())
		ffmpeg.AVPacketRescaleTs(pkt, e.audioCodec.TimeBase(), e.audioStream.TimeBase())

		// Write packet to output. AVInterleavedWriteFrame consumes the packet's
		// reference; unref afterwards to reset it for reuse on the next iteration.
		ret, err := ffmpeg.AVInterleavedWriteFrame(e.formatCtx, pkt)
		ffmpeg.AVPacketUnref(pkt)

		if err := checkFFmpeg(ret, err, "write audio packet"); err != nil {
			return err
		}
	}

	return nil
}

// channelLayoutName returns the human-readable name for a channel count.
func channelLayoutName(channels int) string {
	if channels == 2 {
		return "stereo"
	}
	return "mono"
}

// WriteAudioSamples writes pre-decoded audio samples to the encoder.
// Samples should be float32, mono or stereo interleaved depending on AudioChannels config.
// For mono: just the samples. For stereo: L0, R0, L1, R1, ...
// This method handles FIFO buffering and encodes complete AAC frames.
func (e *Encoder) WriteAudioSamples(samples []float32) error {
	if e.audioCodec == nil {
		return nil // No audio configured
	}

	encoderFrameSize := e.audioCodec.FrameSize() // 1024 for AAC
	outputChannels := e.outputChannels()

	if err := e.audioFIFO.write(samples); err != nil {
		return err
	}

	// Drain the FIFO one encoder frame at a time. AVAudioFifoSize reports
	// samples-per-channel, so compare against encoderFrameSize (1024), not
	// encoderFrameSize*channels.
	for {
		size, err := e.audioFIFO.size()
		if err != nil {
			return err
		}
		if size < encoderFrameSize {
			break
		}

		frameSamples, err := e.audioFIFO.read(encoderFrameSize)
		if err != nil {
			return err
		}

		_, _ = ffmpeg.AVFrameMakeWritable(e.audioEncFrame)

		var writeErr error
		if outputChannels == 2 {
			writeErr = writeStereoFloats(e.audioEncFrame, frameSamples)
		} else {
			writeErr = writeMonoFloats(e.audioEncFrame, frameSamples)
		}

		if writeErr != nil {
			return fmt.Errorf("failed to write %s samples: %w",
				channelLayoutName(outputChannels), writeErr)
		}

		e.audioEncFrame.SetPts(e.nextAudioPts)
		e.nextAudioPts += int64(encoderFrameSize)

		ret, err := ffmpeg.AVCodecSendFrame(e.audioCodec, e.audioEncFrame)
		if err := checkFFmpeg(ret, err, "send audio frame to encoder"); err != nil {
			return err
		}

		if err := e.receiveAndWriteAudioPackets(); err != nil {
			return err
		}
	}

	return nil
}

// FlushAudioEncoder flushes any remaining samples in the FIFO and encoder.
// Call this after all audio samples have been written.
func (e *Encoder) FlushAudioEncoder() error {
	if e.audioCodec == nil {
		return nil // No audio configured
	}

	encoderFrameSize := e.audioCodec.FrameSize()
	outputChannels := e.outputChannels()

	// Drain any residual partial frame (< encoderFrameSize samples-per-channel)
	// from the AVAudioFifo and zero-pad it to a full encoder frame. The residual
	// now lives in e.audioFIFO since WriteAudioSamples routes through it.
	remaining, err := e.audioFIFO.size()
	if err != nil {
		return err
	}
	if remaining > 0 {
		samplesPerFrame := encoderFrameSize * outputChannels
		frameSamples := make([]float32, samplesPerFrame)
		partialSamples, err := e.audioFIFO.read(remaining)
		if err != nil {
			return err
		}
		copy(frameSamples, partialSamples)

		_, _ = ffmpeg.AVFrameMakeWritable(e.audioEncFrame)

		var writeErr error
		if outputChannels == 2 {
			writeErr = writeStereoFloats(e.audioEncFrame, frameSamples)
		} else {
			writeErr = writeMonoFloats(e.audioEncFrame, frameSamples)
		}

		if writeErr != nil {
			return fmt.Errorf("failed to write final samples: %w", writeErr)
		}

		e.audioEncFrame.SetPts(e.nextAudioPts)
		e.nextAudioPts += int64(encoderFrameSize)

		ret, err := ffmpeg.AVCodecSendFrame(e.audioCodec, e.audioEncFrame)
		if err := checkFFmpeg(ret, err, "send final audio frame"); err != nil {
			return err
		}
	}

	// Send a NULL frame to enter draining mode.
	_, _ = ffmpeg.AVCodecSendFrame(e.audioCodec, nil)

	return e.receiveAndWriteAudioPackets()
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

// Close finalizes the output file and frees resources.
func (e *Encoder) Close() error {
	// Flush the video encoder before writing the trailer.
	if e.videoCodec != nil && e.pkt != nil {
		_, _ = ffmpeg.AVCodecSendFrame(e.videoCodec, nil)

		// Drain remaining packets, reusing the shared e.pkt (freed once below).
		pkt := e.pkt
		for {
			ret, err := ffmpeg.AVCodecReceivePacket(e.videoCodec, pkt)

			if errors.Is(err, ffmpeg.AVErrorEOF) || errors.Is(err, ffmpeg.EAgain) {
				break
			}

			if ret >= 0 {
				pkt.SetStreamIndex(e.videoStream.Index())
				ffmpeg.AVPacketRescaleTs(pkt, e.videoCodec.TimeBase(), e.videoStream.TimeBase())
				_, _ = ffmpeg.AVInterleavedWriteFrame(e.formatCtx, pkt)
				ffmpeg.AVPacketUnref(pkt)
			}
		}
	}

	if e.formatCtx != nil {
		_, _ = ffmpeg.AVWriteTrailer(e.formatCtx)

		if e.formatCtx.Pb() != nil {
			ffmpeg.AVIOClose(e.formatCtx.Pb())
		}
	}

	if e.videoCodec != nil {
		ffmpeg.AVCodecFreeContext(&e.videoCodec)
	}
	if e.audioCodec != nil {
		ffmpeg.AVCodecFreeContext(&e.audioCodec)
	}

	if e.audioEncFrame != nil {
		ffmpeg.AVFrameFree(&e.audioEncFrame)
	}

	if e.audioFIFO != nil {
		e.audioFIFO.free()
		e.audioFIFO = nil
	}

	if e.hwDeviceCtx != nil {
		ffmpeg.AVBufferUnref(&e.hwDeviceCtx)
		e.hwDeviceCtx = nil
	}

	if e.hwFramesCtx != nil {
		ffmpeg.AVBufferUnref(&e.hwFramesCtx)
		e.hwFramesCtx = nil
	}
	if e.hwNV12Frame != nil {
		ffmpeg.AVFrameFree(&e.hwNV12Frame)
		e.hwNV12Frame = nil
	}

	if e.swYUVFrame != nil {
		ffmpeg.AVFrameFree(&e.swYUVFrame)
		e.swYUVFrame = nil
	}

	if e.rgbaFrame != nil {
		ffmpeg.AVFrameFree(&e.rgbaFrame)
		e.rgbaFrame = nil
	}

	if e.pkt != nil {
		ffmpeg.AVPacketFree(&e.pkt)
		e.pkt = nil
	}

	if e.rowPool != nil {
		e.rowPool.Close()
		e.rowPool = nil
	}

	if e.formatCtx != nil {
		ffmpeg.AVFormatFreeContext(e.formatCtx)
		e.formatCtx = nil
	}

	return nil
}
