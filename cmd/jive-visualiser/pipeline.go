package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/linuxmatters/jive-visualiser/internal/audio"
	"github.com/linuxmatters/jive-visualiser/internal/cli"
	"github.com/linuxmatters/jive-visualiser/internal/config"
	"github.com/linuxmatters/jive-visualiser/internal/renderer"
	"github.com/linuxmatters/jive-visualiser/internal/ui"
)

func generateVideo(cfg *runConfig) {
	overallStartTime := time.Now()

	thumbnailDuration, err := generateThumbnail(cfg)
	if err != nil {
		cli.PrintError(fmt.Sprintf("failed to generate thumbnail: %v", err))
		os.Exit(1)
	}

	estimatedTotalFrames, err := estimateAnalysisFrames(cfg.inputFile)
	if err != nil {
		cli.PrintError(err.Error())
		os.Exit(1)
	}

	p := newProgressProgram(cfg.noPreview)
	analysisErrCh := startPasses(p, cfg, estimatedTotalFrames, thumbnailDuration, overallStartTime)

	finalModel, err := p.Run()
	if err != nil {
		cli.PrintError(fmt.Sprintf("running UI: %v", err))
		os.Exit(1)
	}

	interrupted, err := printFinalModel(finalModel)
	if err != nil {
		cli.PrintError(err.Error())
		os.Exit(1)
	}
	if interrupted {
		return
	}

	if analysisErr := <-analysisErrCh; analysisErr != nil {
		cli.PrintError(fmt.Sprintf("analysing audio: %v", analysisErr))
		os.Exit(1)
	}
}

func generateThumbnail(cfg *runConfig) (time.Duration, error) {
	thumbnailStartTime := time.Now()
	if err := renderer.GenerateThumbnail(thumbnailOutputPath(cfg.outputFile), cfg.meta, cfg.runtimeConfig); err != nil {
		return 0, err
	}
	return time.Since(thumbnailStartTime), nil
}

func estimateAnalysisFrames(inputFile string) (int, error) {
	// Get audio metadata upfront for Pass 1 progress estimation.
	metadata, err := audio.GetMetadata(inputFile)
	if err != nil {
		return 0, fmt.Errorf("reading audio metadata: %w", err)
	}

	// Use the file's actual sample rate so each frame maps to 1/FPS seconds of
	// audio regardless of input rate.
	samplesPerFrame := metadata.SampleRate / config.FPS
	if samplesPerFrame <= 0 {
		return 0, fmt.Errorf("input sample rate too low for %d FPS: %d Hz", config.FPS, metadata.SampleRate)
	}
	return int(metadata.NumSamples) / samplesPerFrame, nil
}

func newProgressProgram(noPreview bool) *tea.Program {
	// The alternate screen buffer (set via View().AltScreen) prevents ghost box
	// edges when the view height changes between passes.
	return tea.NewProgram(ui.NewModel(noPreview))
}

func startPasses(p *tea.Program, cfg *runConfig, estimatedTotalFrames int, thumbnailDuration time.Duration, overallStartTime time.Time) <-chan error {
	errCh := make(chan error, 1)
	go func() {
		errCh <- runAnalysisAndPass2(p, cfg, estimatedTotalFrames, thumbnailDuration, overallStartTime)
	}()
	return errCh
}

func runAnalysisAndPass2(p *tea.Program, cfg *runConfig, estimatedTotalFrames int, thumbnailDuration time.Duration, overallStartTime time.Time) error {
	pass1StartTime := time.Now()

	profile, err := audio.AnalyzeAudio(cfg.inputFile, func(frame int, currentRMS, currentPeak float64, barHeights []float64, duration time.Duration) {
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
	if err != nil {
		p.Quit()
		return err
	}

	// Signal Pass 1 complete - this transitions the UI to Pass 2.
	p.Send(ui.AnalysisComplete{
		PeakMagnitude: profile.GlobalPeak,
		RMSLevel:      profile.GlobalRMS,
		DynamicRange:  profile.DynamicRange,
		Duration:      time.Duration(float64(time.Second) * profile.Duration),
		OptimalScale:  profile.OptimalBaseScale,
		AnalysisTime:  pass1Duration,
	})

	runPass2(p, profile, pass2Config{
		inputFile:         cfg.inputFile,
		outputFile:        cfg.outputFile,
		channels:          cfg.channels,
		noPreview:         cfg.noPreview,
		hwAccel:           cfg.hwAccel,
		runtimeConfig:     cfg.runtimeConfig,
		meta:              cfg.meta,
		thumbnailDuration: thumbnailDuration,
		overallStartTime:  overallStartTime,
	})
	return nil
}

func printFinalModel(finalModel tea.Model) (bool, error) {
	// Surface results from the final model now the alt screen is gone. The
	// warnings travelled on the RenderComplete message, so reading them here is
	// synchronised by p.Run() returning.
	if m, ok := finalModel.(*ui.Model); ok {
		for _, w := range m.AssetWarnings() {
			cli.PrintWarning(w)
		}
		if renderErr := m.RenderError(); renderErr != nil {
			return false, renderErr
		}
		if summary := m.CompletionSummary(); summary != "" {
			fmt.Println(summary)
		}
		return m.Interrupted(), nil
	}
	return false, nil
}

func thumbnailOutputPath(outputFile string) string {
	ext := filepath.Ext(outputFile)
	if ext == "" {
		return outputFile + ".png"
	}
	return strings.TrimSuffix(outputFile, ext) + ".png"
}
