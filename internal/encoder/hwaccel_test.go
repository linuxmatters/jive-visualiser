package encoder

import (
	"reflect"
	"testing"

	ffmpeg "github.com/linuxmatters/ffmpeg-statigo"
)

func TestValidCLIEncoderNames(t *testing.T) {
	want := []string{"auto", "nvenc", "qsv", "vaapi", "vulkan", "software"}
	got := ValidCLIEncoderNames()

	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ValidCLIEncoderNames() = %v, want %v", got, want)
	}

	got[0] = "changed"
	if ValidCLIEncoderNames()[0] != "auto" {
		t.Fatal("ValidCLIEncoderNames returned mutable package state")
	}
}

func TestHWAccelTypeForCLINameValidNames(t *testing.T) {
	tests := map[string]HWAccelType{
		"auto":     HWAccelAuto,
		"nvenc":    HWAccelNVENC,
		"qsv":      HWAccelQSV,
		"vaapi":    HWAccelVAAPI,
		"vulkan":   HWAccelVulkan,
		"software": HWAccelNone,
	}

	for name, want := range tests {
		got, ok := HWAccelTypeForCLIName(name)
		if !ok {
			t.Fatalf("expected %q to be valid", name)
		}
		if got != want {
			t.Fatalf("hwAccelTypeForCLIName(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestHWAccelTypeForCLINameRejectsVideoToolbox(t *testing.T) {
	entry, ok := hwEncoderRegistryEntryForType(HWAccelVideoToolbox)
	if !ok {
		t.Fatal("VideoToolbox registry entry missing")
	}
	if entry.cliSelectable {
		t.Fatal("VideoToolbox must not be CLI-selectable")
	}

	if got, ok := HWAccelTypeForCLIName("videotoolbox"); ok {
		t.Fatalf("expected VideoToolbox to be rejected for CLI lookup, got %q", got)
	}
}

func TestSelectBestEncoderFromFallsBackToSoftware(t *testing.T) {
	encoders := []HWEncoder{
		{Name: "h264_nvenc", Type: HWAccelNVENC, Available: false},
		{Name: "h264_qsv", Type: HWAccelQSV, Available: false},
	}

	if got := SelectBestEncoderFrom(encoders, HWAccelAuto); got != nil {
		t.Fatalf("expected nil when no hardware encoder is available, got %q", got.Name)
	}
	if got := SelectBestEncoderFrom(encoders, HWAccelNone); got != nil {
		t.Fatalf("expected nil for software selection, got %q", got.Name)
	}
}

func TestHardwareEncoderRegistrySelectionPriority(t *testing.T) {
	entries := hardwareEncoderRegistryForOS("linux")
	encoders := make([]HWEncoder, 0, len(entries))

	for _, entry := range entries {
		available := entry.accelType == HWAccelNVENC || entry.accelType == HWAccelQSV
		encoders = append(encoders, entry.toHWEncoder(available))
	}

	got := selectBestAvailableEncoderFromRegistry(encoders, "linux")
	if got == nil {
		t.Fatal("expected an encoder from Linux priority list")
	}
	if got.Type != HWAccelNVENC {
		t.Fatalf("selected %q, want %q", got.Type, HWAccelNVENC)
	}
}

func TestSelectBestAvailableEncoderFromRegistryUsesPriority(t *testing.T) {
	encoders := []HWEncoder{
		{Name: "h264_qsv", Type: HWAccelQSV, Available: true},
		{Name: "h264_nvenc", Type: HWAccelNVENC, Available: true},
		{Name: "h264_vaapi", Type: HWAccelVAAPI, Available: true},
	}

	got := selectBestAvailableEncoderFromRegistry(encoders, "linux")
	if got == nil {
		t.Fatal("expected an encoder from Linux priority list")
	}
	if got.Type != HWAccelNVENC {
		t.Fatalf("selected %q, want %q", got.Type, HWAccelNVENC)
	}
}

func TestSelectBestEncoderFromExplicitSemantics(t *testing.T) {
	encoders := []HWEncoder{
		{Name: "h264_nvenc", Type: HWAccelNVENC, Available: false},
		{Name: "h264_qsv", Type: HWAccelQSV, Available: true},
	}

	got := SelectBestEncoderFrom(encoders, HWAccelQSV)
	if got == nil {
		t.Fatal("expected available explicit QSV encoder")
	}
	if got.Type != HWAccelQSV {
		t.Fatalf("selected %q, want %q", got.Type, HWAccelQSV)
	}

	if got := SelectBestEncoderFrom(encoders, HWAccelNVENC); got != nil {
		t.Fatalf("expected nil for unavailable explicit NVENC encoder, got %q", got.Name)
	}
	if got := SelectBestEncoderFrom(encoders, HWAccelType("unknown")); got != nil {
		t.Fatalf("expected nil for unknown hardware type, got %q", got.Name)
	}
}

func TestHardwareEncoderRegistryRuntimeChoices(t *testing.T) {
	tests := map[HWAccelType]struct {
		deviceType         ffmpeg.AVHWDeviceType
		probePixelFormat   ffmpeg.AVPixelFormat
		runtimePixelFormat ffmpeg.AVPixelFormat
		optionPolicy       hwEncoderOptionPolicy
	}{
		HWAccelNVENC: {
			deviceType:         ffmpeg.AVHWDeviceTypeCuda,
			probePixelFormat:   ffmpeg.AVPixFmtRgba,
			runtimePixelFormat: ffmpeg.AVPixFmtRgba,
			optionPolicy:       hwEncoderOptionsNVENC,
		},
		HWAccelQSV: {
			deviceType:         ffmpeg.AVHWDeviceTypeQsv,
			probePixelFormat:   ffmpeg.AVPixFmtQsv,
			runtimePixelFormat: ffmpeg.AVPixFmtQsv,
			optionPolicy:       hwEncoderOptionsQSV,
		},
		HWAccelVAAPI: {
			deviceType:         ffmpeg.AVHWDeviceTypeVaapi,
			probePixelFormat:   ffmpeg.AVPixFmtVaapi,
			runtimePixelFormat: ffmpeg.AVPixFmtVaapi,
			optionPolicy:       hwEncoderOptionsVAAPI,
		},
		HWAccelVulkan: {
			deviceType:         ffmpeg.AVHWDeviceTypeVulkan,
			probePixelFormat:   ffmpeg.AVPixFmtVulkan,
			runtimePixelFormat: ffmpeg.AVPixFmtVulkan,
			optionPolicy:       hwEncoderOptionsVulkan,
		},
		HWAccelVideoToolbox: {
			deviceType:         ffmpeg.AVHWDeviceTypeVideotoolbox,
			probePixelFormat:   ffmpeg.AVPixFmtVideotoolbox,
			runtimePixelFormat: ffmpeg.AVPixFmtVideotoolbox,
			optionPolicy:       hwEncoderOptionsVideoToolbox,
		},
	}

	for accelType, want := range tests {
		entry, ok := hwEncoderRegistryEntryForType(accelType)
		if !ok {
			t.Fatalf("registry entry missing for %q", accelType)
		}
		if entry.deviceType != want.deviceType {
			t.Fatalf("%q device type = %v, want %v", accelType, entry.deviceType, want.deviceType)
		}
		if entry.probePixelFormat != want.probePixelFormat {
			t.Fatalf("%q probe pixel format = %v, want %v", accelType, entry.probePixelFormat, want.probePixelFormat)
		}
		if entry.runtimePixelFormat != want.runtimePixelFormat {
			t.Fatalf("%q runtime pixel format = %v, want %v", accelType, entry.runtimePixelFormat, want.runtimePixelFormat)
		}
		if entry.optionPolicy != want.optionPolicy {
			t.Fatalf("%q option policy = %v, want %v", accelType, entry.optionPolicy, want.optionPolicy)
		}
	}
}

func TestDetectHWEncoders(t *testing.T) {
	encoders := DetectHWEncoders()

	t.Logf("Detected %d encoder types", len(encoders))

	for _, enc := range encoders {
		status := "not available"
		if enc.Available {
			status = "AVAILABLE"
		}
		t.Logf("  %s (%s): %s", enc.Description, enc.Name, status)
	}
}

func TestSelectBestEncoder(t *testing.T) {
	// Test auto-detection
	enc := SelectBestEncoder(HWAccelAuto)
	if enc != nil {
		t.Logf("Auto-selected encoder: %s (%s)", enc.Description, enc.Name)
	} else {
		t.Log("No hardware encoder available, will use software (libx264)")
	}

	// Test explicit software selection
	enc = SelectBestEncoder(HWAccelNone)
	if enc != nil {
		t.Errorf("Expected nil for HWAccelNone, got %s", enc.Name)
	}
}
