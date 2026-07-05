package main

import (
	"errors"
	"fmt"
	"image"
	"io"
	"math"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/harmonica"
	"github.com/linuxmatters/jive-visualiser/internal/audio"
	"github.com/linuxmatters/jive-visualiser/internal/config"
	"github.com/linuxmatters/jive-visualiser/internal/encoder"
	"github.com/linuxmatters/jive-visualiser/internal/renderer"
	"github.com/linuxmatters/jive-visualiser/internal/ui"
	"golang.org/x/image/font"
)

type pass2Runner struct {
	p       *tea.Program
	profile *audio.Profile
	cfg     pass2Config

	warnings []string

	reader    *audio.StreamingReader
	enc       *encoder.Encoder
	bgImage   *image.RGBA
	fontFace  font.Face
	processor *audio.Processor
	frame     *renderer.Frame

	numFrames          int
	totalVis           time.Duration
	totalEncode        time.Duration
	totalAudio         time.Duration
	renderStartTime    time.Time
	lastProgressUpdate time.Time
	audioCodecInfo     string

	prevBarHeights    []float64
	harmonicaSprings  []harmonica.Spring
	harmonicaPos      []float64
	harmonicaVel      []float64
	barHeights        []float64
	rearrangedHeights []float64
	barHeightsCopy    []float64

	previewImgs [2]*image.RGBA
	previewIdx  int

	sensitivity     float64
	samplesPerFrame int
	fftBuffer       []float64
	newSamples      []float64
	audioSamples    []float32
	stereo          bool
	stereoSamples   []float32
}

func newPass2Runner(p *tea.Program, profile *audio.Profile, cfg pass2Config) *pass2Runner {
	return &pass2Runner{
		p:           p,
		profile:     profile,
		cfg:         cfg,
		numFrames:   profile.NumFrames,
		sensitivity: 1.0,
	}
}

func (r *pass2Runner) fail(err error) {
	r.p.Send(ui.RenderComplete{Err: err, AssetWarnings: r.warnings})
}

func (r *pass2Runner) sendProgressIfDue(frameNum int, img *image.RGBA, interval time.Duration) {
	if !r.progressDue(interval) {
		return
	}

	r.lastProgressUpdate = time.Now()
	elapsed := time.Since(r.renderStartTime)
	r.p.Send(r.renderProgressMessage(frameNum, img, elapsed, r.currentOutputFileSize()))
}

func (r *pass2Runner) progressDue(interval time.Duration) bool {
	return time.Since(r.lastProgressUpdate) >= interval
}

func (r *pass2Runner) renderProgressMessage(frameNum int, img *image.RGBA, elapsed time.Duration, fileSize int64) ui.RenderProgress {
	copy(r.barHeightsCopy, r.rearrangedHeights)

	return ui.RenderProgress{
		Frame:       frameNum + 1,
		TotalFrames: r.numFrames,
		Elapsed:     elapsed,
		BarHeights:  r.barHeightsCopy,
		FileSize:    fileSize,
		Sensitivity: r.sensitivity,
		FrameData:   r.copyPreviewFrame(img),
		VideoCodec:  fmt.Sprintf("H.264 %d×%d", config.Width, config.Height),
		AudioCodec:  r.audioCodecInfo,
		EncoderName: r.enc.EncoderName(),
	}
}

func (r *pass2Runner) currentOutputFileSize() int64 {
	fileInfo, err := os.Stat(r.cfg.outputFile)
	if err != nil {
		return 0
	}
	return fileInfo.Size()
}

func (r *pass2Runner) copyPreviewFrame(img *image.RGBA) *image.RGBA {
	if r.cfg.noPreview {
		return nil
	}

	previewImg := r.previewImgs[r.previewIdx]
	copy(previewImg.Pix, img.Pix)
	r.previewIdx ^= 1
	return previewImg
}

func (r *pass2Runner) closeEncoder() error {
	if err := r.enc.Close(); err != nil {
		return fmt.Errorf("closing encoder: %w", err)
	}
	return nil
}

func (r *pass2Runner) sendComplete() {
	r.p.Send(r.renderCompleteMessage(r.currentOutputFileSize(), time.Since(r.cfg.overallStartTime)))
}

func (r *pass2Runner) renderCompleteMessage(fileSize int64, totalTime time.Duration) ui.RenderComplete {
	return ui.RenderComplete{
		OutputFile:       r.cfg.outputFile,
		FileSize:         fileSize,
		TotalFrames:      r.numFrames,
		VisTime:          r.totalVis,
		EncodeTime:       r.totalEncode,
		AudioTime:        r.totalAudio,
		TotalTime:        totalTime,
		ThumbnailTime:    r.cfg.thumbnailDuration,
		SamplesProcessed: int64(r.profile.SampleRate) * int64(r.profile.Duration),
		EncoderName:      r.enc.EncoderName(),
		EncoderIsHW:      r.enc.IsHardware(),
		AssetWarnings:    r.warnings,
	}
}

func (r *pass2Runner) setupReader() error {
	reader, err := audio.NewStreamingReader(r.cfg.inputFile)
	if err != nil {
		return fmt.Errorf("opening audio stream: %w", err)
	}
	r.reader = reader
	return nil
}

func (r *pass2Runner) setupEncoder() error {
	enc, err := encoder.New(encoder.Config{
		OutputPath:    r.cfg.outputFile,
		Width:         config.Width,
		Height:        config.Height,
		Framerate:     config.FPS,
		SampleRate:    r.reader.SampleRate(),
		AudioChannels: r.cfg.channels,
		HWAccel:       r.cfg.hwAccel,
	})
	if err != nil {
		return fmt.Errorf("creating encoder: %w", err)
	}

	if err = enc.Initialize(); err != nil {
		return fmt.Errorf("initialising encoder: %w", err)
	}

	r.enc = enc
	return nil
}

func (r *pass2Runner) loadAssets() {
	bgImage, err := renderer.LoadBackgroundImage(r.cfg.runtimeConfig)
	if err != nil {
		bgImage = nil
		if _, isCustom := r.cfg.runtimeConfig.GetBackgroundImagePath(); isCustom {
			r.warnings = append(r.warnings, fmt.Sprintf("could not load background image, rendering without it: %v", err))
		} else {
			r.warnings = append(r.warnings, fmt.Sprintf("could not load embedded default background, rendering without it: %v", err))
		}
	}
	r.bgImage = bgImage

	fontFace, err := renderer.LoadFont(48)
	if err != nil {
		fontFace = nil
		r.warnings = append(r.warnings, fmt.Sprintf("could not load embedded font, rendering without centre text: %v", err))
	}
	r.fontFace = fontFace
}

func (r *pass2Runner) setupProcessorAndFrame() error {
	processor, err := audio.NewProcessor()
	if err != nil {
		return fmt.Errorf("creating FFT processor: %w", err)
	}

	r.processor = processor
	r.frame = renderer.NewFrame(r.bgImage, r.fontFace, r.cfg.meta, r.cfg.runtimeConfig)
	return nil
}

func (r *pass2Runner) setupTimingAndDisplay() {
	r.renderStartTime = time.Now()
	r.lastProgressUpdate = r.renderStartTime

	audioSampleRate := r.reader.SampleRate()
	audioChannelStr := "mono"
	if r.cfg.channels == 2 {
		audioChannelStr = "stereo"
	}
	r.audioCodecInfo = fmt.Sprintf("AAC %.1f㎑ %s", float64(audioSampleRate)/1000.0, audioChannelStr)
}

func (r *pass2Runner) setupRenderState() {
	r.prevBarHeights = make([]float64, config.NumBars)

	const (
		harmonicaSpringFreq    = 6.0
		harmonicaSpringDamping = 1.0
	)
	harmonicaDelta := 1.0 / config.Framerate
	r.harmonicaSprings = make([]harmonica.Spring, config.NumBars)
	for i := range r.harmonicaSprings {
		r.harmonicaSprings[i] = harmonica.NewSpring(harmonicaDelta, harmonicaSpringFreq, harmonicaSpringDamping)
	}
	r.harmonicaPos = make([]float64, config.NumBars)
	r.harmonicaVel = make([]float64, config.NumBars)

	r.barHeights = make([]float64, config.NumBars)
	r.rearrangedHeights = make([]float64, config.NumBars)
	r.barHeightsCopy = make([]float64, config.NumBars)
}

func (r *pass2Runner) setupPreviewBuffers() {
	if r.cfg.noPreview {
		return
	}
	r.previewImgs[0] = image.NewRGBA(image.Rect(0, 0, config.Width, config.Height))
	r.previewImgs[1] = image.NewRGBA(image.Rect(0, 0, config.Width, config.Height))
}

func (r *pass2Runner) setupAudioBuffers() {
	r.samplesPerFrame = r.reader.SampleRate() / config.FPS
	r.fftBuffer = make([]float64, config.FFTSize)

	convBufLen := audioConvBufLen(r.samplesPerFrame)
	r.newSamples = make([]float64, r.samplesPerFrame)
	r.audioSamples = make([]float32, convBufLen)
	r.stereo = r.cfg.channels == 2
	if r.stereo {
		r.stereoSamples = make([]float32, convBufLen*2)
	}
}

func (r *pass2Runner) prefillFFT() error {
	n, err := audio.FillFFTBuffer(r.reader, r.fftBuffer)
	if err != nil {
		return fmt.Errorf("reading initial audio chunk: %w", err)
	}
	if n == 0 {
		return errors.New("no audio data available")
	}

	if err := convertAndWriteAudio(r.enc.WriteAudioSamples, r.fftBuffer, n, r.stereo, r.audioSamples, r.stereoSamples); err != nil {
		return fmt.Errorf("writing initial audio: %w", err)
	}
	return nil
}

func (r *pass2Runner) renderFrame() *image.RGBA {
	t0 := time.Now()

	r.processBars(r.fftBuffer[:config.FFTSize])
	r.frame.Draw(r.rearrangedHeights)

	r.totalVis += time.Since(t0)
	return r.frame.GetImage()
}

func (r *pass2Runner) processBars(chunk []float64) {
	coeffs := r.processor.ProcessChunk(chunk)
	audio.BinFFT(coeffs, r.sensitivity, r.profile.OptimalBaseScale, r.barHeights)

	r.applySensitivity()
	availableHeight := r.scaleBars()
	r.applySpringDynamics(availableHeight)
	audio.RearrangeFrequenciesCenterOut(r.prevBarHeights, r.rearrangedHeights)
}

func (r *pass2Runner) applySensitivity() {
	overshootDetected := false

	for i, h := range r.barHeights {
		if h > config.OvershootThreshold {
			overshootDetected = true
			overshoot := h - config.OvershootThreshold
			r.barHeights[i] = config.OvershootThreshold + overshoot*math.Exp(-overshoot/config.OvershootThreshold)
		}
	}

	if overshootDetected {
		r.sensitivity *= config.SensitivityDecay
	} else {
		r.sensitivity *= config.SensitivityGrowth
	}

	if r.sensitivity < config.SensitivityMin {
		r.sensitivity = config.SensitivityMin
	}
	if r.sensitivity > config.SensitivityMax {
		r.sensitivity = config.SensitivityMax
	}
}

func (r *pass2Runner) scaleBars() float64 {
	actualAvailableSpace := float64(config.Height/2 - config.CenterGap/2)
	availableHeight := actualAvailableSpace * config.MaxBarHeight
	for i := range r.barHeights {
		r.barHeights[i] *= availableHeight
	}
	return availableHeight
}

func (r *pass2Runner) applySpringDynamics(availableHeight float64) {
	const harmonicaGain = 2.0

	for i := range r.barHeights {
		currentHeight := r.barHeights[i] * harmonicaGain

		if currentHeight >= r.harmonicaPos[i] {
			r.harmonicaPos[i] = currentHeight
			r.harmonicaVel[i] = 0
		} else {
			r.harmonicaPos[i], r.harmonicaVel[i] = r.harmonicaSprings[i].Update(
				r.harmonicaPos[i], r.harmonicaVel[i], currentHeight)
			if r.harmonicaPos[i] < 0 {
				r.harmonicaPos[i] = 0
				r.harmonicaVel[i] = 0
			}
		}

		heldHeight := r.harmonicaPos[i]
		if heldHeight > availableHeight {
			overshoot := heldHeight - availableHeight
			heldHeight = availableHeight + overshoot*math.Exp(-overshoot/availableHeight)
		}

		r.prevBarHeights[i] = heldHeight
	}
}

func (r *pass2Runner) writeVideoFrame(frameNum int, img *image.RGBA) error {
	t0 := time.Now()
	if err := r.enc.WriteFrameRGBA(img.Pix); err != nil {
		return fmt.Errorf("encoding frame %d: %w", frameNum, err)
	}
	r.totalEncode += time.Since(t0)
	return nil
}

func (r *pass2Runner) processNextAudioFrame(frameNum int) (bool, error) {
	t0 := time.Now()
	nRead, readErr := audio.ReadNextFrame(r.reader, r.newSamples)
	if readErr != nil {
		if errors.Is(readErr, io.EOF) {
			r.totalAudio += time.Since(t0)
			return false, nil
		}
		return false, fmt.Errorf("reading audio: %w", readErr)
	}

	if err := r.writeAudioSamples(frameNum, nRead); err != nil {
		return false, err
	}
	audio.SlideFFTWindow(r.fftBuffer, r.newSamples, nRead)
	r.totalAudio += time.Since(t0)
	return true, nil
}

func (r *pass2Runner) writeAudioSamples(frameNum int, nRead int) error {
	if err := convertAndWriteAudio(r.enc.WriteAudioSamples, r.newSamples, nRead, r.stereo, r.audioSamples, r.stereoSamples); err != nil {
		return fmt.Errorf("writing audio at frame %d: %w", frameNum, err)
	}
	return nil
}
