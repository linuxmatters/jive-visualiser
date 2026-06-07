package encoder

import (
	"os"
	"runtime"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"

	"github.com/linuxmatters/jivefire/internal/config"
)

// HWAccelType represents a hardware acceleration type
type HWAccelType string

const (
	HWAccelNone         HWAccelType = "none"         // Software encoding (libx264)
	HWAccelAuto         HWAccelType = "auto"         // Auto-detect best available
	HWAccelNVENC        HWAccelType = "nvenc"        // NVIDIA NVENC
	HWAccelQSV          HWAccelType = "qsv"          // Intel Quick Sync Video
	HWAccelVAAPI        HWAccelType = "vaapi"        // VA-API (AMD, Intel, older hardware)
	HWAccelVulkan       HWAccelType = "vulkan"       // Vulkan Video
	HWAccelVideoToolbox HWAccelType = "videotoolbox" // Apple VideoToolbox (macOS)
)

// HWEncoder represents a detected hardware encoder
type HWEncoder struct {
	Name        string      // Encoder name (e.g., "h264_nvenc")
	Type        HWAccelType // Hardware acceleration type
	DeviceType  ffmpeg.AVHWDeviceType
	Available   bool   // Whether hardware is present and working
	Description string // Human-readable description
}

// encoderSpec defines a hardware encoder configuration for priority lists
type encoderSpec struct {
	name       string
	accelType  HWAccelType
	deviceType ffmpeg.AVHWDeviceType
	desc       string
}

// linuxEncoderPriority defines the encoder preference order for Linux
// Priority: nvenc > qsv > vaapi > vulkan > software
// VAAPI is preferred over Vulkan as it has broader hardware support (AMD, Intel, older Intel)
var linuxEncoderPriority = []encoderSpec{
	{"h264_nvenc", HWAccelNVENC, ffmpeg.AVHWDeviceTypeCuda, "NVIDIA NVENC"},
	{"h264_qsv", HWAccelQSV, ffmpeg.AVHWDeviceTypeQsv, "Intel Quick Sync Video"},
	{"h264_vaapi", HWAccelVAAPI, ffmpeg.AVHWDeviceTypeVaapi, "VA-API"},
	{"h264_vulkan", HWAccelVulkan, ffmpeg.AVHWDeviceTypeVulkan, "Vulkan Video"},
}

// macOSEncoderPriority defines the encoder preference order for macOS
// Priority: videotoolbox > software
var macOSEncoderPriority = []encoderSpec{
	{"h264_videotoolbox", HWAccelVideoToolbox, ffmpeg.AVHWDeviceTypeVideotoolbox, "Apple VideoToolbox"},
}

// suppressHWProbeLogging temporarily silences FFmpeg and libva logging during
// hardware probing. Returns a cleanup function that restores the original state.
func suppressHWProbeLogging() func() {
	// Save and silence FFmpeg logs
	oldLevel, _ := ffmpeg.AVLogGetLevel()
	ffmpeg.AVLogSetLevel(ffmpeg.AVLogQuiet)

	// Save and silence libva logs (VA-API has its own logging separate from FFmpeg)
	oldLibvaLevel := os.Getenv("LIBVA_MESSAGING_LEVEL")
	os.Setenv("LIBVA_MESSAGING_LEVEL", "0")

	return func() {
		ffmpeg.AVLogSetLevel(oldLevel)
		if oldLibvaLevel == "" {
			os.Unsetenv("LIBVA_MESSAGING_LEVEL")
		} else {
			os.Setenv("LIBVA_MESSAGING_LEVEL", oldLibvaLevel)
		}
	}
}

// setupTestHWFramesContext creates and initialises a hardware frames context for
// encoder capability testing. Used by Vulkan and VA-API which require frames context.
// Returns the frames reference (caller must defer AVBufferUnref) or nil on failure.
func setupTestHWFramesContext(hwDeviceCtx *ffmpeg.AVBufferRef, codecCtx *ffmpeg.AVCodecContext, hwFormat ffmpeg.AVPixelFormat) *ffmpeg.AVBufferRef {
	hwFramesRef := ffmpeg.AVHWFrameCtxAlloc(hwDeviceCtx)
	if hwFramesRef == nil {
		return nil
	}

	framesCtx := ffmpeg.ToAVHWFramesContext(hwFramesRef.Data())
	if framesCtx == nil {
		ffmpeg.AVBufferUnref(&hwFramesRef)
		return nil
	}

	framesCtx.SetFormat(hwFormat)
	framesCtx.SetSwFormat(ffmpeg.AVPixFmtNv12)
	framesCtx.SetWidth(config.Width)
	framesCtx.SetHeight(config.Height)

	ret, _ := ffmpeg.AVHWFrameCtxInit(hwFramesRef)
	if ret < 0 {
		ffmpeg.AVBufferUnref(&hwFramesRef)
		return nil
	}

	codecCtx.SetHwFramesCtx(ffmpeg.AVBufferRef_(hwFramesRef))
	return hwFramesRef
}

// testEncoderAvailable performs a full encoder capability test by attempting to
// configure and open the encoder with proper hardware context. This catches cases
// where a hardware device exists but doesn't support the specific encoder
// (e.g., Intel iGPU with Vulkan but no Vulkan Video encoding support).
func testEncoderAvailable(encoderName string, deviceType ffmpeg.AVHWDeviceType, accelType HWAccelType) bool {
	restoreLogging := suppressHWProbeLogging()
	defer restoreLogging()

	// Find the encoder
	encName := ffmpeg.ToCStr(encoderName)
	defer encName.Free()
	codec := ffmpeg.AVCodecFindEncoderByName(encName)
	if codec == nil {
		return false
	}

	// Create hardware device context
	var hwDeviceCtx *ffmpeg.AVBufferRef
	ret, _ := ffmpeg.AVHWDeviceCtxCreate(&hwDeviceCtx, deviceType, nil, nil, 0)
	if ret < 0 || hwDeviceCtx == nil {
		return false
	}
	defer ffmpeg.AVBufferUnref(&hwDeviceCtx)

	// Create codec context
	codecCtx := ffmpeg.AVCodecAllocContext3(codec)
	if codecCtx == nil {
		return false
	}
	defer ffmpeg.AVCodecFreeContext(&codecCtx)

	// Configure minimal encoder settings for the test
	codecCtx.SetWidth(config.Width)
	codecCtx.SetHeight(config.Height)
	codecCtx.SetTimeBase(ffmpeg.AVMakeQ(1, config.FPS))
	codecCtx.SetFramerate(ffmpeg.AVMakeQ(config.FPS, 1))

	// Set pixel format based on encoder type
	switch accelType {
	case HWAccelNVENC:
		// NVENC can accept RGBA directly
		codecCtx.SetPixFmt(ffmpeg.AVPixFmtRgba)
		codecCtx.SetHwDeviceCtx(ffmpeg.AVBufferRef_(hwDeviceCtx))
	case HWAccelVulkan:
		// Vulkan requires hardware frames context
		codecCtx.SetPixFmt(ffmpeg.AVPixFmtVulkan)
		hwFramesRef := setupTestHWFramesContext(hwDeviceCtx, codecCtx, ffmpeg.AVPixFmtVulkan)
		if hwFramesRef == nil {
			return false
		}
		defer ffmpeg.AVBufferUnref(&hwFramesRef)
	case HWAccelQSV:
		// QSV requires hardware frames context
		codecCtx.SetPixFmt(ffmpeg.AVPixFmtQsv)
		hwFramesRef := setupTestHWFramesContext(hwDeviceCtx, codecCtx, ffmpeg.AVPixFmtQsv)
		if hwFramesRef == nil {
			return false
		}
		defer ffmpeg.AVBufferUnref(&hwFramesRef)
	case HWAccelVAAPI:
		// VA-API requires hardware frames context
		codecCtx.SetPixFmt(ffmpeg.AVPixFmtVaapi)
		hwFramesRef := setupTestHWFramesContext(hwDeviceCtx, codecCtx, ffmpeg.AVPixFmtVaapi)
		if hwFramesRef == nil {
			return false
		}
		defer ffmpeg.AVBufferUnref(&hwFramesRef)
	case HWAccelVideoToolbox:
		// VideoToolbox requires hardware frames context with NV12 software format
		codecCtx.SetPixFmt(ffmpeg.AVPixFmtVideotoolbox)
		hwFramesRef := setupTestHWFramesContext(hwDeviceCtx, codecCtx, ffmpeg.AVPixFmtVideotoolbox)
		if hwFramesRef == nil {
			return false
		}
		defer ffmpeg.AVBufferUnref(&hwFramesRef)
	default:
		return false
	}

	// Try to open the encoder - this is the definitive test
	ret, _ = ffmpeg.AVCodecOpen2(codecCtx, codec, nil)
	return ret >= 0
}

// DetectHWEncoders probes for available hardware encoders
// Returns a list of detected encoders in priority order
func DetectHWEncoders() []HWEncoder {
	var encoders []HWEncoder

	// Select encoder list based on OS
	var priority []encoderSpec

	switch runtime.GOOS {
	case "darwin":
		priority = macOSEncoderPriority
	default: // Linux and others
		priority = linuxEncoderPriority
	}

	// Check each encoder in priority order
	for _, enc := range priority {
		encoder := HWEncoder{
			Name:        enc.name,
			Type:        enc.accelType,
			DeviceType:  enc.deviceType,
			Description: enc.desc,
			Available:   false,
		}

		// Perform comprehensive encoder test - this actually attempts to open
		// the encoder with proper hardware context, catching cases where the
		// hardware device exists but doesn't support the specific encoder
		encoder.Available = testEncoderAvailable(enc.name, enc.deviceType, enc.accelType)

		encoders = append(encoders, encoder)
	}

	return encoders
}

// SelectBestEncoder returns the best available encoder based on priority
// If requestedType is HWAccelAuto, it selects the first available hardware encoder
// If requestedType is HWAccelNone, it returns nil (use software)
// Otherwise, it attempts to use the requested type if available
func SelectBestEncoder(requestedType HWAccelType) *HWEncoder {
	if requestedType == HWAccelNone {
		return nil // Explicitly requested software encoding
	}

	// Detect all available encoders in priority order
	return SelectBestEncoderFrom(DetectHWEncoders(), requestedType)
}

// SelectBestEncoderFrom selects the best encoder from an already-probed list,
// avoiding a redundant hardware probe when the caller already has the result of
// DetectHWEncoders. Selection semantics match SelectBestEncoder.
func SelectBestEncoderFrom(encoders []HWEncoder, requestedType HWAccelType) *HWEncoder {
	if requestedType == HWAccelNone {
		return nil // Explicitly requested software encoding
	}

	if requestedType == HWAccelAuto {
		// Return first available encoder from the priority list
		for i := range encoders {
			if encoders[i].Available {
				return &encoders[i]
			}
		}
		return nil // No hardware available, fall back to software
	}

	// Look for specifically requested encoder type
	for i := range encoders {
		if encoders[i].Type == requestedType {
			if encoders[i].Available {
				return &encoders[i]
			}
			return nil // Requested type not available
		}
	}

	return nil // Requested type not found
}
