package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/linuxmatters/jive-visualiser/internal/cli"
	"github.com/linuxmatters/jive-visualiser/internal/config"
	"github.com/linuxmatters/jive-visualiser/internal/encoder"
	"github.com/linuxmatters/jive-visualiser/internal/renderer"
)

type runConfig struct {
	inputFile     string
	outputFile    string
	channels      int
	noPreview     bool
	hwAccel       encoder.HWAccelType
	runtimeConfig *config.RuntimeConfig
	meta          renderer.PodcastMeta
}

func newRunConfig(opts cliOptions) (*runConfig, error) {
	if opts.Input == "" || opts.Output == "" {
		return nil, fmt.Errorf("<input> and <output> are required")
	}

	if err := validateExistingPath("input file", opts.Input, os.Stat); err != nil {
		return nil, err
	}

	if opts.Channels != 1 && opts.Channels != 2 {
		return nil, fmt.Errorf("invalid channels value: %d (must be 1 or 2)", opts.Channels)
	}

	hwAccelType, err := parseEncoderFlag(opts.Encoder)
	if err != nil {
		return nil, err
	}

	if err := validateRequestedEncoder(opts.Encoder, hwAccelType); err != nil {
		return nil, err
	}

	runtimeConfig, err := newRuntimeConfig(opts)
	if err != nil {
		return nil, err
	}

	return &runConfig{
		inputFile:     opts.Input,
		outputFile:    opts.Output,
		channels:      opts.Channels,
		noPreview:     opts.NoPreview,
		hwAccel:       hwAccelType,
		runtimeConfig: runtimeConfig,
		meta: renderer.PodcastMeta{
			Title:   opts.Title,
			Episode: opts.Episode,
		},
	}, nil
}

func probeHardwareEncoders() {
	encoders := encoder.DetectHWEncoders()
	var infos []cli.EncoderInfo
	for _, enc := range encoders {
		infos = append(infos, cli.EncoderInfo{
			Name:        enc.Name,
			Description: enc.Description,
			Available:   enc.Available,
		})
	}
	cli.PrintHardwareProbe(infos)
}

func parseEncoderFlag(value string) (encoder.HWAccelType, error) {
	hwAccelType, ok := encoder.HWAccelTypeForCLIName(value)
	if !ok {
		return "", fmt.Errorf("invalid --encoder value: %s (must be %s)", value, formatEncoderNames(encoder.ValidCLIEncoderNames()))
	}
	return hwAccelType, nil
}

func validateRequestedEncoder(cliName string, hwAccelType encoder.HWAccelType) error {
	if hwAccelType == encoder.HWAccelAuto || hwAccelType == encoder.HWAccelNone {
		return nil
	}

	encoders := encoder.DetectHWEncoders()
	if encoder.SelectBestEncoderFrom(encoders, hwAccelType) != nil {
		return nil
	}

	available := availableEncoderNames(encoders)
	if len(available) > 0 {
		return fmt.Errorf("requested encoder '%s' is not available. Available hardware encoders: %s",
			cliName, strings.Join(available, ", "))
	}

	return fmt.Errorf("requested encoder '%s' is not available. No hardware encoders detected; use --encoder=software",
		cliName)
}

func availableEncoderNames(encoders []encoder.HWEncoder) []string {
	var available []string
	for _, enc := range encoders {
		if enc.Available {
			available = append(available, string(enc.Type))
		}
	}
	return available
}

func newRuntimeConfig(opts cliOptions) (*config.RuntimeConfig, error) {
	runtimeConfig := &config.RuntimeConfig{}

	if err := setRuntimeColor(&runtimeConfig.BarColor, "bar-color", opts.BarColor); err != nil {
		return nil, err
	}
	if err := setRuntimeColor(&runtimeConfig.TextColor, "text-color", opts.TextColor); err != nil {
		return nil, err
	}

	if opts.BackgroundImage != "" {
		if err := validateExistingPath("background image", opts.BackgroundImage, os.Stat); err != nil {
			return nil, err
		}
		runtimeConfig.BackgroundImagePath = opts.BackgroundImage
	}

	if opts.ThumbnailImage != "" {
		if err := validateExistingPath("thumbnail image", opts.ThumbnailImage, os.Stat); err != nil {
			return nil, err
		}
		runtimeConfig.ThumbnailImagePath = opts.ThumbnailImage
	}

	return runtimeConfig, nil
}

func setRuntimeColor(dst *config.OptionalColor, flagName, value string) error {
	if value == "" {
		return nil
	}

	r, g, b, err := config.ParseHexColor(value)
	if err != nil {
		return fmt.Errorf("invalid --%s: %w", flagName, err)
	}

	*dst = config.OptionalColor{R: r, G: g, B: b, Set: true}
	return nil
}

func validateExistingPath(label, path string, stat func(string) (os.FileInfo, error)) error {
	if _, err := stat(path); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("%s does not exist: %s", label, path)
		}
		return fmt.Errorf("checking %s %q: %w", label, path, err)
	}
	return nil
}

func formatEncoderNames(names []string) string {
	if len(names) == 0 {
		return ""
	}
	if len(names) == 1 {
		return names[0]
	}
	return fmt.Sprintf("%s, or %s", strings.Join(names[:len(names)-1], ", "), names[len(names)-1])
}
