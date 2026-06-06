package encoder

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"sync"
	"unsafe"

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

// AudioFIFO provides a simple FIFO buffer for audio samples with pooled slice allocation.
type AudioFIFO struct {
	buffer    []float32
	frameSize int        // Samples per frame (e.g., 1024 for mono AAC, 2048 for stereo)
	pool      *sync.Pool // Pooled slices for this frame size
}

// NewAudioFIFO creates a new audio FIFO buffer with pooled allocation for the given frame size.
// frameSize should be encoderFrameSize * channels (e.g., 1024 for mono AAC, 2048 for stereo).
func NewAudioFIFO(frameSize int) *AudioFIFO {
	return &AudioFIFO{
		buffer:    make([]float32, 0, 4096), // Start with reasonable capacity
		frameSize: frameSize,
		pool: &sync.Pool{
			New: func() any {
				s := make([]float32, frameSize)
				return &s
			},
		},
	}
}

// Push adds samples to the FIFO
func (f *AudioFIFO) Push(samples []float32) {
	f.buffer = append(f.buffer, samples...)
}

// Pop removes and returns the requested number of samples.
// Returns nil if not enough samples available.
// For the configured frame size, returns a pooled slice.
// Caller MUST call ReturnSlice() when done to return it to the pool.
func (f *AudioFIFO) Pop(count int) []float32 {
	if len(f.buffer) < count {
		return nil
	}

	var result []float32

	// Use pooled slice for configured frame size
	if count == f.frameSize {
		result = *f.pool.Get().(*[]float32)
	} else {
		// Fallback to allocation for non-standard sizes (e.g., partial frames at flush)
		result = make([]float32, count)
	}

	copy(result, f.buffer[:count])

	// Shift remaining samples
	copy(f.buffer, f.buffer[count:])
	f.buffer = f.buffer[:len(f.buffer)-count]

	return result
}

// ReturnSlice returns a slice to the pool after use.
// Call this after processing each slice returned by Pop().
// Safe to call with nil or non-pooled slices (will be no-op).
func (f *AudioFIFO) ReturnSlice(s []float32) {
	if s == nil || cap(s) != f.frameSize {
		return
	}
	f.pool.Put(&s)
}

// Available returns the number of samples in the buffer
func (f *AudioFIFO) Available() int {
	return len(f.buffer)
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

	// Input pixel format (RGBA for NVENC, NV12 for Vulkan/QSV, YUV420P for software)
	inputPixFmt ffmpeg.AVPixelFormat

	// Audio stream and encoder
	audioStream   *ffmpeg.AVStream
	audioCodec    *ffmpeg.AVCodecContext
	audioEncFrame *ffmpeg.AVFrame
	audioFIFO     *AudioFIFO // FIFO for frame size adjustment

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
func (e *Encoder) Initialize() error {
	var ret int

	// Suppress FFmpeg log output to prevent interference with TUI
	ffmpeg.AVLogSetLevel(ffmpeg.AVLogQuiet)

	// Convert Go string to C string
	outputPath := ffmpeg.ToCStr(e.config.OutputPath)
	defer outputPath.Free()

	// Allocate output format context
	ret, err := ffmpeg.AVFormatAllocOutputContext2(&e.formatCtx, nil, nil, outputPath)
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

	// Create video stream
	e.videoStream = ffmpeg.AVFormatNewStream(e.formatCtx, nil)
	if e.videoStream == nil {
		return fmt.Errorf("failed to create video stream")
	}
	e.videoStream.SetId(0)

	// Allocate codec context
	e.videoCodec = ffmpeg.AVCodecAllocContext3(codec)
	if e.videoCodec == nil {
		return fmt.Errorf("failed to allocate codec context")
	}

	// Configure video encoder for YouTube compatibility
	e.videoCodec.SetWidth(e.config.Width)
	e.videoCodec.SetHeight(e.config.Height)

	// Configure pixel format and hardware context based on encoder type
	if err := e.configurePixelFormat(); err != nil {
		return err
	}

	// Set time base (1/framerate)
	timeBase := ffmpeg.AVMakeQ(1, e.config.Framerate)
	e.videoCodec.SetTimeBase(timeBase)

	// Set framerate
	framerate := ffmpeg.AVMakeQ(e.config.Framerate, 1)
	e.videoCodec.SetFramerate(framerate)

	e.videoCodec.SetGopSize(e.config.Framerate * 2) // Keyframe every 2 seconds

	// Set stream timebase
	e.videoStream.SetTimeBase(timeBase)

	// Set encoding options based on encoder type
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

	// Open codec with options
	ret, err = ffmpeg.AVCodecOpen2(e.videoCodec, codec, &opts)
	if err := checkFFmpeg(ret, err, "open codec"); err != nil {
		return err
	}

	// Copy codec parameters to stream
	ret, err = ffmpeg.AVCodecParametersFromContext(e.videoStream.Codecpar(), e.videoCodec)
	if err := checkFFmpeg(ret, err, "copy codec parameters"); err != nil {
		return err
	}

	// Open output file
	var pb *ffmpeg.AVIOContext
	ret, err = ffmpeg.AVIOOpen(&pb, outputPath, ffmpeg.AVIOFlagWrite)
	if err := checkFFmpeg(ret, err, "open output file"); err != nil {
		return err
	}
	e.formatCtx.SetPb(pb)

	// Initialize audio encoder if sample rate is provided
	if e.config.SampleRate > 0 {
		if err := e.initializeAudioEncoder(); err != nil {
			return fmt.Errorf("failed to initialize audio encoder: %w", err)
		}
	}

	// Write file header
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
	if e.hwEncoder == nil {
		// Software encoder (libx264)
		e.inputPixFmt = ffmpeg.AVPixFmtYuv420P
		e.videoCodec.SetPixFmt(ffmpeg.AVPixFmtYuv420P)
		return nil
	}

	switch e.hwEncoder.Type {
	case HWAccelNVENC:
		// NVENC can accept RGBA directly - GPU handles colourspace conversion
		e.inputPixFmt = ffmpeg.AVPixFmtRgba
		e.videoCodec.SetPixFmt(ffmpeg.AVPixFmtRgba)
		e.videoCodec.SetHwDeviceCtx(ffmpeg.AVBufferRef_(e.hwDeviceCtx))

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
	// Set up AAC encoder for output
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

	// Set channel configuration based on config (default mono)
	outputChannels := e.outputChannels()

	// Set channel layout using FFmpeg 8.0 API
	if outputChannels == 1 {
		ffmpeg.AVChannelLayoutDefault(e.audioCodec.ChLayout(), 1)
	} else {
		ffmpeg.AVChannelLayoutDefault(e.audioCodec.ChLayout(), 2)
	}

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

	// Allocate encoder frame only (no decoder needed)
	e.audioEncFrame = ffmpeg.AVFrameAlloc()
	if e.audioEncFrame == nil {
		return fmt.Errorf("failed to allocate audio encoder frame")
	}

	// Initialize audio FIFO with pooled allocation for the configured frame size
	// Frame size = encoder frame size (1024 for AAC) × number of channels
	samplesPerFrame := e.audioCodec.FrameSize() * outputChannels
	e.audioFIFO = NewAudioFIFO(samplesPerFrame)

	// Configure encoder frame with correct size
	e.audioEncFrame.SetNbSamples(e.audioCodec.FrameSize())
	e.audioEncFrame.SetFormat(int(ffmpeg.AVSampleFmtFltp))
	if outputChannels == 1 {
		ffmpeg.AVChannelLayoutDefault(e.audioEncFrame.ChLayout(), 1)
	} else {
		ffmpeg.AVChannelLayoutDefault(e.audioEncFrame.ChLayout(), 2)
	}
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

	// For Vulkan/QSV/VAAPI/VideoToolbox, convert RGBA→NV12 then upload to GPU
	if e.hwEncoder != nil && (e.hwEncoder.Type == HWAccelVulkan || e.hwEncoder.Type == HWAccelQSV || e.hwEncoder.Type == HWAccelVAAPI || e.hwEncoder.Type == HWAccelVideoToolbox) {
		return e.writeFrameHWUpload(rgbaData)
	}

	// For software encoder, convert RGBA directly to YUV420P (skipping RGB24 intermediate)
	return e.writeFrameRGBASoftware(rgbaData)
}

// writeFrameRGBASoftware converts RGBA directly to YUV420P and encodes.
// This avoids the intermediate RGB24 buffer allocation for ~3% faster software encoding.
func (e *Encoder) writeFrameRGBASoftware(rgbaData []byte) error {
	// Allocate YUV frame
	yuvFrame := ffmpeg.AVFrameAlloc()
	if yuvFrame == nil {
		return fmt.Errorf("failed to allocate YUV frame")
	}
	defer ffmpeg.AVFrameFree(&yuvFrame)

	yuvFrame.SetWidth(e.config.Width)
	yuvFrame.SetHeight(e.config.Height)
	yuvFrame.SetFormat(int(ffmpeg.AVPixFmtYuv420P))

	ret, err := ffmpeg.AVFrameGetBuffer(yuvFrame, 0)
	if err := checkFFmpeg(ret, err, "allocate YUV buffer"); err != nil {
		return err
	}

	// Convert RGBA directly to YUV420P (skips RGB24 intermediate)
	convertRGBAToYUV(rgbaData, yuvFrame, e.config.Width, e.config.Height)

	// Set presentation timestamp
	yuvFrame.SetPts(e.nextVideoPts)
	e.nextVideoPts++

	// Send frame to encoder
	ret, err = ffmpeg.AVCodecSendFrame(e.videoCodec, yuvFrame)
	if err := checkFFmpeg(ret, err, "send frame to encoder"); err != nil {
		return err
	}

	// Receive and write encoded packets
	return e.receiveAndWriteVideoPackets()
}

// writeFrameRGBADirect sends RGBA frame directly to hardware encoder
// This avoids CPU-side colourspace conversion - GPU handles it
func (e *Encoder) writeFrameRGBADirect(rgbaData []byte) error {
	// Allocate RGBA frame
	rgbaFrame := ffmpeg.AVFrameAlloc()
	if rgbaFrame == nil {
		return fmt.Errorf("failed to allocate RGBA frame")
	}
	defer ffmpeg.AVFrameFree(&rgbaFrame)

	rgbaFrame.SetWidth(e.config.Width)
	rgbaFrame.SetHeight(e.config.Height)
	rgbaFrame.SetFormat(int(ffmpeg.AVPixFmtRgba))

	ret, err := ffmpeg.AVFrameGetBuffer(rgbaFrame, 0)
	if err := checkFFmpeg(ret, err, "allocate RGBA buffer"); err != nil {
		return err
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
	ret, err = ffmpeg.AVCodecSendFrame(e.videoCodec, rgbaFrame)
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
	height := e.config.Height

	// Use pre-allocated NV12 frame (already configured in setupHWFramesContext)
	nv12Frame := e.hwNV12Frame

	// Convert RGBA → NV12 using parallel Go conversion (much faster than SwsScaleFrame)
	convertRGBAToNV12(rgbaData, nv12Frame, width, height)

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
	for {
		pkt := ffmpeg.AVPacketAlloc()

		_, err := ffmpeg.AVCodecReceivePacket(e.videoCodec, pkt)
		if err != nil {
			ffmpeg.AVPacketFree(&pkt)
			// EAGAIN and EOF are expected - means no more packets available
			if errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
				break
			}
			return fmt.Errorf("receive packet: %w", err)
		}

		// Set stream index and rescale timestamps
		pkt.SetStreamIndex(e.videoStream.Index())
		ffmpeg.AVPacketRescaleTs(pkt, e.videoCodec.TimeBase(), e.videoStream.TimeBase())

		// Write packet to output
		ret, err := ffmpeg.AVInterleavedWriteFrame(e.formatCtx, pkt)
		ffmpeg.AVPacketFree(&pkt)

		if err := checkFFmpeg(ret, err, "write packet"); err != nil {
			return err
		}
	}

	return nil
}

// WriteAudioSamples writes pre-decoded audio samples to the encoder.
// Samples should be float32, mono or stereo interleaved depending on AudioChannels config.
// For mono: just the samples. For stereo: L0, R0, L1, R1, ...
// This method handles FIFO buffering and encodes complete AAC frames.
func (e *Encoder) WriteAudioSamples(samples []float32) error {
	if e.audioCodec == nil {
		return nil // No audio configured
	}

	encoderFrameSize := e.audioCodec.FrameSize() // Should be 1024 for AAC
	outputChannels := e.outputChannels()

	// Push samples to FIFO
	e.audioFIFO.Push(samples)

	// Process all complete frames in FIFO
	samplesPerFrame := encoderFrameSize * outputChannels
	for e.audioFIFO.Available() >= samplesPerFrame {
		// Pop exactly one encoder frame worth of samples (pooled for AAC sizes)
		frameSamples := e.audioFIFO.Pop(samplesPerFrame)

		// Make frame writable and write samples
		_, _ = ffmpeg.AVFrameMakeWritable(e.audioEncFrame)

		var writeErr error
		if outputChannels == 2 {
			writeErr = writeStereoFloats(e.audioEncFrame, frameSamples)
		} else {
			writeErr = writeMonoFloats(e.audioEncFrame, frameSamples)
		}

		// Return slice to pool after use (safe for non-pooled slices)
		e.audioFIFO.ReturnSlice(frameSamples)

		if writeErr != nil {
			return fmt.Errorf("failed to write %s samples: %w",
				map[int]string{1: "mono", 2: "stereo"}[outputChannels], writeErr)
		}

		// Set PTS
		e.audioEncFrame.SetPts(e.nextAudioPts)
		e.nextAudioPts += int64(encoderFrameSize)

		// Send to encoder
		ret, err := ffmpeg.AVCodecSendFrame(e.audioCodec, e.audioEncFrame)
		if err := checkFFmpeg(ret, err, "send audio frame to encoder"); err != nil {
			return err
		}

		// Receive encoded packets
		for {
			encodedPkt := ffmpeg.AVPacketAlloc()
			_, err = ffmpeg.AVCodecReceivePacket(e.audioCodec, encodedPkt)
			if err != nil {
				ffmpeg.AVPacketFree(&encodedPkt)
				// EAGAIN and EOF are expected - means no more packets available
				if errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
					break
				}
				return fmt.Errorf("receive audio packet from encoder: %w", err)
			}

			// Set stream index and timestamps
			encodedPkt.SetStreamIndex(e.audioStream.Index())
			ffmpeg.AVPacketRescaleTs(encodedPkt, e.audioCodec.TimeBase(), e.audioStream.TimeBase())

			// Write packet to output
			ret, err = ffmpeg.AVInterleavedWriteFrame(e.formatCtx, encodedPkt)
			ffmpeg.AVPacketFree(&encodedPkt)
			if err := checkFFmpeg(ret, err, "write audio packet"); err != nil {
				return err
			}
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

	// Process any remaining samples in FIFO (may be partial frame)
	samplesPerFrame := encoderFrameSize * outputChannels
	remaining := e.audioFIFO.Available()
	if remaining > 0 {
		// Pad with zeros to make a complete frame
		frameSamples := make([]float32, samplesPerFrame)
		partialSamples := e.audioFIFO.Pop(remaining)
		copy(frameSamples, partialSamples)
		// Return partial slice to pool (safe even for non-pooled sizes)
		e.audioFIFO.ReturnSlice(partialSamples)

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

	// Flush encoder by sending NULL frame
	_, _ = ffmpeg.AVCodecSendFrame(e.audioCodec, nil)

	// Receive all remaining packets
	for {
		encodedPkt := ffmpeg.AVPacketAlloc()
		_, err := ffmpeg.AVCodecReceivePacket(e.audioCodec, encodedPkt)
		if err != nil {
			ffmpeg.AVPacketFree(&encodedPkt)
			break
		}

		encodedPkt.SetStreamIndex(e.audioStream.Index())
		ffmpeg.AVPacketRescaleTs(encodedPkt, e.audioCodec.TimeBase(), e.audioStream.TimeBase())

		_, _ = ffmpeg.AVInterleavedWriteFrame(e.formatCtx, encodedPkt)
		ffmpeg.AVPacketFree(&encodedPkt)
	}

	return nil
}

// writeMonoFloats writes mono float samples to an encoder frame
func writeMonoFloats(frame *ffmpeg.AVFrame, samples []float32) error {
	nbSamples := len(samples)

	// Get pointer for mono channel (planar format)
	dataPtr := frame.Data().Get(0)

	if dataPtr == nil {
		return fmt.Errorf("frame data pointer not allocated")
	}

	// Convert to byte slice for writing
	data := (*[1 << 30]byte)(dataPtr)[: nbSamples*4 : nbSamples*4]

	// Write samples to channel
	for i := range nbSamples {
		// Write channel - direct float32 byte copy
		sampleFloat := samples[i]
		binary.LittleEndian.PutUint32(data[i*4:(i+1)*4], math.Float32bits(sampleFloat))
	}

	return nil
}

// writeStereoFloats writes stereo float samples to an encoder frame
func writeStereoFloats(frame *ffmpeg.AVFrame, samples []float32) error {
	nbSamples := len(samples) / 2 // stereo has 2 channels

	// Get pointers for both channels (planar format) using .Get() method
	leftPtr := frame.Data().Get(0)
	rightPtr := frame.Data().Get(1)

	if leftPtr == nil || rightPtr == nil {
		return fmt.Errorf("frame data pointers not allocated")
	}

	// Convert to byte slices for writing
	leftData := (*[1 << 30]byte)(leftPtr)[: nbSamples*4 : nbSamples*4]
	rightData := (*[1 << 30]byte)(rightPtr)[: nbSamples*4 : nbSamples*4]

	// Write samples to both channels
	for i := range nbSamples {
		// Write left channel - direct float32 byte copy
		leftFloat := samples[i*2]
		binary.LittleEndian.PutUint32(leftData[i*4:(i+1)*4], math.Float32bits(leftFloat))

		// Write right channel - direct float32 byte copy
		rightFloat := samples[i*2+1]
		binary.LittleEndian.PutUint32(rightData[i*4:(i+1)*4], math.Float32bits(rightFloat))
	}

	return nil
}

// Close finalizes the output file and frees resources
func (e *Encoder) Close() error {
	// Flush encoder
	if e.videoCodec != nil {
		_, _ = ffmpeg.AVCodecSendFrame(e.videoCodec, nil)

		// Drain remaining packets
		for {
			pkt := ffmpeg.AVPacketAlloc()
			ret, err := ffmpeg.AVCodecReceivePacket(e.videoCodec, pkt)

			if errors.Is(err, ffmpeg.AVErrorEOF) || errors.Is(err, ffmpeg.EAgain) {
				ffmpeg.AVPacketFree(&pkt)
				break
			}

			if ret >= 0 {
				pkt.SetStreamIndex(e.videoStream.Index())
				ffmpeg.AVPacketRescaleTs(pkt, e.videoCodec.TimeBase(), e.videoStream.TimeBase())
				_, _ = ffmpeg.AVInterleavedWriteFrame(e.formatCtx, pkt)
			}

			ffmpeg.AVPacketFree(&pkt)
		}
	}

	// Write trailer
	if e.formatCtx != nil {
		_, _ = ffmpeg.AVWriteTrailer(e.formatCtx)

		// Close output file
		if e.formatCtx.Pb() != nil {
			ffmpeg.AVIOClose(e.formatCtx.Pb())
		}
	}

	// Free codec contexts
	if e.videoCodec != nil {
		ffmpeg.AVCodecFreeContext(&e.videoCodec)
	}
	if e.audioCodec != nil {
		ffmpeg.AVCodecFreeContext(&e.audioCodec)
	}

	// Free audio resources
	if e.audioEncFrame != nil {
		ffmpeg.AVFrameFree(&e.audioEncFrame)
	}

	// Free hardware device context
	if e.hwDeviceCtx != nil {
		ffmpeg.AVBufferUnref(&e.hwDeviceCtx)
		e.hwDeviceCtx = nil
	}

	// Free Vulkan-specific resources
	if e.hwFramesCtx != nil {
		ffmpeg.AVBufferUnref(&e.hwFramesCtx)
		e.hwFramesCtx = nil
	}
	// Free pre-allocated Vulkan NV12 frame
	if e.hwNV12Frame != nil {
		ffmpeg.AVFrameFree(&e.hwNV12Frame)
		e.hwNV12Frame = nil
	}

	// Free format context
	if e.formatCtx != nil {
		ffmpeg.AVFormatFreeContext(e.formatCtx)
		e.formatCtx = nil
	}

	return nil
}
