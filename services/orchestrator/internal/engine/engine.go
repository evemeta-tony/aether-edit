// services/orchestrator/internal/engine/engine.go
//
// TranscodeEngine interface per ruling R3: this package covers codec work
// ONLY (probe, decode/scale/encode ladder execution) and contains NO FFmpeg
// types. Concrete adapters live in subpackages; adapter 1 is FFmpeg via
// os/exec argv arrays.
package engine

import (
	"context"
	"fmt"
)

// MediaInfo is the probed description of a source object.
type MediaInfo struct {
	Container         string  `json:"container"`
	DurationSeconds   float64 `json:"durationSeconds"`
	VideoCodec        string  `json:"videoCodec,omitempty"`
	AudioCodec        string  `json:"audioCodec,omitempty"`
	Width             int     `json:"width,omitempty"`
	Height            int     `json:"height,omitempty"`
	ChromaSubsampling string  `json:"chromaSubsampling,omitempty"`
	SourceBitrateBps  int64   `json:"sourceBitrateBps,omitempty"`
	VideoStreams      int     `json:"videoStreams"`
	AudioStreams      int     `json:"audioStreams"`
	SubtitleStreams   int     `json:"subtitleStreams"`
}

// RateControl describes the rate control for one output.
type RateControl struct {
	Mode           string // crf | vbr | cbr
	CRF            int
	BitrateKbps    int
	MaxBitrateKbps int
}

// OutputSpec describes one ladder rung to encode.
type OutputSpec struct {
	// RungName labels the output (for example "1080p").
	RungName string
	// Width and Height of the scaled output.
	Width  int
	Height int
	// Container: mp4 | mov | hls | dash | webm.
	Container string
	// VideoCodec: h264 | hevc | av1 (codec-neutral; adapters map to encoders).
	VideoCodec  string
	RateControl RateControl
	GOPLength   int
	// SpeedPreset: p1..p7 (p1 slowest and highest quality).
	SpeedPreset string
	// IncludeAudio is set by the caller from MediaInfo.AudioStreams.
	IncludeAudio bool
	// DestDir is the staging directory the adapter writes output files into.
	DestDir string
	// SourceDurationSeconds lets the adapter compute progress percent and eta.
	SourceDurationSeconds float64
}

// Progress is a live encode progress sample for one output.
type Progress struct {
	RungName       string
	FPS            float64
	SpeedX         float64
	OutTimeSeconds float64
	ProgressPct    float64
	ETASeconds     float64
}

// ErrorStage classifies where an engine failure happened, mapping onto the
// job error taxonomy (asset, decode, encode; anything else is internal).
type ErrorStage string

const (
	StageAsset  ErrorStage = "asset"
	StageDecode ErrorStage = "decode"
	StageEncode ErrorStage = "encode"
)

// Error is a typed engine failure.
type Error struct {
	Stage   ErrorStage
	Message string
}

func (e *Error) Error() string {
	return fmt.Sprintf("engine %s error: %s", e.Stage, e.Message)
}

// TranscodeEngine is the codec seam. Farm-of-one note (T-7): this interface
// executes on the single local node; a multi-node scheduler in front of many
// engines is explicitly deferred to a later work order.
type TranscodeEngine interface {
	// Probe inspects the media file at inputPath.
	Probe(ctx context.Context, inputPath string) (MediaInfo, error)
	// Transcode encodes one output rung, calling onProgress with live
	// samples until it returns. onProgress may be nil.
	Transcode(ctx context.Context, inputPath string, out OutputSpec, onProgress func(Progress)) error
}
