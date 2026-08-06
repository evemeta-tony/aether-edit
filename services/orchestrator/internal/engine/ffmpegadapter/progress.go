// services/orchestrator/internal/engine/ffmpegadapter/progress.go
//
// Parser for the ffmpeg -progress key=value stream. Progress travels over a
// pipe (stdout, -progress pipe:1), never argv (S3). Blocks are terminated by
// a progress=continue or progress=end line.
package ffmpegadapter

import (
	"bufio"
	"io"
	"strconv"
	"strings"
)

// ProgressSample is one parsed -progress block.
type ProgressSample struct {
	Frame          int64
	FPS            float64
	OutTimeSeconds float64
	SpeedX         float64
	End            bool
}

// ProgressParser accumulates key=value lines into samples.
type ProgressParser struct {
	cur      ProgressSample
	haveData bool
}

// Feed consumes one line. It returns a completed sample and true when the
// line terminates a block (progress=continue or progress=end).
func (p *ProgressParser) Feed(line string) (ProgressSample, bool) {
	line = strings.TrimSpace(line)
	key, val, ok := strings.Cut(line, "=")
	if !ok {
		return ProgressSample{}, false
	}
	val = strings.TrimSpace(val)
	switch key {
	case "frame":
		if n, err := strconv.ParseInt(val, 10, 64); err == nil {
			p.cur.Frame = n
			p.haveData = true
		}
	case "fps":
		if f, err := strconv.ParseFloat(val, 64); err == nil {
			p.cur.FPS = f
			p.haveData = true
		}
	case "out_time_us":
		if n, err := strconv.ParseInt(val, 10, 64); err == nil && n >= 0 {
			p.cur.OutTimeSeconds = float64(n) / 1e6
			p.haveData = true
		}
	case "out_time_ms":
		// ffmpeg emits out_time_ms in microseconds as well (long-standing
		// upstream quirk); out_time_us wins when both are present because it
		// is processed per-block in stream order and carries the same value.
		if n, err := strconv.ParseInt(val, 10, 64); err == nil && n >= 0 {
			p.cur.OutTimeSeconds = float64(n) / 1e6
			p.haveData = true
		}
	case "speed":
		v := strings.TrimSuffix(val, "x")
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			p.cur.SpeedX = f
			p.haveData = true
		}
	case "progress":
		s := p.cur
		s.End = val == "end"
		p.cur = ProgressSample{}
		done := p.haveData || s.End
		p.haveData = false
		return s, done
	}
	return ProgressSample{}, false
}

// ScanProgress reads the whole progress stream from r, invoking emit for
// each completed block.
func ScanProgress(r io.Reader, emit func(ProgressSample)) error {
	var p ProgressParser
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if s, ok := p.Feed(sc.Text()); ok {
			emit(s)
		}
	}
	return sc.Err()
}

// DeriveProgress converts a raw sample into engine progress numbers given
// the source duration. Duration <= 0 yields pct 0 and eta 0 (unknown).
func DeriveProgress(s ProgressSample, durationSeconds float64) (pct, eta float64) {
	if durationSeconds <= 0 {
		return 0, 0
	}
	pct = s.OutTimeSeconds / durationSeconds * 100
	if pct > 100 {
		pct = 100
	}
	if pct < 0 {
		pct = 0
	}
	if s.End {
		pct = 100
	}
	if s.SpeedX > 0 {
		remain := durationSeconds - s.OutTimeSeconds
		if remain < 0 {
			remain = 0
		}
		eta = remain / s.SpeedX
	}
	return pct, eta
}
