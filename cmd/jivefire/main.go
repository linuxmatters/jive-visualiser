package main

import (
	"errors"
	"fmt"
	"image"
	"io"
	"math"
	"os"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/alecthomas/kong"
	"github.com/charmbracelet/harmonica"
	"github.com/linuxmatters/jivefire/internal/audio"
	"github.com/linuxmatters/jivefire/internal/cli"
	"github.com/linuxmatters/jivefire/internal/config"
	"github.com/linuxmatters/jivefire/internal/encoder"
	"github.com/linuxmatters/jivefire/internal/renderer"
	"github.com/linuxmatters/jivefire/internal/ui"
)

// version is set via ldflags at build time
// Local dev builds: "dev"
// Release builds: git tag (e.g. "v0.1.0")
var version = "dev"

var CLI struct {
	Input           string `arg:"" name:"input" help:"Input WAV file" optional:""`
	Output          string `arg:"" name:"output" help:"Output MP4 file" optional:""`
	Episode         int    `help:"Episode number" default:"0"`
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
		kong.Name("jivefire"),
		kong.Description("Spin your podcast .wav into a groovy MP4 visualiser."),
		kong.Vars{"version": version},
		kong.UsageOnError(),
		kong.Help(cli.StyledHelpPrinter(kong.HelpOptions{Compact: true})),
	)

	// Handle version flag
	if CLI.Version {
		cli.PrintVersion(version)
		os.Exit(0)
	}

	// Handle probe flag - display hardware encoder status
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

	// Show help if no arguments provided
	if CLI.Input == "" && CLI.Output == "" {
		_ = ctx.PrintUsage(true)
		os.Exit(0)
	}

	// Validate required arguments
	if CLI.Input == "" || CLI.Output == "" {
		cli.PrintError("<input> and <output> are required")
		os.Exit(1)
	}

	// Validate input file exists
	if _, err := os.Stat(CLI.Input); os.IsNotExist(err) {
		cli.PrintError(fmt.Sprintf("input file does not exist: %s", CLI.Input))
		os.Exit(1)
	}

	// Validate channels
	if CLI.Channels != 1 && CLI.Channels != 2 {
		cli.PrintError(fmt.Sprintf("invalid channels value: %d (must be 1 or 2)", CLI.Channels))
		os.Exit(1)
	}

	// Validate encoder option
	validEncoders := map[string]encoder.HWAccelType{
		"auto":     encoder.HWAccelAuto,
		"nvenc":    encoder.HWAccelNVENC,
		"qsv":      encoder.HWAccelQSV,
		"vaapi":    encoder.HWAccelVAAPI,
		"vulkan":   encoder.HWAccelVulkan,
		"software": encoder.HWAccelNone,
	}
	hwAccelType, ok := validEncoders[CLI.Encoder]
	if !ok {
		cli.PrintError(fmt.Sprintf("invalid --encoder value: %s (must be auto, nvenc, qsv, vaapi, vulkan, or software)", CLI.Encoder))
		os.Exit(1)
	}

	// If user explicitly requested a specific hardware encoder, verify it's available
	if hwAccelType != encoder.HWAccelAuto && hwAccelType != encoder.HWAccelNone {
		selectedEncoder := encoder.SelectBestEncoder(hwAccelType)
		if selectedEncoder == nil {
			// Requested encoder not available - list what IS available
			encoders := encoder.DetectHWEncoders()
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

	// Build runtime config from CLI arguments
	runtimeConfig := &config.RuntimeConfig{}

	// Parse and validate bar color if provided
	if CLI.BarColor != "" {
		r, g, b, err := config.ParseHexColor(CLI.BarColor)
		if err != nil {
			cli.PrintError(fmt.Sprintf("invalid --bar-color: %v", err))
			os.Exit(1)
		}
		runtimeConfig.BarColor = config.OptionalColor{R: r, G: g, B: b, Set: true}
	}

	// Parse and validate text color if provided
	if CLI.TextColor != "" {
		r, g, b, err := config.ParseHexColor(CLI.TextColor)
		if err != nil {
			cli.PrintError(fmt.Sprintf("invalid --text-color: %v", err))
			os.Exit(1)
		}
		runtimeConfig.TextColor = config.OptionalColor{R: r, G: g, B: b, Set: true}
	}

	// Validate background image if provided
	if CLI.BackgroundImage != "" {
		if _, err := os.Stat(CLI.BackgroundImage); os.IsNotExist(err) {
			cli.PrintError(fmt.Sprintf("background image does not exist: %s", CLI.BackgroundImage))
			os.Exit(1)
		}
		runtimeConfig.BackgroundImagePath = CLI.BackgroundImage
	}

	// Validate thumbnail image if provided
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

func generateVideo(inputFile string, outputFile string, channels int, noPreview bool, hwAccel encoder.HWAccelType, runtimeConfig *config.RuntimeConfig, meta renderer.PodcastMeta) {
	// Track overall timing from the very start
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

	// Calculate estimated total frames for Pass 1 progress
	samplesPerFrame := config.SampleRate / config.FPS
	estimatedTotalFrames := int(metadata.NumSamples) / samplesPerFrame

	// Create unified Bubbletea program for both passes
	// Alternate screen buffer (set via View().AltScreen) prevents ghost box
	// edges when the view height changes between passes
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
			// Send progress update to unified UI
			p.Send(ui.AnalysisProgress{
				Frame:       frame,
				TotalFrames: estimatedTotalFrames, // Use pre-calculated estimate
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

	// Run the unified Bubbletea UI (uses alternate screen buffer)
	finalModel, err := p.Run()
	if err != nil {
		cli.PrintError(fmt.Sprintf("running UI: %v", err))
		os.Exit(1)
	}

	// Print completion summary after exiting alternate screen
	if m, ok := finalModel.(*ui.Model); ok {
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

func runPass2(p *tea.Program, profile *audio.Profile, cfg pass2Config) {
	// Open streaming reader for Pass 2
	reader, err := audio.NewStreamingReader(cfg.inputFile)
	if err != nil {
		cli.PrintError(fmt.Sprintf("opening audio stream: %v", err))
		p.Quit()
		return
	}
	defer reader.Close()

	// Initialize encoder with video and audio (using new sample-based API)
	enc, err := encoder.New(encoder.Config{
		OutputPath:    cfg.outputFile,
		Width:         config.Width,
		Height:        config.Height,
		Framerate:     config.FPS,
		SampleRate:    reader.SampleRate(), // Use sample rate from audio file
		AudioChannels: cfg.channels,        // Mono (1) or stereo (2)
		HWAccel:       cfg.hwAccel,         // Hardware acceleration type
	})
	if err != nil {
		cli.PrintError(fmt.Sprintf("creating encoder: %v", err))
		p.Quit()
		return
	}

	err = enc.Initialize()
	if err != nil {
		cli.PrintError(fmt.Sprintf("initializing encoder: %v", err))
		p.Quit()
		return
	}

	defer enc.Close()

	// Load background image (custom or embedded)
	bgImage, err := renderer.LoadBackgroundImage(cfg.runtimeConfig)
	if err != nil {
		bgImage = nil
	}

	// Load font for center text (embedded)
	fontFace, err := renderer.LoadFont(48)
	if err != nil {
		fontFace = nil
	}

	// Create audio processor and frame renderer
	processor := audio.NewProcessor()
	frame := renderer.NewFrame(bgImage, fontFace, cfg.meta, cfg.runtimeConfig)

	// Calculate frames from profile
	numFrames := profile.NumFrames

	// Profiling variables
	var totalVis, totalEncode, totalAudio time.Duration
	renderStartTime := time.Now()
	lastProgressUpdate := renderStartTime
	const progressUpdateInterval = 30 * time.Millisecond

	// Get audio format information for codec display
	audioSampleRate := reader.SampleRate()
	// Use output channel count (from CLI), not input channel count
	audioChannelStr := "mono"
	if cfg.channels == 2 {
		audioChannelStr = "stereo"
	}
	audioCodecInfo := fmt.Sprintf("AAC %.1fkHz %s", float64(audioSampleRate)/1000.0, audioChannelStr)

	// Harmonica spring peak-hold state. Each bar rises INSTANTLY to a new high,
	// then springs DOWN toward the raw level over subsequent frames. The spring
	// delta is locked to the video frame interval (1/FPS) so the fall rate is
	// framerate-independent.
	prevBarHeights := make([]float64, config.NumBars)
	const (
		harmonicaSpringFreq    = 6.0
		harmonicaSpringDamping = 1.0
		// harmonicaGain lifts the spring bars to a fuller amplitude. The peak-hold
		// path has no integrator, so bars otherwise peak at the raw scaled height
		// and look short (the old leaky-integrator path had a steady-state gain of
		// roughly 4.3x for free). The existing soft-knee compression caps the loud
		// bars, so this keeps dynamic spread rather than flattening everything to
		// full height. Tune for taste.
		harmonicaGain = 2.0
	)
	harmonicaDelta := 1.0 / config.Framerate
	harmonicaSprings := make([]harmonica.Spring, config.NumBars)
	for i := range harmonicaSprings {
		harmonicaSprings[i] = harmonica.NewSpring(harmonicaDelta, harmonicaSpringFreq, harmonicaSpringDamping)
	}
	harmonicaPos := make([]float64, config.NumBars)
	harmonicaVel := make([]float64, config.NumBars)

	// Pre-allocate reusable buffers to avoid allocations in render loop
	barHeights := make([]float64, config.NumBars)
	rearrangedHeights := make([]float64, config.NumBars)
	barHeightsCopy := make([]float64, config.NumBars) // For UI updates

	// Auto-sensitivity adjustment
	sensitivity := 1.0

	// Sliding buffer for FFT: we read samplesPerFrame but need FFTSize for FFT
	samplesPerFrame := config.SampleRate / config.FPS
	fftBuffer := make([]float64, config.FFTSize)

	// Pre-allocate reusable buffers for audio processing (avoid per-frame allocations)
	newSamples := make([]float64, samplesPerFrame)
	audioSamples := make([]float32, samplesPerFrame)

	// Pre-fill buffer with first chunk
	n, err := audio.FillFFTBuffer(reader, fftBuffer)
	if err != nil {
		cli.PrintError(fmt.Sprintf("error reading initial audio chunk: %v", err))
		p.Quit()
		return
	}
	if n == 0 {
		cli.PrintError("no audio data available")
		p.Quit()
		return
	}

	// Write initial audio samples to encoder (first samplesPerFrame worth).
	// This corresponds to the audio for frame 0. Reuse the audioSamples buffer:
	// WriteAudioSamples copies into the FIFO and retains no reference, and the
	// buffer is overwritten before each later use in the render loop.
	initialCount := min(samplesPerFrame, n)
	for i := range initialCount {
		audioSamples[i] = float32(fftBuffer[i])
	}
	if err := enc.WriteAudioSamples(audioSamples[:initialCount]); err != nil {
		cli.PrintError(fmt.Sprintf("error writing initial audio: %v", err))
		p.Quit()
		return
	}

	// Process frames until we run out of audio
	frameNum := 0
	for frameNum < numFrames {
		// Use current buffer for FFT
		chunk := fftBuffer[:config.FFTSize]

		// === VISUALISATION TIMING START ===
		t0 := time.Now()

		// Compute FFT
		coeffs := processor.ProcessChunk(chunk)

		// Compute magnitudes and bin into bars using optimal baseScale from Pass 1
		audio.BinFFT(coeffs, sensitivity, profile.OptimalBaseScale, barHeights)

		// Auto-sensitivity with soft knee compression
		overshootDetected := false

		for i, h := range barHeights {
			if h > config.OvershootThreshold {
				overshootDetected = true
				// Apply soft knee compression
				overshoot := h - config.OvershootThreshold
				barHeights[i] = config.OvershootThreshold + overshoot*math.Exp(-overshoot/config.OvershootThreshold)
			}
		}

		// Adjust sensitivity
		if overshootDetected {
			sensitivity *= config.SensitivityDecay
		} else {
			sensitivity *= config.SensitivityGrowth
		}

		// Clamp sensitivity
		if sensitivity < config.SensitivityMin {
			sensitivity = config.SensitivityMin
		}
		if sensitivity > config.SensitivityMax {
			sensitivity = config.SensitivityMax
		}

		// Scale to pixel space
		actualAvailableSpace := float64(config.Height/2 - config.CenterGap/2)
		availableHeight := actualAvailableSpace * config.MaxBarHeight
		for i := range barHeights {
			barHeights[i] *= availableHeight
		}

		// Harmonica peak-hold dynamic. Each bar rises instantly to a new peak, then
		// springs down toward the raw level. Writes into prevBarHeights so the
		// downstream rearrange/draw pipeline stays unchanged.
		for i := range barHeights {
			// Apply the spring-path gain so bars reach a fuller amplitude; the
			// soft-knee below caps the loud ones, preserving dynamic spread.
			currentHeight := barHeights[i] * harmonicaGain

			if currentHeight >= harmonicaPos[i] {
				// Instant rise to the new peak; reset velocity so the fall starts
				// from rest.
				harmonicaPos[i] = currentHeight
				harmonicaVel[i] = 0
			} else {
				harmonicaPos[i], harmonicaVel[i] = harmonicaSprings[i].Update(
					harmonicaPos[i], harmonicaVel[i], currentHeight)
				if harmonicaPos[i] < 0 {
					harmonicaPos[i] = 0
					harmonicaVel[i] = 0
				}
			}

			heldHeight := harmonicaPos[i]

			// Soft knee compression
			if heldHeight > availableHeight {
				overshoot := heldHeight - availableHeight
				heldHeight = availableHeight + overshoot*math.Exp(-overshoot/availableHeight)
			}

			prevBarHeights[i] = heldHeight
		}

		// Rearrange frequencies for center-out distribution
		audio.RearrangeFrequenciesCenterOut(prevBarHeights, rearrangedHeights)

		// Generate frame image
		frame.Draw(rearrangedHeights)
		totalVis += time.Since(t0)
		// === VISUALISATION TIMING END ===

		// === VIDEO ENCODING TIMING START ===
		t0 = time.Now()
		img := frame.GetImage()
		if err := enc.WriteFrameRGBA(img.Pix); err != nil {
			cli.PrintError(fmt.Sprintf("error encoding frame %d: %v", frameNum, err))
			p.Quit()
			return
		}
		totalEncode += time.Since(t0)
		// === VIDEO ENCODING TIMING END ===

		// Time-based UI updates (10Hz) - minimal overhead, not timed
		if time.Since(lastProgressUpdate) >= progressUpdateInterval {
			lastProgressUpdate = time.Now()
			elapsed := time.Since(renderStartTime)

			// Copy bar heights for UI (use rearranged for better visual)
			// Uses pre-allocated barHeightsCopy buffer
			copy(barHeightsCopy, rearrangedHeights)

			// Get actual file size from disk (not an estimate)
			var currentFileSize int64
			if fileInfo, err := os.Stat(cfg.outputFile); err == nil {
				currentFileSize = fileInfo.Size()
			}

			// Include frame data for preview (skip if disabled)
			var frameData *image.RGBA
			if !cfg.noPreview {
				frameData = img
			}

			p.Send(ui.RenderProgress{
				Frame:       frameNum + 1,
				TotalFrames: numFrames,
				Elapsed:     elapsed,
				BarHeights:  barHeightsCopy,
				FileSize:    currentFileSize,
				Sensitivity: sensitivity,
				FrameData:   frameData,
				VideoCodec:  fmt.Sprintf("H.264 %d×%d", config.Width, config.Height),
				AudioCodec:  audioCodecInfo,
				EncoderName: enc.EncoderName(),
			})
		}

		// Advance to next frame
		frameNum++

		// === AUDIO TIMING START ===
		// Read audio, encode, and manage buffer for next frame
		t0 = time.Now()
		nRead, readErr := audio.ReadNextFrame(reader, newSamples)
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				totalAudio += time.Since(t0)
				break
			}
			cli.PrintError(fmt.Sprintf("error reading audio: %v", readErr))
			p.Quit()
			return
		}

		// Write audio samples for this frame to encoder
		// Convert float64 samples to float32 for AAC encoder
		// Uses pre-allocated audioSamples buffer, slice to actual length
		for i := range nRead {
			audioSamples[i] = float32(newSamples[i])
		}
		if err := enc.WriteAudioSamples(audioSamples[:nRead]); err != nil {
			cli.PrintError(fmt.Sprintf("error writing audio at frame %d: %v", frameNum, err))
			p.Quit()
			return
		}
		// Shift buffer left by samplesPerFrame, append new samples
		copy(fftBuffer, fftBuffer[samplesPerFrame:])
		// Pad with zeros if we got fewer samples than expected
		if nRead < samplesPerFrame {
			copy(fftBuffer[config.FFTSize-samplesPerFrame:], newSamples[:nRead])
			// Zero-fill the remaining space
			clear(fftBuffer[config.FFTSize-samplesPerFrame+nRead:])
		} else {
			copy(fftBuffer[config.FFTSize-samplesPerFrame:], newSamples[:nRead])
		}
		totalAudio += time.Since(t0)
		// === AUDIO TIMING END ===
	}

	// Flush any remaining audio after all video frames are written
	// This encodes any samples remaining in the FIFO and flushes the encoder
	if err := enc.FlushAudioEncoder(); err != nil {
		cli.PrintError(fmt.Sprintf("error flushing audio: %v", err))
		p.Quit()
		return
	}

	// Finalize encoding
	if err := enc.Close(); err != nil {
		cli.PrintError(fmt.Sprintf("error closing encoder: %v", err))
		p.Quit()
		return
	}

	// Get actual file size
	fileInfo, err := os.Stat(cfg.outputFile)
	var actualFileSize int64
	if err == nil {
		actualFileSize = fileInfo.Size()
	}

	// Calculate samples processed (sample rate * duration)
	samplesProcessed := int64(profile.SampleRate) * int64(profile.Duration)

	// Calculate overall total time from the very beginning
	overallTotalTime := time.Since(cfg.overallStartTime)

	// Send completion message
	p.Send(ui.RenderComplete{
		OutputFile:       cfg.outputFile,
		FileSize:         actualFileSize,
		TotalFrames:      numFrames,
		VisTime:          totalVis,
		EncodeTime:       totalEncode,
		AudioTime:        totalAudio,
		TotalTime:        overallTotalTime,
		ThumbnailTime:    cfg.thumbnailDuration,
		SamplesProcessed: samplesProcessed,
		EncoderName:      enc.EncoderName(),
		EncoderIsHW:      enc.IsHardware(),
	})
}
