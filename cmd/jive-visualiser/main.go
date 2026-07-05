package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/alecthomas/kong"
	"github.com/linuxmatters/jive-visualiser/internal/audio"
	"github.com/linuxmatters/jive-visualiser/internal/cli"
	"github.com/linuxmatters/jive-visualiser/internal/config"
	"github.com/linuxmatters/jive-visualiser/internal/encoder"
	"github.com/linuxmatters/jive-visualiser/internal/renderer"
	"github.com/linuxmatters/jive-visualiser/internal/ui"
)

// version is set via ldflags at build time: "dev" for local builds, the git tag
// (e.g. "v0.1.0") for releases.
var version = "dev"

// CLI holds the parsed command-line flags and positional arguments.
var CLI struct {
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
	ctx := kong.Parse(
		&CLI,
		kong.Name("jive-visualiser"),
		kong.Description("Spin your podcast .wav into a groovy MP4 visualiser."),
		kong.Vars{"version": version},
		kong.UsageOnError(),
		kong.Help(cli.StyledHelpPrinter()),
	)

	if CLI.Version {
		cli.PrintVersion(version)
		os.Exit(0)
	}

	// Probe flag: display hardware encoder status, then exit
	if CLI.Probe {
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
		os.Exit(0)
	}

	// No arguments: show usage instead of erroring
	if CLI.Input == "" && CLI.Output == "" {
		_ = ctx.PrintUsage(true)
		os.Exit(0)
	}

	if CLI.Input == "" || CLI.Output == "" {
		cli.PrintError("<input> and <output> are required")
		os.Exit(1)
	}

	if _, err := os.Stat(CLI.Input); os.IsNotExist(err) {
		cli.PrintError(fmt.Sprintf("input file does not exist: %s", CLI.Input))
		os.Exit(1)
	}

	if CLI.Channels != 1 && CLI.Channels != 2 {
		cli.PrintError(fmt.Sprintf("invalid channels value: %d (must be 1 or 2)", CLI.Channels))
		os.Exit(1)
	}

	hwAccelType, err := parseEncoderFlag(CLI.Encoder)
	if err != nil {
		cli.PrintError(err.Error())
		os.Exit(1)
	}

	// If user explicitly requested a specific hardware encoder, verify it's available
	if hwAccelType != encoder.HWAccelAuto && hwAccelType != encoder.HWAccelNone {
		encoders := encoder.DetectHWEncoders()
		selectedEncoder := encoder.SelectBestEncoderFrom(encoders, hwAccelType)
		if selectedEncoder == nil {
			// Requested encoder not available - list what IS available
			var available []string
			for _, enc := range encoders {
				if enc.Available {
					available = append(available, string(enc.Type))
				}
			}
			if len(available) > 0 {
				cli.PrintError(fmt.Sprintf("requested encoder '%s' is not available. Available hardware encoders: %s",
					CLI.Encoder, strings.Join(available, ", ")))
			} else {
				cli.PrintError(fmt.Sprintf("requested encoder '%s' is not available. No hardware encoders detected; use --encoder=software",
					CLI.Encoder))
			}
			os.Exit(1)
		}
	}

	runtimeConfig := &config.RuntimeConfig{}

	if CLI.BarColor != "" {
		r, g, b, err := config.ParseHexColor(CLI.BarColor)
		if err != nil {
			cli.PrintError(fmt.Sprintf("invalid --bar-color: %v", err))
			os.Exit(1)
		}
		runtimeConfig.BarColor = config.OptionalColor{R: r, G: g, B: b, Set: true}
	}

	if CLI.TextColor != "" {
		r, g, b, err := config.ParseHexColor(CLI.TextColor)
		if err != nil {
			cli.PrintError(fmt.Sprintf("invalid --text-color: %v", err))
			os.Exit(1)
		}
		runtimeConfig.TextColor = config.OptionalColor{R: r, G: g, B: b, Set: true}
	}

	if CLI.BackgroundImage != "" {
		if _, err := os.Stat(CLI.BackgroundImage); os.IsNotExist(err) {
			cli.PrintError(fmt.Sprintf("background image does not exist: %s", CLI.BackgroundImage))
			os.Exit(1)
		}
		runtimeConfig.BackgroundImagePath = CLI.BackgroundImage
	}

	if CLI.ThumbnailImage != "" {
		if _, err := os.Stat(CLI.ThumbnailImage); os.IsNotExist(err) {
			cli.PrintError(fmt.Sprintf("thumbnail image does not exist: %s", CLI.ThumbnailImage))
			os.Exit(1)
		}
		runtimeConfig.ThumbnailImagePath = CLI.ThumbnailImage
	}

	inputFile := CLI.Input
	outputFile := CLI.Output
	channels := CLI.Channels
	noPreview := CLI.NoPreview

	meta := renderer.PodcastMeta{Title: CLI.Title, Episode: CLI.Episode}

	// Generate video using 2-pass streaming approach
	generateVideo(inputFile, outputFile, channels, noPreview, hwAccelType, runtimeConfig, meta)
}

func parseEncoderFlag(value string) (encoder.HWAccelType, error) {
	hwAccelType, ok := encoder.HWAccelTypeForCLIName(value)
	if !ok {
		return "", fmt.Errorf("invalid --encoder value: %s (must be %s)", value, formatEncoderNames(encoder.ValidCLIEncoderNames()))
	}
	return hwAccelType, nil
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

func generateVideo(inputFile string, outputFile string, channels int, noPreview bool, hwAccel encoder.HWAccelType, runtimeConfig *config.RuntimeConfig, meta renderer.PodcastMeta) {
	overallStartTime := time.Now()

	thumbnailPath := strings.Replace(outputFile, ".mp4", ".png", 1)
	thumbnailStartTime := time.Now()
	if err := renderer.GenerateThumbnail(thumbnailPath, meta, runtimeConfig); err != nil {
		cli.PrintError(fmt.Sprintf("failed to generate thumbnail: %v", err))
		os.Exit(1)
	}
	thumbnailDuration := time.Since(thumbnailStartTime)

	// Get audio metadata upfront for Pass 1 progress estimation
	metadata, err := audio.GetMetadata(inputFile)
	if err != nil {
		cli.PrintError(fmt.Sprintf("reading audio metadata: %v", err))
		os.Exit(1)
	}

	// Calculate estimated total frames for Pass 1 progress.
	// Use the file's actual sample rate so each frame maps to 1/FPS seconds
	// of audio regardless of input rate.
	samplesPerFrame := metadata.SampleRate / config.FPS
	if samplesPerFrame <= 0 {
		cli.PrintError(fmt.Sprintf("input sample rate too low for %d FPS: %d Hz", config.FPS, metadata.SampleRate))
		os.Exit(1)
	}
	estimatedTotalFrames := int(metadata.NumSamples) / samplesPerFrame

	// The alternate screen buffer (set via View().AltScreen) prevents ghost box
	// edges when the view height changes between passes.
	model := ui.NewModel(noPreview)
	p := tea.NewProgram(model)

	// Shared state between goroutines
	var profile *audio.Profile
	var analysisErr error

	// Run both passes in a single goroutine
	go func() {
		// === PASS 1: Analysis ===
		pass1StartTime := time.Now()

		profile, analysisErr = audio.AnalyzeAudio(inputFile, func(frame int, currentRMS, currentPeak float64, barHeights []float64, duration time.Duration) {
			p.Send(ui.AnalysisProgress{
				Frame:       frame,
				TotalFrames: estimatedTotalFrames,
				CurrentRMS:  currentRMS,
				CurrentPeak: currentPeak,
				BarHeights:  barHeights,
				Duration:    duration,
			})
		})

		pass1Duration := time.Since(pass1StartTime)

		if analysisErr != nil {
			p.Quit()
			return
		}

		// Signal Pass 1 complete - this transitions the UI to Pass 2
		p.Send(ui.AnalysisComplete{
			PeakMagnitude: profile.GlobalPeak,
			RMSLevel:      profile.GlobalRMS,
			DynamicRange:  profile.DynamicRange,
			Duration:      time.Duration(float64(time.Second) * profile.Duration),
			OptimalScale:  profile.OptimalBaseScale,
			AnalysisTime:  pass1Duration,
		})

		// === PASS 2: Rendering & Encoding ===
		runPass2(p, profile, pass2Config{
			inputFile:         inputFile,
			outputFile:        outputFile,
			channels:          channels,
			noPreview:         noPreview,
			hwAccel:           hwAccel,
			runtimeConfig:     runtimeConfig,
			meta:              meta,
			thumbnailDuration: thumbnailDuration,
			overallStartTime:  overallStartTime,
		})
	}()

	finalModel, err := p.Run()
	if err != nil {
		cli.PrintError(fmt.Sprintf("running UI: %v", err))
		os.Exit(1)
	}

	// Surface results from the final model now the alt screen is gone. The
	// warnings travelled on the RenderComplete message, so reading them here is
	// synchronised by p.Run() returning.
	if m, ok := finalModel.(*ui.Model); ok {
		for _, w := range m.AssetWarnings() {
			cli.PrintWarning(w)
		}
		if renderErr := m.RenderError(); renderErr != nil {
			cli.PrintError(renderErr.Error())
			os.Exit(1)
		}
		if summary := m.CompletionSummary(); summary != "" {
			fmt.Println(summary)
		}
	}

	// Check for analysis errors (encoding errors handled within runPass2)
	if analysisErr != nil {
		cli.PrintError(fmt.Sprintf("analysing audio: %v", analysisErr))
		os.Exit(1)
	}
}

// pass2Config groups the encoding and timing parameters for runPass2 so the
// call site uses named fields and transposed arguments can't compile silently.
type pass2Config struct {
	inputFile         string
	outputFile        string
	channels          int
	noPreview         bool
	hwAccel           encoder.HWAccelType
	runtimeConfig     *config.RuntimeConfig
	meta              renderer.PodcastMeta
	thumbnailDuration time.Duration
	overallStartTime  time.Time
}

// expandMonoToStereo writes n mono samples from src into dst as interleaved
// L,R pairs (each mono sample duplicated to both channels). dst must hold at
// least 2*n elements.
func expandMonoToStereo(dst []float32, src []float64, n int) {
	for i := range n {
		s := float32(src[i])
		dst[i*2] = s
		dst[i*2+1] = s
	}
}

// convertAndWriteAudio converts n mono float64 samples from src to float32 via
// the pre-allocated buffers (duplicating each sample into interleaved L,R
// pairs when stereo) and writes them with write. When stereo, stereoBuf must
// hold at least 2*n elements (monoBuf is unused and may be nil); otherwise
// monoBuf must hold at least n (stereoBuf is unused and may be nil).
func convertAndWriteAudio(write func([]float32) error, src []float64, n int, stereo bool, monoBuf, stereoBuf []float32) error {
	if stereo {
		expandMonoToStereo(stereoBuf, src, n)
		return write(stereoBuf[:n*2])
	}
	for i := range n {
		monoBuf[i] = float32(src[i])
	}
	return write(monoBuf[:n])
}

// audioConvBufLen returns the length the audio conversion buffers need: they
// must hold the whole FFT prefill (config.FFTSize samples), which exceeds
// samplesPerFrame at common sample rates; at high rates samplesPerFrame is
// the larger of the two.
func audioConvBufLen(samplesPerFrame int) int {
	return max(samplesPerFrame, config.FFTSize)
}

// runPass2 collects any non-fatal warnings during rendering (e.g. an asset that
// failed to load and was dropped) and delivers them on the RenderComplete
// message so the caller can print them after the Bubbletea alt screen exits.
func runPass2(p *tea.Program, profile *audio.Profile, cfg pass2Config) {
	runner := newPass2Runner(p, profile, cfg)

	if err := runner.setupReader(); err != nil {
		runner.fail(err)
		return
	}
	defer runner.reader.Close()

	if err := runner.setupEncoder(); err != nil {
		runner.fail(err)
		return
	}

	defer runner.enc.Close()

	// Asset load failures are non-fatal: nil assets degrade gracefully, but warn
	// so dropped assets are not silent.
	runner.loadAssets()

	if err := runner.setupProcessorAndFrame(); err != nil {
		runner.fail(err)
		return
	}
	defer runner.processor.Close()

	runner.setupTimingAndDisplay()
	const progressUpdateInterval = 30 * time.Millisecond

	runner.setupRenderState()

	// Double-buffered private RGBA images for the preview. The render loop reuses
	// the frame's internal image every iteration, so the UI goroutine must read a
	// copy rather than the live buffer the next Draw will overwrite. A single copy
	// is not enough: the UI still holds the pointer from the previous send while
	// the render loop overwrites that same buffer on the next tick. Ping-pong
	// between two buffers so the producer always fills the one the UI is not
	// reading. Allocated once here, only when preview is enabled, to keep it off
	// the hot path.
	runner.setupPreviewBuffers()

	// Sliding buffer for FFT: we read samplesPerFrame but need FFTSize for FFT.
	// Derive from the file's actual sample rate so encoded audio and video
	// durations stay aligned for any input rate.
	// The reader downmixes to mono. For stereo output the encoder expects
	// interleaved L,R pairs, so duplicate each mono sample into both channels via
	// this pre-allocated buffer (no per-frame allocation).
	runner.setupAudioBuffers()

	// Write the whole prefill to the encoder. FillFFTBuffer consumed n samples
	// from the reader, so all n must reach the encoder or that audio is lost
	// (truncating to samplesPerFrame dropped ~13 ms at 44.1 kHz). The FIFO in
	// the encoder absorbs the surplus beyond frame 0. Reuse the conversion
	// buffers: WriteAudioSamples copies into the FIFO and retains no reference,
	// and the buffers are overwritten before each later use in the render loop.
	if err := runner.prefillFFT(); err != nil {
		runner.fail(err)
		return
	}

	// Process frames until we run out of audio
	frameNum := 0
	for frameNum < runner.numFrames {
		img := runner.renderFrame()
		if err := runner.writeVideoFrame(frameNum, img); err != nil {
			runner.fail(err)
			return
		}

		runner.sendProgressIfDue(frameNum, img, progressUpdateInterval)

		frameNum++

		hasAudio, err := runner.processNextAudioFrame(frameNum)
		if err != nil {
			runner.fail(err)
			return
		}
		if !hasAudio {
			break
		}
	}

	// Flush samples still in the FIFO after the last video frame is written.
	if err := runner.enc.FlushAudioEncoder(); err != nil {
		runner.fail(fmt.Errorf("flushing audio: %w", err))
		return
	}

	// A Close failure is fatal: the trailer never lands and the file is
	// truncated.
	if err := runner.closeEncoder(); err != nil {
		runner.fail(err)
		return
	}

	runner.sendComplete()
}
