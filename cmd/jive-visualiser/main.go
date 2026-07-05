package main

import (
	"os"

	"github.com/alecthomas/kong"
	"github.com/linuxmatters/jive-visualiser/internal/cli"
)

// version is set via ldflags at build time: "dev" for local builds, the git tag
// (e.g. "v0.1.0") for releases.
var version = "dev"

type cliOptions struct {
	Input           string `arg:"" name:"input" help:"Input WAV file" optional:""`
	Output          string `arg:"" name:"output" help:"Output MP4 file" optional:""`
	Episode         *int   `help:"Episode number (omitted from output when not set)"`
	Title           string `help:"Podcast title" default:"Podcast Title"`
	Channels        int    `help:"Audio channels in MP4: 1 (mono) or 2 (stereo)" default:"1"`
	BarColor        string `help:"Bar color in hex format (e.g., #A40000 or A40000)"`
	TextColor       string `help:"Text color in hex format (e.g., #F8B31D or F8B31D)"`
	BackgroundImage string `help:"Path to custom background image (PNG, 1280x720)"`
	ThumbnailImage  string `help:"Path to custom thumbnail image (PNG, 1280x720)"`
	NoPreview       bool   `help:"Disable video preview during encoding"`
	Encoder         string `help:"Video encoder: auto, nvenc, qsv, vaapi, vulkan, software" default:"auto"`
	Version         bool   `help:"Show version information"`
	Probe           bool   `help:"Probe and display available hardware encoders"`
}

func main() {
	// opts holds the parsed command-line flags and positional arguments.
	var opts cliOptions

	ctx := kong.Parse(
		&opts,
		kong.Name("jive-visualiser"),
		kong.Description("Spin your podcast .wav into a groovy MP4 visualiser."),
		kong.Vars{"version": version},
		kong.UsageOnError(),
		kong.Help(cli.StyledHelpPrinter()),
	)

	if opts.Version {
		cli.PrintVersion(version)
		os.Exit(0)
	}

	if opts.Probe {
		probeHardwareEncoders()
		os.Exit(0)
	}

	// No arguments: show usage instead of erroring
	if opts.Input == "" && opts.Output == "" {
		_ = ctx.PrintUsage(true)
		os.Exit(0)
	}

	runConfig, err := newRunConfig(opts)
	if err != nil {
		cli.PrintError(err.Error())
		os.Exit(1)
	}

	generateVideo(runConfig)
}
