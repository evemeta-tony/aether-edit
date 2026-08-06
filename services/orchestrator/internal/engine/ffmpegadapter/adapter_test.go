// services/orchestrator/internal/engine/ffmpegadapter/adapter_test.go
//
// Adapter tests run against fixture ffmpeg and ffprobe outputs with a fake
// CommandRunner injected at the exec seam (test double in test code only;
// the OSRunner execer ships in production). Real-encoder behavior (NVENC
// sessions, playable outputs) needs the OVH box and is out of local scope.
package ffmpegadapter

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/evemeta-tony/aether-edit/services/orchestrator/internal/engine"
)

// fakeRunner is a CommandRunner test double.
type fakeRunner struct {
	outputStdout []byte
	outputStderr []byte
	outputErr    error
	startProc    *fakeProcess
	startErr     error
	lastName     string
	lastArgs     []string
}

func (f *fakeRunner) Output(_ context.Context, name string, args []string) ([]byte, []byte, error) {
	f.lastName = name
	f.lastArgs = args
	return f.outputStdout, f.outputStderr, f.outputErr
}

func (f *fakeRunner) Start(_ context.Context, name string, args []string) (Process, error) {
	f.lastName = name
	f.lastArgs = args
	if f.startErr != nil {
		return nil, f.startErr
	}
	return f.startProc, nil
}

// fakeProcess is a Process test double.
type fakeProcess struct {
	stdout  io.Reader
	stderr  []byte
	waitErr error
	killed  bool
}

func (p *fakeProcess) Stdout() io.Reader     { return p.stdout }
func (p *fakeProcess) Wait() ([]byte, error) { return p.stderr, p.waitErr }
func (p *fakeProcess) Kill() error           { p.killed = true; return nil }

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return data
}

func newTestAdapter(t *testing.T, r *fakeRunner) *Adapter {
	t.Helper()
	gate := &fakeRunner{outputStdout: fixture(t, "buildconf_clean.txt")}
	a, err := New(context.Background(), Config{FFmpegPath: "/opt/am5/ffmpeg", FFprobePath: "/opt/am5/ffprobe"}, gate)
	if err != nil {
		t.Fatalf("adapter construct: %v", err)
	}
	a.runner = r
	return a
}

func TestBuildGateRefusesGPL(t *testing.T) {
	for _, name := range []string{"buildconf_gpl.txt", "buildconf_nonfree.txt"} {
		r := &fakeRunner{outputStdout: fixture(t, name)}
		_, err := New(context.Background(), Config{FFmpegPath: "ffmpeg", FFprobePath: "ffprobe"}, r)
		if !errors.Is(err, ErrForbiddenBuild) {
			t.Errorf("fixture %s: got err %v, want ErrForbiddenBuild", name, err)
		}
	}
}

func TestBuildGateAcceptsCleanBuild(t *testing.T) {
	r := &fakeRunner{outputStdout: fixture(t, "buildconf_clean.txt")}
	a, err := New(context.Background(), Config{FFmpegPath: "ffmpeg", FFprobePath: "ffprobe"}, r)
	if err != nil {
		t.Fatalf("clean build refused: %v", err)
	}
	if a == nil {
		t.Fatal("nil adapter")
	}
	if r.lastName != "ffmpeg" || !slices.Equal(r.lastArgs, []string{"-hide_banner", "-buildconf"}) {
		t.Errorf("gate argv = %s %v", r.lastName, r.lastArgs)
	}
}

func TestBuildGateFailsWhenBinaryMissing(t *testing.T) {
	r := &fakeRunner{outputErr: fmt.Errorf("exec: not found")}
	if _, err := New(context.Background(), Config{FFmpegPath: "ffmpeg", FFprobePath: "ffprobe"}, r); err == nil {
		t.Fatal("expected error when buildconf cannot run")
	}
}

func TestCheckBuildconfTokenExactness(t *testing.T) {
	if err := CheckBuildconf("--enable-libgpl-tools"); err != nil {
		t.Errorf("non-matching token misclassified: %v", err)
	}
	if err := CheckBuildconf("prefix --enable-gpl suffix"); !errors.Is(err, ErrForbiddenBuild) {
		t.Error("inline --enable-gpl not caught")
	}
}

func TestProbeParsesFfprobeJSON(t *testing.T) {
	r := &fakeRunner{outputStdout: fixture(t, "ffprobe_h264.json")}
	a := newTestAdapter(t, r)
	mi, err := a.Probe(context.Background(), "/store/assets/ws/sha256/input")
	if err != nil {
		t.Fatalf("probe: %v", err)
	}
	if r.lastName != "/opt/am5/ffprobe" {
		t.Errorf("probe binary = %s", r.lastName)
	}
	if mi.VideoCodec != "h264" || mi.Width != 1920 || mi.Height != 1080 {
		t.Errorf("video fields wrong: %+v", mi)
	}
	if mi.ChromaSubsampling != "4:2:0" {
		t.Errorf("chroma = %s, want 4:2:0", mi.ChromaSubsampling)
	}
	if mi.DurationSeconds != 30 || mi.SourceBitrateBps != 5000000 {
		t.Errorf("format fields wrong: %+v", mi)
	}
	if mi.VideoStreams != 1 || mi.AudioStreams != 1 || mi.SubtitleStreams != 1 {
		t.Errorf("stream inventory wrong: %+v", mi)
	}
	if mi.AudioCodec != "aac" {
		t.Errorf("audio codec = %s", mi.AudioCodec)
	}
	if !strings.Contains(mi.Container, "mp4") {
		t.Errorf("container = %s", mi.Container)
	}
}

func TestProbeRejectsNoVideo(t *testing.T) {
	r := &fakeRunner{outputStdout: []byte(`{"streams":[{"codec_type":"audio","codec_name":"aac"}],"format":{"format_name":"mp3","duration":"10.0"}}`)}
	a := newTestAdapter(t, r)
	_, err := a.Probe(context.Background(), "/in")
	var engErr *engine.Error
	if !errors.As(err, &engErr) || engErr.Stage != engine.StageAsset {
		t.Fatalf("got %v, want asset-stage engine error", err)
	}
}

func TestTranscodeStreamsProgressFromPipe(t *testing.T) {
	proc := &fakeProcess{stdout: strings.NewReader(string(fixture(t, "progress_h264.txt")))}
	r := &fakeRunner{startProc: proc}
	a := newTestAdapter(t, r)
	var samples []engine.Progress
	err := a.Transcode(context.Background(), "/store/in.mp4", engine.OutputSpec{
		RungName: "1080p", Width: 1920, Height: 1080,
		Container: "mp4", VideoCodec: "h264",
		RateControl:           engine.RateControl{Mode: "crf", CRF: 23},
		GOPLength:             48,
		SpeedPreset:           "p5",
		IncludeAudio:          true,
		DestDir:               t.TempDir(),
		SourceDurationSeconds: 30,
	}, func(p engine.Progress) { samples = append(samples, p) })
	if err != nil {
		t.Fatalf("transcode: %v", err)
	}
	if len(samples) != 4 {
		t.Fatalf("got %d samples, want 4", len(samples))
	}
	first, last := samples[0], samples[3]
	if first.FPS != 0 || first.SpeedX != 3.85 {
		t.Errorf("first sample wrong: %+v", first)
	}
	if abs(first.ProgressPct-2.002/30*100) > 0.01 {
		t.Errorf("first pct = %f", first.ProgressPct)
	}
	if last.ProgressPct != 100 {
		t.Errorf("final pct = %f, want 100 on progress=end", last.ProgressPct)
	}
	if last.FPS != 121.30 || last.SpeedX != 5.06 {
		t.Errorf("last sample wrong: %+v", last)
	}
	// The progress destination is the pipe, never argv.
	if !containsPair(r.lastArgs, "-progress", "pipe:1") {
		t.Errorf("argv missing -progress pipe:1: %v", r.lastArgs)
	}
}

func TestTranscodeClassifiesFailures(t *testing.T) {
	cases := []struct {
		stderr string
		want   engine.ErrorStage
	}{
		{"in.mp4: No such file or directory", engine.StageAsset},
		{"in.mp4: Invalid data found when processing input", engine.StageAsset},
		{"Error while decoding stream #0:0: generic error", engine.StageDecode},
		{"[h264_nvenc] OpenEncodeSessionEx failed: out of memory", engine.StageEncode},
		{"something unrecognized exploded", engine.StageEncode},
	}
	for _, tc := range cases {
		proc := &fakeProcess{
			stdout:  strings.NewReader(""),
			stderr:  []byte(tc.stderr),
			waitErr: fmt.Errorf("exit status 1"),
		}
		r := &fakeRunner{startProc: proc}
		a := newTestAdapter(t, r)
		err := a.Transcode(context.Background(), "/in", engine.OutputSpec{
			RungName: "r", Width: 640, Height: 360, Container: "mp4", VideoCodec: "h264",
			RateControl: engine.RateControl{Mode: "crf", CRF: 23},
			GOPLength:   48, SpeedPreset: "p5", DestDir: t.TempDir(),
		}, nil)
		var engErr *engine.Error
		if !errors.As(err, &engErr) || engErr.Stage != tc.want {
			t.Errorf("stderr %q: got %v, want stage %s", tc.stderr, err, tc.want)
		}
	}
}

func abs(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func containsPair(args []string, k, v string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == k && args[i+1] == v {
			return true
		}
	}
	return false
}
