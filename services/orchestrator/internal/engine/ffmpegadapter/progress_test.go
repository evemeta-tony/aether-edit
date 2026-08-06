// services/orchestrator/internal/engine/ffmpegadapter/progress_test.go
package ffmpegadapter

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScanProgressFixture(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("testdata", "progress_h264.txt"))
	if err != nil {
		t.Fatal(err)
	}
	var samples []ProgressSample
	if err := ScanProgress(strings.NewReader(string(data)), func(s ProgressSample) {
		samples = append(samples, s)
	}); err != nil {
		t.Fatal(err)
	}
	if len(samples) != 4 {
		t.Fatalf("got %d samples, want 4", len(samples))
	}
	if samples[1].Frame != 240 || samples[1].FPS != 118.42 || samples[1].SpeedX != 4.94 {
		t.Errorf("sample 1 wrong: %+v", samples[1])
	}
	if samples[1].OutTimeSeconds != 10.01 {
		t.Errorf("sample 1 out_time = %f, want 10.01", samples[1].OutTimeSeconds)
	}
	if !samples[3].End {
		t.Error("final sample must carry End")
	}
	for _, s := range samples[:3] {
		if s.End {
			t.Errorf("non-final sample marked End: %+v", s)
		}
	}
}

func TestFeedIgnoresMalformedLines(t *testing.T) {
	var p ProgressParser
	for _, line := range []string{"", "garbage", "fps=notanumber", "out_time_us=-5"} {
		if _, done := p.Feed(line); done {
			t.Errorf("line %q must not complete a block", line)
		}
	}
	if _, done := p.Feed("frame=10"); done {
		t.Error("frame line must not complete a block")
	}
	s, done := p.Feed("progress=continue")
	if !done || s.Frame != 10 {
		t.Errorf("block not completed correctly: %+v done=%v", s, done)
	}
}

func TestOutTimeUSWinsOverOutTimeMS(t *testing.T) {
	// out_time_us is authoritative. Feed divergent values in both orders and
	// require the us value to win; out_time_ms alone is a microsecond
	// fallback (unit to be verified against the AM-5 ffmpeg build).
	var p ProgressParser
	p.Feed("out_time_us=2000000")
	p.Feed("out_time_ms=9000000")
	s, done := p.Feed("progress=continue")
	if !done || s.OutTimeSeconds != 2.0 {
		t.Errorf("us then ms: out_time = %f, want 2.0", s.OutTimeSeconds)
	}
	p.Feed("out_time_ms=9000000")
	p.Feed("out_time_us=2000000")
	s, done = p.Feed("progress=continue")
	if !done || s.OutTimeSeconds != 2.0 {
		t.Errorf("ms then us: out_time = %f, want 2.0", s.OutTimeSeconds)
	}
	p.Feed("out_time_ms=3000000")
	s, done = p.Feed("progress=continue")
	if !done || s.OutTimeSeconds != 3.0 {
		t.Errorf("ms only fallback: out_time = %f, want 3.0", s.OutTimeSeconds)
	}
}

func TestDeriveProgress(t *testing.T) {
	pct, eta := DeriveProgress(ProgressSample{OutTimeSeconds: 15, SpeedX: 3}, 30)
	if pct != 50 {
		t.Errorf("pct = %f, want 50", pct)
	}
	if eta != 5 {
		t.Errorf("eta = %f, want 5", eta)
	}
	pct, eta = DeriveProgress(ProgressSample{OutTimeSeconds: 15, SpeedX: 3}, 0)
	if pct != 0 || eta != 0 {
		t.Errorf("unknown duration must yield 0,0; got %f,%f", pct, eta)
	}
	pct, _ = DeriveProgress(ProgressSample{OutTimeSeconds: 45, SpeedX: 1}, 30)
	if pct != 100 {
		t.Errorf("pct must clamp at 100, got %f", pct)
	}
}
