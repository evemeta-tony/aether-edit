// services/orchestrator/internal/engine/ffmpegadapter/adapter.go
//
// Adapter 1 for the TranscodeEngine interface: FFmpeg driven via os/exec
// argv arrays (S3). Binary paths come from configuration. The constructor
// runs the buildconf license gate and refuses to construct against a gpl or
// nonfree build (see buildconf.go).
package ffmpegadapter

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/engine"
)

// Config holds the adapter configuration.
type Config struct {
	// FFmpegPath is the ffmpeg binary path (from service config).
	FFmpegPath string
	// FFprobePath is the ffprobe binary path (from service config).
	FFprobePath string
}

// Adapter implements engine.TranscodeEngine with FFmpeg.
type Adapter struct {
	cfg    Config
	runner CommandRunner
}

// compile-time interface check.
var _ engine.TranscodeEngine = (*Adapter)(nil)

// New constructs the adapter and runs the startup buildconf gate. It returns
// ErrForbiddenBuild (wrapped) when the ffmpeg build advertises gpl or
// nonfree; callers must treat that as fatal.
func New(ctx context.Context, cfg Config, runner CommandRunner) (*Adapter, error) {
	if cfg.FFmpegPath == "" || cfg.FFprobePath == "" {
		return nil, fmt.Errorf("ffmpegadapter: FFmpegPath and FFprobePath are required")
	}
	if runner == nil {
		runner = OSRunner{}
	}
	a := &Adapter{cfg: cfg, runner: runner}
	if err := a.verifyBuild(ctx); err != nil {
		return nil, err
	}
	return a, nil
}

// ffprobe JSON output shapes (adapter-internal; not exported per R3).
type ffprobeOut struct {
	Format struct {
		FormatName string `json:"format_name"`
		Duration   string `json:"duration"`
		BitRate    string `json:"bit_rate"`
	} `json:"format"`
	Streams []struct {
		CodecType string `json:"codec_type"`
		CodecName string `json:"codec_name"`
		Width     int    `json:"width"`
		Height    int    `json:"height"`
		PixFmt    string `json:"pix_fmt"`
	} `json:"streams"`
}

// Probe implements engine.TranscodeEngine via ffprobe with JSON output.
func (a *Adapter) Probe(ctx context.Context, inputPath string) (engine.MediaInfo, error) {
	args := []string{
		"-hide_banner",
		"-v", "error",
		"-print_format", "json",
		"-show_format",
		"-show_streams",
		inputPath,
	}
	stdout, stderr, err := a.runner.Output(ctx, a.cfg.FFprobePath, args)
	if err != nil {
		return engine.MediaInfo{}, &engine.Error{
			Stage:   engine.StageDecode,
			Message: fmt.Sprintf("ffprobe failed: %v: %s", err, firstLine(stderr)),
		}
	}
	var out ffprobeOut
	if err := json.Unmarshal(stdout, &out); err != nil {
		return engine.MediaInfo{}, &engine.Error{
			Stage:   engine.StageDecode,
			Message: fmt.Sprintf("ffprobe output parse: %v", err),
		}
	}
	mi := engine.MediaInfo{Container: out.Format.FormatName}
	if out.Format.Duration != "" {
		if f, err := strconv.ParseFloat(out.Format.Duration, 64); err == nil {
			mi.DurationSeconds = f
		}
	}
	if out.Format.BitRate != "" {
		if n, err := strconv.ParseInt(out.Format.BitRate, 10, 64); err == nil {
			mi.SourceBitrateBps = n
		}
	}
	for _, s := range out.Streams {
		switch s.CodecType {
		case "video":
			mi.VideoStreams++
			if mi.VideoCodec == "" {
				mi.VideoCodec = s.CodecName
				mi.Width = s.Width
				mi.Height = s.Height
				mi.ChromaSubsampling = chromaFromPixFmt(s.PixFmt)
			}
		case "audio":
			mi.AudioStreams++
			if mi.AudioCodec == "" {
				mi.AudioCodec = s.CodecName
			}
		case "subtitle":
			mi.SubtitleStreams++
		}
	}
	if mi.VideoStreams == 0 {
		return engine.MediaInfo{}, &engine.Error{
			Stage:   engine.StageAsset,
			Message: "source has no video stream",
		}
	}
	return mi, nil
}

// chromaFromPixFmt derives the chroma subsampling label from a pixel format.
func chromaFromPixFmt(pixFmt string) string {
	switch {
	case pixFmt == "":
		return ""
	case strings.Contains(pixFmt, "444"):
		return "4:4:4"
	case strings.Contains(pixFmt, "422"):
		return "4:2:2"
	case strings.Contains(pixFmt, "420"):
		return "4:2:0"
	case strings.Contains(pixFmt, "gray"):
		return "4:0:0"
	}
	return pixFmt
}

// Transcode implements engine.TranscodeEngine: it encodes one ladder rung,
// streaming -progress samples from the stdout pipe.
func (a *Adapter) Transcode(ctx context.Context, inputPath string, out engine.OutputSpec, onProgress func(engine.Progress)) error {
	args, _, err := buildTranscodeArgs(inputPath, out)
	if err != nil {
		return &engine.Error{Stage: engine.StageEncode, Message: err.Error()}
	}
	proc, err := a.runner.Start(ctx, a.cfg.FFmpegPath, args)
	if err != nil {
		return &engine.Error{Stage: engine.StageEncode, Message: fmt.Sprintf("start ffmpeg: %v", err)}
	}
	scanErr := ScanProgress(proc.Stdout(), func(s ProgressSample) {
		if onProgress == nil {
			return
		}
		pct, eta := DeriveProgress(s, out.SourceDurationSeconds)
		onProgress(engine.Progress{
			RungName:       out.RungName,
			FPS:            s.FPS,
			SpeedX:         s.SpeedX,
			OutTimeSeconds: s.OutTimeSeconds,
			ProgressPct:    pct,
			ETASeconds:     eta,
		})
	})
	stderr, waitErr := proc.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if waitErr != nil {
		return classifyFailure(stderr, waitErr)
	}
	if scanErr != nil {
		return &engine.Error{Stage: engine.StageEncode, Message: fmt.Sprintf("progress stream: %v", scanErr)}
	}
	return nil
}

// classifyFailure maps ffmpeg stderr onto the error taxonomy. The mapping is
// keyword based and best effort; anything unrecognized is classified as an
// encode failure because the process was already past input open.
func classifyFailure(stderr []byte, waitErr error) error {
	text := strings.ToLower(string(stderr))
	msg := fmt.Sprintf("ffmpeg exited: %v: %s", waitErr, firstNonEmptyTail(string(stderr)))
	switch {
	case strings.Contains(text, "no such file or directory"),
		strings.Contains(text, "invalid data found when processing input"),
		strings.Contains(text, "moov atom not found"),
		strings.Contains(text, "permission denied"):
		return &engine.Error{Stage: engine.StageAsset, Message: msg}
	case strings.Contains(text, "error while decoding"),
		strings.Contains(text, "decoder"),
		strings.Contains(text, "could not find codec parameters"):
		return &engine.Error{Stage: engine.StageDecode, Message: msg}
	default:
		return &engine.Error{Stage: engine.StageEncode, Message: msg}
	}
}

// firstNonEmptyTail returns the last non-empty stderr line, which for ffmpeg
// almost always carries the actual failure reason.
func firstNonEmptyTail(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l != "" {
			return truncateRuneSafe(l, 300)
		}
	}
	return ""
}

// truncateRuneSafe caps s at max bytes without splitting a multibyte UTF-8
// rune at the cut point (Argus PR#4 pass 2 finding N2).
func truncateRuneSafe(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}
