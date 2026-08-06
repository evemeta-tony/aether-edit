// services/orchestrator/internal/jobs/preset_test.go
package jobs

import (
	"strings"
	"testing"
)

func validPreset() Preset {
	return Preset{
		Name:        "web-h264",
		Container:   ContainerMP4,
		VideoCodec:  CodecH264,
		RateControl: RateControlCRF,
		CRF:         23,
		GOPLength:   48,
		SpeedPreset: "p5",
		Ladder: []Rung{
			{Name: "1080p", Width: 1920, Height: 1080},
			{Name: "720p", Width: 1280, Height: 720},
		},
	}
}

func TestPresetValidateAccepts(t *testing.T) {
	p := validPreset()
	if err := p.Validate(); err != nil {
		t.Fatalf("valid preset rejected: %v", err)
	}
	vbr := validPreset()
	vbr.RateControl = RateControlVBR
	vbr.CRF = 0
	vbr.BitrateKbps = 4000
	vbr.MaxBitrateKbps = 6000
	if err := vbr.Validate(); err != nil {
		t.Fatalf("valid vbr preset rejected: %v", err)
	}
	cbr := validPreset()
	cbr.RateControl = RateControlCBR
	cbr.CRF = 0
	cbr.BitrateKbps = 4000
	if err := cbr.Validate(); err != nil {
		t.Fatalf("valid cbr preset rejected: %v", err)
	}
}

func TestPresetValidateRejects(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Preset)
		wantSub string
	}{
		{"bad container", func(p *Preset) { p.Container = "avi" }, "container"},
		{"webm needs av1", func(p *Preset) { p.Container = ContainerWebM }, "webm"},
		{"bad codec", func(p *Preset) { p.VideoCodec = "mpeg2" }, "videoCodec"},
		{"bad rate control", func(p *Preset) { p.RateControl = "abr" }, "rateControl"},
		{"crf out of range", func(p *Preset) { p.CRF = 99 }, "crf"},
		{"crf with bitrate", func(p *Preset) { p.BitrateKbps = 4000 }, "bitrateKbps"},
		{"vbr without bitrate", func(p *Preset) { p.RateControl = RateControlVBR; p.CRF = 0 }, "bitrateKbps"},
		{"vbr maxrate below rate", func(p *Preset) {
			p.RateControl = RateControlVBR
			p.CRF = 0
			p.BitrateKbps = 4000
			p.MaxBitrateKbps = 2000
		}, "maxBitrateKbps"},
		{"gop zero", func(p *Preset) { p.GOPLength = 0 }, "gopLength"},
		{"bad speed preset", func(p *Preset) { p.SpeedPreset = "fast" }, "speedPreset"},
		{"empty ladder", func(p *Preset) { p.Ladder = nil }, "ladder"},
		{"odd dimensions", func(p *Preset) { p.Ladder[0].Width = 1921 }, "even"},
		{"duplicate rung names", func(p *Preset) { p.Ladder[1].Name = "1080p" }, "unique"},
		{"empty name", func(p *Preset) { p.Name = "" }, "name"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := validPreset()
			tc.mutate(&p)
			err := p.Validate()
			if err == nil {
				t.Fatal("expected validation error, got nil")
			}
			if !strings.Contains(err.Error(), tc.wantSub) {
				t.Fatalf("error %q does not mention %q", err, tc.wantSub)
			}
		})
	}
}
