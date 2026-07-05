package encoder

import (
	"errors"
	"fmt"
	"unsafe"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
	"github.com/linuxmatters/jive-visualiser/internal/yuv"
)

type videoEncoder struct {
	config Config
	stream *ffmpeg.AVStream
	codec  *ffmpeg.AVCodecContext

	hwEncoder   *HWEncoder
	hwDeviceCtx *ffmpeg.AVBufferRef
	hwFramesCtx *ffmpeg.AVBufferRef

	hwNV12Frame *ffmpeg.AVFrame
	hwFrame     *ffmpeg.AVFrame
	swYUVFrame  *ffmpeg.AVFrame
	rgbaFrame   *ffmpeg.AVFrame

	rowPool     *yuv.RowPool
	inputPixFmt ffmpeg.AVPixelFormat
	nextPts     int64
}

func newVideoEncoder(formatCtx *ffmpeg.AVFormatContext, config Config) (_ *videoEncoder, err error) {
	v := &videoEncoder{
		config:  config,
		rowPool: yuv.NewRowPool(config.Height),
	}
	defer func() {
		if err != nil {
			v.close()
		}
	}()

	codec, err := v.selectCodec()
	if err != nil {
		return nil, err
	}

	v.stream = ffmpeg.AVFormatNewStream(formatCtx, nil)
	if v.stream == nil {
		return nil, fmt.Errorf("failed to create video stream")
	}
	v.stream.SetId(0)

	v.codec = ffmpeg.AVCodecAllocContext3(codec)
	if v.codec == nil {
		return nil, fmt.Errorf("failed to allocate codec context")
	}

	v.codec.SetWidth(config.Width)
	v.codec.SetHeight(config.Height)

	if err := v.configurePixelFormat(); err != nil {
		return nil, err
	}

	timeBase := ffmpeg.AVMakeQ(1, config.Framerate)
	v.codec.SetTimeBase(timeBase)

	framerate := ffmpeg.AVMakeQ(config.Framerate, 1)
	v.codec.SetFramerate(framerate)

	v.codec.SetGopSize(config.Framerate * 2) // Keyframe every 2 seconds
	v.stream.SetTimeBase(timeBase)

	// internal/yuv converts with full-range BT.601 JFIF coefficients; untagged
	// streams decode as limited-range BT.709, crushing blacks. Tag the stream
	// before AVCodecOpen2 (so libx264 writes the VUI) and before
	// AVCodecParametersFromContext (so codecpar carries the tags into the MP4).
	// These tags are verified correct for the CPU-converted paths (software
	// YUV420P and NV12 hardware upload, both via internal/yuv). The NVENC
	// RGBA-direct path (writeFrameRGBADirect) converts on the GPU with an
	// unverified matrix/range; no NVENC hardware was available to test. If
	// NVENC output shows shifted colours, align the GPU conversion with these
	// tags rather than changing the tags.
	v.codec.SetColorRange(ffmpeg.AVColRangeJpeg)
	v.codec.SetColorspace(ffmpeg.AVColSpcSmpte170M)
	v.codec.SetColorPrimaries(ffmpeg.AVColPriSmpte170M)
	v.codec.SetColorTrc(ffmpeg.AVColTrcSmpte170M)

	var opts *ffmpeg.AVDictionary
	defer ffmpeg.AVDictFree(&opts)

	if v.hwEncoder != nil {
		v.setHWEncoderOptions(&opts)
	} else {
		setSoftwareEncoderOptions(&opts)
	}

	ret, err := ffmpeg.AVCodecOpen2(v.codec, codec, &opts)
	if err := checkFFmpeg(ret, err, "open codec"); err != nil {
		return nil, err
	}

	ret, err = ffmpeg.AVCodecParametersFromContext(v.stream.Codecpar(), v.codec)
	if err := checkFFmpeg(ret, err, "copy codec parameters"); err != nil {
		return nil, err
	}

	return v, nil
}

func (v *videoEncoder) selectCodec() (*ffmpeg.AVCodec, error) {
	hwAccelType := v.config.HWAccel
	if hwAccelType == "" {
		hwAccelType = HWAccelAuto
	}

	v.hwEncoder = SelectBestEncoder(hwAccelType)
	if v.hwEncoder == nil {
		return softwareVideoCodec()
	}

	encoderName := ffmpeg.ToCStr(v.hwEncoder.Name)
	codec := ffmpeg.AVCodecFindEncoderByName(encoderName)
	encoderName.Free()
	if codec == nil {
		return nil, fmt.Errorf("hardware encoder %s not found", v.hwEncoder.Name)
	}

	if !v.createHWDeviceContext() {
		v.hwEncoder = nil
		v.hwDeviceCtx = nil
		return softwareVideoCodec()
	}

	return codec, nil
}

func softwareVideoCodec() (*ffmpeg.AVCodec, error) {
	codec := ffmpeg.AVCodecFindEncoder(ffmpeg.AVCodecIdH264)
	if codec == nil {
		return nil, fmt.Errorf("H.264 encoder not found")
	}
	return codec, nil
}

func (v *videoEncoder) createHWDeviceContext() bool {
	if v.hwEncoder.Type != HWAccelQSV {
		ret, err := ffmpeg.AVHWDeviceCtxCreate(&v.hwDeviceCtx, v.hwEncoder.DeviceType, nil, nil, 0)
		return err == nil && ret >= 0
	}

	for _, device := range []string{"/dev/dri/renderD128", "/dev/dri/renderD129", ""} {
		var deviceCStr *ffmpeg.CStr
		if device != "" {
			deviceCStr = ffmpeg.ToCStr(device)
		}
		ret, err := ffmpeg.AVHWDeviceCtxCreate(&v.hwDeviceCtx, v.hwEncoder.DeviceType, deviceCStr, nil, 0)
		if deviceCStr != nil {
			deviceCStr.Free()
		}
		if err == nil && ret >= 0 {
			return true
		}
	}

	return false
}

// setHWEncoderOptions configures encoder-specific options for hardware encoders.
func (v *videoEncoder) setHWEncoderOptions(opts **ffmpeg.AVDictionary) {
	entry, ok := hwEncoderRegistryEntryForHWEncoder(v.hwEncoder)
	if !ok {
		return
	}

	for _, option := range hwEncoderOptions(entry.optionPolicy) {
		setEncoderOption(opts, option.key, option.value)
	}
}

func setSoftwareEncoderOptions(opts **ffmpeg.AVDictionary) {
	for _, option := range []hwEncoderOption{
		{"crf", "24"},
		{"preset", "veryfast"},
		{"tune", "animation"},
		{"profile", "main"},
		{"ref", "1"},
		{"bf", "1"},
		{"subme", "4"},
	} {
		setEncoderOption(opts, option.key, option.value)
	}
}

type hwEncoderOption struct {
	key   string
	value string
}

func hwEncoderOptions(policy hwEncoderOptionPolicy) []hwEncoderOption {
	switch policy {
	case hwEncoderOptionsNVENC:
		return []hwEncoderOption{
			{"preset", "p1"},
			{"tune", "ull"},
			{"rc", "vbr"},
			{"cq", "24"},
			{"profile", "main"},
			{"bf", "0"},
			{"zerolatency", "1"},
		}
	case hwEncoderOptionsQSV:
		return []hwEncoderOption{
			{"preset", "medium"},
			{"global_quality", "24"},
			{"profile", "main"},
		}
	case hwEncoderOptionsVulkan:
		return []hwEncoderOption{
			{"content", "rendered"},
			{"qp", "24"},
			{"tune", "ull"},
			{"async_depth", "4"},
			{"profile", "main"},
			{"b_depth", "1"},
		}
	case hwEncoderOptionsVAAPI:
		return []hwEncoderOption{
			{"qp", "24"},
			{"profile", "main"},
			{"bf", "0"},
		}
	case hwEncoderOptionsVideoToolbox:
		return []hwEncoderOption{
			{"profile", "main"},
			{"level", "4.1"},
			{"realtime", "1"},
			{"allow_sw", "0"},
		}
	default:
		return nil
	}
}

func setEncoderOption(opts **ffmpeg.AVDictionary, key, value string) {
	keyC := ffmpeg.ToCStr(key)
	defer keyC.Free()
	valueC := ffmpeg.ToCStr(value)
	defer valueC.Free()
	_, _ = ffmpeg.AVDictSet(opts, keyC, valueC, 0)
}

// setupHWFramesContext creates and configures the hardware frames context
// required for GPU-upload encoders. These encoders require frames to be
// uploaded to GPU memory before encoding, using NV12 as the software pixel
// format.
func (v *videoEncoder) setupHWFramesContext(hwPixFmt ffmpeg.AVPixelFormat) error {
	if v.hwDeviceCtx == nil {
		return fmt.Errorf("hardware device context not available")
	}

	hwFramesRef := ffmpeg.AVHWFrameCtxAlloc(v.hwDeviceCtx)
	if hwFramesRef == nil {
		return fmt.Errorf("failed to allocate hardware frames context")
	}
	v.hwFramesCtx = hwFramesRef

	framesCtx := ffmpeg.ToAVHWFramesContext(hwFramesRef.Data())
	if framesCtx == nil {
		return fmt.Errorf("failed to get hardware frames context")
	}

	framesCtx.SetFormat(hwPixFmt)
	framesCtx.SetSwFormat(ffmpeg.AVPixFmtNv12)
	framesCtx.SetWidth(v.config.Width)
	framesCtx.SetHeight(v.config.Height)
	framesCtx.SetInitialPoolSize(20)

	ret, err := ffmpeg.AVHWFrameCtxInit(hwFramesRef)
	if err := checkFFmpeg(ret, err, "initialize hardware frames context"); err != nil {
		return err
	}

	v.codec.SetHwFramesCtx(ffmpeg.AVBufferRef_(hwFramesRef))

	v.hwNV12Frame = ffmpeg.AVFrameAlloc()
	if v.hwNV12Frame == nil {
		return fmt.Errorf("failed to allocate reusable NV12 frame")
	}
	v.hwNV12Frame.SetWidth(v.config.Width)
	v.hwNV12Frame.SetHeight(v.config.Height)
	v.hwNV12Frame.SetFormat(int(ffmpeg.AVPixFmtNv12))

	ret, err = ffmpeg.AVFrameGetBuffer(v.hwNV12Frame, 0)
	if err := checkFFmpeg(ret, err, "allocate NV12 buffer"); err != nil {
		return err
	}

	// Reused across frames: each write unrefs it and checks out a fresh GPU
	// buffer via AVHWFrameGetBuffer, avoiding a per-frame Go-side alloc/free.
	v.hwFrame = ffmpeg.AVFrameAlloc()
	if v.hwFrame == nil {
		return fmt.Errorf("failed to allocate reusable hardware frame")
	}

	return nil
}

func inputPixelFormatForRegistryEntry(entry hwEncoderRegistryEntry) ffmpeg.AVPixelFormat {
	if entry.accelType == HWAccelNone {
		return entry.runtimePixelFormat
	}
	if entry.runtimePixelFormat == ffmpeg.AVPixFmtRgba {
		return ffmpeg.AVPixFmtRgba
	}
	return ffmpeg.AVPixFmtNv12
}

// configurePixelFormat sets up pixel formats and hardware context based on
// encoder type.
func (v *videoEncoder) configurePixelFormat() error {
	if v.hwEncoder == nil {
		entry, ok := hwEncoderRegistryEntryForType(HWAccelNone)
		if !ok {
			return fmt.Errorf("software encoder registry entry missing")
		}

		v.inputPixFmt = inputPixelFormatForRegistryEntry(entry)
		v.codec.SetPixFmt(entry.runtimePixelFormat)

		v.swYUVFrame = ffmpeg.AVFrameAlloc()
		if v.swYUVFrame == nil {
			return fmt.Errorf("failed to allocate reusable YUV frame")
		}
		v.swYUVFrame.SetWidth(v.config.Width)
		v.swYUVFrame.SetHeight(v.config.Height)
		v.swYUVFrame.SetFormat(int(entry.runtimePixelFormat))

		ret, err := ffmpeg.AVFrameGetBuffer(v.swYUVFrame, 0)
		if err := checkFFmpeg(ret, err, "allocate YUV buffer"); err != nil {
			return err
		}
		return nil
	}

	entry, ok := hwEncoderRegistryEntryForHWEncoder(v.hwEncoder)
	if !ok {
		return fmt.Errorf("unsupported hardware encoder type: %s", v.hwEncoder.Type)
	}

	v.inputPixFmt = inputPixelFormatForRegistryEntry(entry)
	v.codec.SetPixFmt(entry.runtimePixelFormat)

	switch v.inputPixFmt {
	case ffmpeg.AVPixFmtRgba:
		v.codec.SetHwDeviceCtx(ffmpeg.AVBufferRef_(v.hwDeviceCtx))

		v.rgbaFrame = ffmpeg.AVFrameAlloc()
		if v.rgbaFrame == nil {
			return fmt.Errorf("failed to allocate reusable RGBA frame")
		}
		v.rgbaFrame.SetWidth(v.config.Width)
		v.rgbaFrame.SetHeight(v.config.Height)
		v.rgbaFrame.SetFormat(int(v.inputPixFmt))

		ret, err := ffmpeg.AVFrameGetBuffer(v.rgbaFrame, 0)
		if err := checkFFmpeg(ret, err, "allocate RGBA buffer"); err != nil {
			return err
		}

	case ffmpeg.AVPixFmtNv12:
		if err := v.setupHWFramesContext(entry.runtimePixelFormat); err != nil {
			return fmt.Errorf("failed to setup %s frames context: %w", entry.description, err)
		}

	default:
		return fmt.Errorf("unsupported runtime pixel format for hardware encoder type: %s", v.hwEncoder.Type)
	}

	return nil
}

func (v *videoEncoder) encoderName() string {
	if v != nil && v.hwEncoder != nil {
		return v.hwEncoder.Name
	}
	return "libx264"
}

func (v *videoEncoder) isHardware() bool {
	return v != nil && v.hwEncoder != nil
}

// writeFrameRGBA encodes and writes a single RGBA frame.
func (v *videoEncoder) writeFrameRGBA(
	rgbaData []byte,
	pkt *ffmpeg.AVPacket,
	writePacket func(*ffmpeg.AVPacket) error,
) error {
	expectedSize := v.config.Width * v.config.Height * 4
	if len(rgbaData) != expectedSize {
		return fmt.Errorf("invalid RGBA frame size: got %d, expected %d", len(rgbaData), expectedSize)
	}

	if v.inputPixFmt == ffmpeg.AVPixFmtRgba {
		return v.writeFrameRGBADirect(rgbaData, pkt, writePacket)
	}

	if v.inputPixFmt == ffmpeg.AVPixFmtNv12 {
		return v.writeFrameHWUpload(rgbaData, pkt, writePacket)
	}

	return v.writeFrameRGBASoftware(rgbaData, pkt, writePacket)
}

// writeFrameRGBASoftware converts RGBA directly to YUV420P and encodes.
func (v *videoEncoder) writeFrameRGBASoftware(
	rgbaData []byte,
	pkt *ffmpeg.AVPacket,
	writePacket func(*ffmpeg.AVPacket) error,
) error {
	yuvFrame := v.swYUVFrame
	ret, err := ffmpeg.AVFrameMakeWritable(yuvFrame)
	if err := checkFFmpeg(ret, err, "make YUV frame writable"); err != nil {
		return err
	}

	convertRGBAToYUV(v.rowPool, rgbaData, yuvFrame, v.config.Width)

	yuvFrame.SetPts(v.nextPts)
	v.nextPts++

	ret, err = ffmpeg.AVCodecSendFrame(v.codec, yuvFrame)
	if err := checkFFmpeg(ret, err, "send frame to encoder"); err != nil {
		return err
	}

	return v.receiveAndWritePackets(pkt, writePacket)
}

// writeFrameRGBADirect sends an RGBA frame directly to the hardware encoder.
func (v *videoEncoder) writeFrameRGBADirect(
	rgbaData []byte,
	pkt *ffmpeg.AVPacket,
	writePacket func(*ffmpeg.AVPacket) error,
) error {
	rgbaFrame := v.rgbaFrame
	ret, err := ffmpeg.AVFrameMakeWritable(rgbaFrame)
	if err := checkFFmpeg(ret, err, "make RGBA frame writable"); err != nil {
		return err
	}

	width := v.config.Width
	height := v.config.Height
	linesize := rgbaFrame.Linesize().Get(0)
	data := rgbaFrame.Data().Get(0)

	srcStride := width * 4
	for y := range height {
		srcOffset := y * srcStride
		dstOffset := y * linesize
		copy(unsafe.Slice((*byte)(unsafe.Add(data, dstOffset)), srcStride), //nolint:gosec // offset is within allocated frame
			rgbaData[srcOffset:srcOffset+srcStride])
	}

	rgbaFrame.SetPts(v.nextPts)
	v.nextPts++

	ret, err = ffmpeg.AVCodecSendFrame(v.codec, rgbaFrame)
	if err := checkFFmpeg(ret, err, "send frame to encoder"); err != nil {
		return err
	}

	return v.receiveAndWritePackets(pkt, writePacket)
}

// writeFrameHWUpload converts RGBA to NV12, uploads to GPU, and encodes.
func (v *videoEncoder) writeFrameHWUpload(
	rgbaData []byte,
	pkt *ffmpeg.AVPacket,
	writePacket func(*ffmpeg.AVPacket) error,
) error {
	width := v.config.Width
	nv12Frame := v.hwNV12Frame

	convertRGBAToNV12(v.rowPool, rgbaData, nv12Frame, width)

	// Release the previous frame's GPU buffer before checking out a fresh one.
	hwFrame := v.hwFrame
	ffmpeg.AVFrameUnref(hwFrame)

	ret, err := ffmpeg.AVHWFrameGetBuffer(v.hwFramesCtx, hwFrame, 0)
	if err := checkFFmpeg(ret, err, "get hardware frame buffer"); err != nil {
		return err
	}

	ret, err = ffmpeg.AVHWFrameTransferData(hwFrame, nv12Frame, 0)
	if err := checkFFmpeg(ret, err, "upload frame to GPU"); err != nil {
		return err
	}

	hwFrame.SetPts(v.nextPts)
	v.nextPts++

	ret, err = ffmpeg.AVCodecSendFrame(v.codec, hwFrame)
	if err := checkFFmpeg(ret, err, "send frame to hardware encoder"); err != nil {
		return err
	}

	return v.receiveAndWritePackets(pkt, writePacket)
}

// receiveAndWritePackets receives encoded packets from the video codec and
// writes them to the output.
func (v *videoEncoder) receiveAndWritePackets(pkt *ffmpeg.AVPacket, writePacket func(*ffmpeg.AVPacket) error) error {
	for {
		_, err := ffmpeg.AVCodecReceivePacket(v.codec, pkt)
		if err != nil {
			if errors.Is(err, ffmpeg.EAgain) || errors.Is(err, ffmpeg.AVErrorEOF) {
				break
			}
			return fmt.Errorf("receive packet: %w", err)
		}

		if err := writePacket(pkt); err != nil {
			return err
		}
	}

	return nil
}

func (v *videoEncoder) flush(pkt *ffmpeg.AVPacket, writePacket func(*ffmpeg.AVPacket) error) error {
	if v == nil || v.codec == nil {
		return nil
	}

	ret, err := ffmpeg.AVCodecSendFrame(v.codec, nil)
	if err := checkFFmpeg(ret, err, "flush video encoder"); err != nil {
		return err
	}
	return v.receiveAndWritePackets(pkt, writePacket)
}

func (v *videoEncoder) close() {
	if v == nil {
		return
	}
	if v.codec != nil {
		ffmpeg.AVCodecFreeContext(&v.codec)
	}
	if v.hwDeviceCtx != nil {
		ffmpeg.AVBufferUnref(&v.hwDeviceCtx)
		v.hwDeviceCtx = nil
	}
	if v.hwFramesCtx != nil {
		ffmpeg.AVBufferUnref(&v.hwFramesCtx)
		v.hwFramesCtx = nil
	}
	if v.hwNV12Frame != nil {
		ffmpeg.AVFrameFree(&v.hwNV12Frame)
		v.hwNV12Frame = nil
	}
	if v.hwFrame != nil {
		ffmpeg.AVFrameFree(&v.hwFrame)
		v.hwFrame = nil
	}
	if v.swYUVFrame != nil {
		ffmpeg.AVFrameFree(&v.swYUVFrame)
		v.swYUVFrame = nil
	}
	if v.rgbaFrame != nil {
		ffmpeg.AVFrameFree(&v.rgbaFrame)
		v.rgbaFrame = nil
	}
	if v.rowPool != nil {
		v.rowPool.Close()
		v.rowPool = nil
	}
}
