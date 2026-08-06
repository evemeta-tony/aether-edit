// services/orchestrator/internal/engine/ffmpegadapter/args_test.go
package ffmpegadapter

import (
	"strings"
	"testing"

	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/engine"
)

func baseSpec(dir string) engine.OutputSpec {
	return engine.OutputSpec{
		RungName: "720p", Width: 1280, Height: 720,
		Container: "mp4", VideoCodec: "h264",
		RateControl:  engine.RateControl{Mode: "crf", CRF: 23},
		GOPLength:    48,
		SpeedPreset:  "p5",
		IncludeAudio: true,
		DestDir:      dir,
	}
}

func TestArgsCRF(t *testing.T) {
	args, artifact, err := buildTranscodeArgs("/in.mp4", baseSpec("/stage"))
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]string{
		{"-i", "/in.mp4"},
		{"-c:v", "h264_nvenc"},
		{"-preset", "p5"},
		{"-g", "48"},
		{"-rc", "vbr"},
		{"-cq", "23"},
		{"-vf", "scale=1280:720"},
		{"-c:a", "aac"},
		{"-progress", "pipe:1"},
	} {
		if !containsPair(args, pair[0], pair[1]) {
			t.Errorf("argv missing %v: %v", pair, args)
		}
	}
	if artifact != "/stage/720p.mp4" {
		t.Errorf("artifact = %s", artifact)
	}
}

func TestArgsVBRAndCBR(t *testing.T) {
	vbr := baseSpec("/stage")
	vbr.RateControl = engine.RateControl{Mode: "vbr", BitrateKbps: 4000, MaxBitrateKbps: 6000}
	args, _, err := buildTranscodeArgs("/in.mp4", vbr)
	if err != nil {
		t.Fatal(err)
	}
	if !containsPair(args, "-b:v", "4000k") || !containsPair(args, "-maxrate", "6000k") {
		t.Errorf("vbr argv wrong: %v", args)
	}

	cbr := baseSpec("/stage")
	cbr.RateControl = engine.RateControl{Mode: "cbr", BitrateKbps: 5000}
	args, _, err = buildTranscodeArgs("/in.mp4", cbr)
	if err != nil {
		t.Fatal(err)
	}
	if !containsPair(args, "-rc", "cbr") || !containsPair(args, "-minrate", "5000k") {
		t.Errorf("cbr argv wrong: %v", args)
	}
}

func TestArgsCodecMapping(t *testing.T) {
	hevc := baseSpec("/s")
	hevc.VideoCodec = "hevc"
	args, _, err := buildTranscodeArgs("/in", hevc)
	if err != nil {
		t.Fatal(err)
	}
	if !containsPair(args, "-c:v", "hevc_nvenc") {
		t.Errorf("hevc mapping wrong: %v", args)
	}
	if _, _, err := buildTranscodeArgs("/in", engine.OutputSpec{VideoCodec: "vp8", Container: "mp4", RateControl: engine.RateControl{Mode: "crf"}}); err == nil {
		t.Error("unknown codec accepted")
	}
}

func TestArgsContainers(t *testing.T) {
	hls := baseSpec("/s")
	hls.Container = "hls"
	args, artifact, err := buildTranscodeArgs("/in", hls)
	if err != nil {
		t.Fatal(err)
	}
	if artifact != "/s/720p.m3u8" || !containsPair(args, "-f", "hls") {
		t.Errorf("hls argv wrong: artifact=%s args=%v", artifact, args)
	}

	dash := baseSpec("/s")
	dash.Container = "dash"
	_, artifact, err = buildTranscodeArgs("/in", dash)
	if err != nil {
		t.Fatal(err)
	}
	if artifact != "/s/720p.mpd" {
		t.Errorf("dash artifact = %s", artifact)
	}

	webm := baseSpec("/s")
	webm.Container = "webm"
	webm.VideoCodec = "av1"
	args, artifact, err = buildTranscodeArgs("/in", webm)
	if err != nil {
		t.Fatal(err)
	}
	if artifact != "/s/720p.webm" || !containsPair(args, "-c:a", "libopus") {
		t.Errorf("webm argv wrong: artifact=%s args=%v", artifact, args)
	}
}

func TestArgsNoAudio(t *testing.T) {
	spec := baseSpec("/s")
	spec.IncludeAudio = false
	args, _, err := buildTranscodeArgs("/in", spec)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, a := range args {
		if a == "-an" {
			found = true
		}
		if a == "0:a:0" || a == "-c:a" {
			t.Errorf("audio argv present without audio: %v", args)
		}
	}
	if !found {
		t.Errorf("-an missing: %v", args)
	}
}

func TestArgsNeverInvokeShell(t *testing.T) {
	// Defensive: argv content must never contain shell metacharacter
	// concatenations of the input path; the path is a single argv element.
	args, _, err := buildTranscodeArgs("/store/assets/ws/sha256/aa bb", baseSpec("/s"))
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(args, "\x00")
	if !strings.Contains(joined, "\x00/store/assets/ws/sha256/aa bb\x00") {
		t.Errorf("input path not preserved as one argv element: %v", args)
	}
}
