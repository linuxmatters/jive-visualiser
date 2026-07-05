package encoder

import (
	"os"
	"runtime"
	"sort"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"

	"github.com/linuxmatters/jive-visualiser/internal/config"
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

type hwEncoderOptionPolicy int

const (
	// hwEncoderOptionsNone sets no dispatched options. Used by encoders whose
	// options come from elsewhere (the software encoder, see
	// setSoftwareEncoderOptions) or that take no options (auto).
	hwEncoderOptionsNone hwEncoderOptionPolicy = iota
	hwEncoderOptionsNVENC
	hwEncoderOptionsQSV
	hwEncoderOptionsVAAPI
	hwEncoderOptionsVulkan
	hwEncoderOptionsVideoToolbox
)

type hwEncoderPriority struct {
	linux  int
	darwin int
}

func (p hwEncoderPriority) forGOOS(goos string) int {
	if goos == "darwin" {
		return p.darwin
	}
	return p.linux
}

type hwEncoderRegistryEntry struct {
	cliName            string
	ffmpegName         string
	accelType          HWAccelType
	deviceType         ffmpeg.AVHWDeviceType
	description        string
	priority           hwEncoderPriority
	probePixelFormat   ffmpeg.AVPixelFormat
	runtimePixelFormat ffmpeg.AVPixelFormat
	optionPolicy       hwEncoderOptionPolicy
	cliSelectable      bool
}

func (entry hwEncoderRegistryEntry) toHWEncoder(available bool) HWEncoder {
	return HWEncoder{
		Name:        entry.ffmpegName,
		Type:        entry.accelType,
		DeviceType:  entry.deviceType,
		Description: entry.description,
		Available:   available,
	}
}

var hwEncoderRegistry = []hwEncoderRegistryEntry{
	{
		cliName:            "auto",
		accelType:          HWAccelAuto,
		deviceType:         ffmpeg.AVHWDeviceTypeNone,
		description:        "Auto-detect best available",
		priority:           hwEncoderPriority{},
		probePixelFormat:   ffmpeg.AVPixFmtNone,
		runtimePixelFormat: ffmpeg.AVPixFmtNone,
		optionPolicy:       hwEncoderOptionsNone,
		cliSelectable:      true,
	},
	{
		cliName:            "software",
		ffmpegName:         "libx264",
		accelType:          HWAccelNone,
		deviceType:         ffmpeg.AVHWDeviceTypeNone,
		description:        "Software encoding",
		priority:           hwEncoderPriority{linux: 100, darwin: 100},
		probePixelFormat:   ffmpeg.AVPixFmtYuv420P,
		runtimePixelFormat: ffmpeg.AVPixFmtYuv420P,
		optionPolicy:       hwEncoderOptionsNone,
		cliSelectable:      true,
	},
	{
		cliName:            "nvenc",
		ffmpegName:         "h264_nvenc",
		accelType:          HWAccelNVENC,
		deviceType:         ffmpeg.AVHWDeviceTypeCuda,
		description:        "NVIDIA NVENC",
		priority:           hwEncoderPriority{linux: 10},
		probePixelFormat:   ffmpeg.AVPixFmtRgba,
		runtimePixelFormat: ffmpeg.AVPixFmtRgba,
		optionPolicy:       hwEncoderOptionsNVENC,
		cliSelectable:      true,
	},
	{
		cliName:            "qsv",
		ffmpegName:         "h264_qsv",
		accelType:          HWAccelQSV,
		deviceType:         ffmpeg.AVHWDeviceTypeQsv,
		description:        "Intel Quick Sync Video",
		priority:           hwEncoderPriority{linux: 20},
		probePixelFormat:   ffmpeg.AVPixFmtQsv,
		runtimePixelFormat: ffmpeg.AVPixFmtQsv,
		optionPolicy:       hwEncoderOptionsQSV,
		cliSelectable:      true,
	},
	{
		cliName:            "vaapi",
		ffmpegName:         "h264_vaapi",
		accelType:          HWAccelVAAPI,
		deviceType:         ffmpeg.AVHWDeviceTypeVaapi,
		description:        "VA-API",
		priority:           hwEncoderPriority{linux: 30},
		probePixelFormat:   ffmpeg.AVPixFmtVaapi,
		runtimePixelFormat: ffmpeg.AVPixFmtVaapi,
		optionPolicy:       hwEncoderOptionsVAAPI,
		cliSelectable:      true,
	},
	{
		cliName:            "vulkan",
		ffmpegName:         "h264_vulkan",
		accelType:          HWAccelVulkan,
		deviceType:         ffmpeg.AVHWDeviceTypeVulkan,
		description:        "Vulkan Video",
		priority:           hwEncoderPriority{linux: 40},
		probePixelFormat:   ffmpeg.AVPixFmtVulkan,
		runtimePixelFormat: ffmpeg.AVPixFmtVulkan,
		optionPolicy:       hwEncoderOptionsVulkan,
		cliSelectable:      true,
	},
	{
		cliName:            "videotoolbox",
		ffmpegName:         "h264_videotoolbox",
		accelType:          HWAccelVideoToolbox,
		deviceType:         ffmpeg.AVHWDeviceTypeVideotoolbox,
		description:        "Apple VideoToolbox",
		priority:           hwEncoderPriority{darwin: 10},
		probePixelFormat:   ffmpeg.AVPixFmtVideotoolbox,
		runtimePixelFormat: ffmpeg.AVPixFmtVideotoolbox,
		optionPolicy:       hwEncoderOptionsVideoToolbox,
		cliSelectable:      false,
	},
}

var cliEncoderNames = []string{"auto", "nvenc", "qsv", "vaapi", "vulkan", "software"}

// ValidCLIEncoderNames returns encoder names accepted by the --encoder flag.
func ValidCLIEncoderNames() []string {
	names := make([]string, len(cliEncoderNames))
	copy(names, cliEncoderNames)
	return names
}

// HWAccelTypeForCLIName maps a CLI encoder name to its hardware acceleration type.
func HWAccelTypeForCLIName(name string) (HWAccelType, bool) {
	entry, ok := hwEncoderRegistryEntryForCLIName(name)
	if !ok {
		return "", false
	}
	return entry.accelType, true
}

func hwEncoderRegistryEntryForCLIName(name string) (hwEncoderRegistryEntry, bool) {
	for _, entry := range hwEncoderRegistry {
		if entry.cliName == name && entry.cliSelectable {
			return entry, true
		}
	}
	return hwEncoderRegistryEntry{}, false
}

func hwEncoderRegistryEntryForType(accelType HWAccelType) (hwEncoderRegistryEntry, bool) {
	for _, entry := range hwEncoderRegistry {
		if entry.accelType == accelType {
			return entry, true
		}
	}
	return hwEncoderRegistryEntry{}, false
}

func hwEncoderRegistryEntryForHWEncoder(encoder *HWEncoder) (hwEncoderRegistryEntry, bool) {
	if encoder == nil {
		return hwEncoderRegistryEntry{}, false
	}
	entry, ok := hwEncoderRegistryEntryForType(encoder.Type)
	if !ok {
		return hwEncoderRegistryEntry{}, false
	}
	if encoder.Name != "" && entry.ffmpegName != "" && encoder.Name != entry.ffmpegName {
		return hwEncoderRegistryEntry{}, false
	}
	return entry, true
}

func hardwareEncoderRegistryForOS(goos string) []hwEncoderRegistryEntry {
	var entries []hwEncoderRegistryEntry
	for _, entry := range hwEncoderRegistry {
		if entry.accelType == HWAccelNone || entry.accelType == HWAccelAuto {
			continue
		}
		if entry.priority.forGOOS(goos) == 0 {
			continue
		}
		entries = append(entries, entry)
	}

	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].priority.forGOOS(goos) < entries[j].priority.forGOOS(goos)
	})

	return entries
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
func testEncoderAvailable(entry hwEncoderRegistryEntry) bool {
	restoreLogging := suppressHWProbeLogging()
	defer restoreLogging()

	encName := ffmpeg.ToCStr(entry.ffmpegName)
	defer encName.Free()
	codec := ffmpeg.AVCodecFindEncoderByName(encName)
	if codec == nil {
		return false
	}

	var hwDeviceCtx *ffmpeg.AVBufferRef
	ret, _ := ffmpeg.AVHWDeviceCtxCreate(&hwDeviceCtx, entry.deviceType, nil, nil, 0)
	if ret < 0 || hwDeviceCtx == nil {
		return false
	}
	defer ffmpeg.AVBufferUnref(&hwDeviceCtx)

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

	codecCtx.SetPixFmt(entry.probePixelFormat)
	if entry.probePixelFormat == ffmpeg.AVPixFmtRgba {
		codecCtx.SetHwDeviceCtx(ffmpeg.AVBufferRef_(hwDeviceCtx))
	} else {
		hwFramesRef := setupTestHWFramesContext(hwDeviceCtx, codecCtx, entry.probePixelFormat)
		if hwFramesRef == nil {
			return false
		}
		defer ffmpeg.AVBufferUnref(&hwFramesRef)
	}

	// Try to open the encoder - this is the definitive test
	ret, _ = ffmpeg.AVCodecOpen2(codecCtx, codec, nil)
	return ret >= 0
}

// DetectHWEncoders probes for available hardware encoders
// Returns a list of detected encoders in priority order
func DetectHWEncoders() []HWEncoder {
	var encoders []HWEncoder

	// Check each encoder in priority order
	for _, entry := range hardwareEncoderRegistryForOS(runtime.GOOS) {
		encoder := entry.toHWEncoder(false)

		// Perform comprehensive encoder test - this actually attempts to open
		// the encoder with proper hardware context, catching cases where the
		// hardware device exists but doesn't support the specific encoder
		encoder.Available = testEncoderAvailable(entry)

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
		return selectBestAvailableEncoderFromRegistry(encoders, runtime.GOOS)
	}

	if _, ok := hwEncoderRegistryEntryForType(requestedType); !ok {
		return nil // Unknown hardware type
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

func selectBestAvailableEncoderFromRegistry(encoders []HWEncoder, goos string) *HWEncoder {
	for _, entry := range hardwareEncoderRegistryForOS(goos) {
		for i := range encoders {
			if encoders[i].Type == entry.accelType && encoders[i].Available {
				return &encoders[i]
			}
		}
	}
	return nil
}
