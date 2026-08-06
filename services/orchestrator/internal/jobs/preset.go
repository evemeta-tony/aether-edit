// services/orchestrator/internal/jobs/preset.go
//
// Preset domain model. Preset edits apply to jobs that START after the edit:
// the scheduler snapshots the preset row at claim time, so a running job is
// never re-parameterized mid-encode. That semantic is documented in the
// service README and covered by tests.
package jobs

import (
	"fmt"
	"regexp"
	"time"
)

// rungNamePattern restricts rung names to a filesystem- and object-key-safe
// character set. Rung names flow into ffmpeg output argv, staging file paths
// and object-store key prefixes, so they must start with an alphanumeric
// (which also excludes "." and "..") and contain only [A-Za-z0-9._-]. The
// object store validates keys again on the way out; this gate runs first so
// a hostile name never reaches the filesystem (Argus PR#4 finding 11).
var rungNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,31}$`)

// Container is the output container format.
type Container string

const (
	ContainerMP4  Container = "mp4"
	ContainerMOV  Container = "mov"
	ContainerHLS  Container = "hls"
	ContainerDASH Container = "dash"
	ContainerWebM Container = "webm"
)

// ValidContainer reports whether c is a supported container.
func ValidContainer(c Container) bool {
	switch c {
	case ContainerMP4, ContainerMOV, ContainerHLS, ContainerDASH, ContainerWebM:
		return true
	}
	return false
}

// RateControlMode selects the rate control strategy.
type RateControlMode string

const (
	RateControlCRF RateControlMode = "crf"
	RateControlVBR RateControlMode = "vbr"
	RateControlCBR RateControlMode = "cbr"
)

// ValidRateControlMode reports whether m is supported.
func ValidRateControlMode(m RateControlMode) bool {
	switch m {
	case RateControlCRF, RateControlVBR, RateControlCBR:
		return true
	}
	return false
}

// VideoCodec is a codec-neutral name; the engine adapter maps it to a
// concrete encoder (per ruling R3 no FFmpeg types leak out of the adapter).
type VideoCodec string

const (
	CodecH264 VideoCodec = "h264"
	CodecHEVC VideoCodec = "hevc"
	CodecAV1  VideoCodec = "av1"
)

// ValidVideoCodec reports whether v is supported.
func ValidVideoCodec(v VideoCodec) bool {
	switch v {
	case CodecH264, CodecHEVC, CodecAV1:
		return true
	}
	return false
}

// SpeedPreset is the encoder speed preset, console model p1 (slowest, best
// quality) through p7 (fastest).
type SpeedPreset string

// ValidSpeedPreset reports whether s is p1..p7.
func ValidSpeedPreset(s SpeedPreset) bool {
	switch s {
	case "p1", "p2", "p3", "p4", "p5", "p6", "p7":
		return true
	}
	return false
}

// Rung is one ladder output of a preset.
type Rung struct {
	Name   string `json:"name"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}

// Preset is a stored transcode preset.
type Preset struct {
	ID          string          `json:"id"`
	WorkspaceID string          `json:"workspaceId"`
	Name        string          `json:"name"`
	Container   Container       `json:"container"`
	VideoCodec  VideoCodec      `json:"videoCodec"`
	RateControl RateControlMode `json:"rateControl"`
	// CRF is set when RateControl is crf (constant quality value).
	CRF int `json:"crf,omitempty"`
	// BitrateKbps is set when RateControl is vbr or cbr.
	BitrateKbps int `json:"bitrateKbps,omitempty"`
	// MaxBitrateKbps optionally caps vbr.
	MaxBitrateKbps int         `json:"maxBitrateKbps,omitempty"`
	GOPLength      int         `json:"gopLength"`
	SpeedPreset    SpeedPreset `json:"speedPreset"`
	Ladder         []Rung      `json:"ladder"`
	CreatedAt      time.Time   `json:"createdAt"`
	UpdatedAt      time.Time   `json:"updatedAt"`
}

// Validate enforces cross-field preset invariants. It rejects, never coerces.
func (p *Preset) Validate() error {
	if p.Name == "" || len(p.Name) > 128 {
		return fmt.Errorf("preset name must be 1..128 characters")
	}
	if !ValidContainer(p.Container) {
		return fmt.Errorf("container must be one of mp4, mov, hls, dash, webm")
	}
	if !ValidVideoCodec(p.VideoCodec) {
		return fmt.Errorf("videoCodec must be one of h264, hevc, av1")
	}
	if p.Container == ContainerWebM && p.VideoCodec != CodecAV1 {
		return fmt.Errorf("webm container requires videoCodec av1")
	}
	if !ValidRateControlMode(p.RateControl) {
		return fmt.Errorf("rateControl must be one of crf, vbr, cbr")
	}
	switch p.RateControl {
	case RateControlCRF:
		if p.CRF < 0 || p.CRF > 51 {
			return fmt.Errorf("crf must be 0..51 for rateControl crf")
		}
		if p.BitrateKbps != 0 || p.MaxBitrateKbps != 0 {
			return fmt.Errorf("bitrateKbps and maxBitrateKbps must be unset for rateControl crf")
		}
	case RateControlVBR:
		if p.BitrateKbps < 1 {
			return fmt.Errorf("bitrateKbps must be >= 1 for rateControl vbr")
		}
		if p.MaxBitrateKbps != 0 && p.MaxBitrateKbps < p.BitrateKbps {
			return fmt.Errorf("maxBitrateKbps must be >= bitrateKbps")
		}
		if p.CRF != 0 {
			return fmt.Errorf("crf must be unset for rateControl vbr")
		}
	case RateControlCBR:
		if p.BitrateKbps < 1 {
			return fmt.Errorf("bitrateKbps must be >= 1 for rateControl cbr")
		}
		if p.CRF != 0 || p.MaxBitrateKbps != 0 {
			return fmt.Errorf("crf and maxBitrateKbps must be unset for rateControl cbr")
		}
	}
	if p.GOPLength < 1 || p.GOPLength > 600 {
		return fmt.Errorf("gopLength must be 1..600 frames")
	}
	if !ValidSpeedPreset(p.SpeedPreset) {
		return fmt.Errorf("speedPreset must be p1..p7")
	}
	if len(p.Ladder) == 0 || len(p.Ladder) > 8 {
		return fmt.Errorf("ladder must have 1..8 rungs")
	}
	seen := map[string]bool{}
	for i, r := range p.Ladder {
		if !rungNamePattern.MatchString(r.Name) {
			return fmt.Errorf("ladder[%d].name must be 1..32 characters, start with a letter or digit, and contain only letters, digits, dot, underscore, hyphen", i)
		}
		if seen[r.Name] {
			return fmt.Errorf("ladder rung names must be unique: %q", r.Name)
		}
		seen[r.Name] = true
		if r.Width < 16 || r.Width > 8192 || r.Height < 16 || r.Height > 8192 {
			return fmt.Errorf("ladder[%d] dimensions must be 16..8192", i)
		}
		if r.Width%2 != 0 || r.Height%2 != 0 {
			return fmt.Errorf("ladder[%d] dimensions must be even", i)
		}
	}
	return nil
}
