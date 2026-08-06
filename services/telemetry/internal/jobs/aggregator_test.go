// services/telemetry/internal/jobs/aggregator_test.go

package jobs

import (
	"bufio"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/evemeta-tony/aether-edit/services/telemetry/internal/hub"
)

type fixtureLine struct {
	Subject string          `json:"subject"`
	Payload json.RawMessage `json:"payload"`
}

func feedFixture(t *testing.T, a *Aggregator, name string) {
	t.Helper()
	f, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	line := 0
	for sc.Scan() {
		line++
		if len(sc.Bytes()) == 0 {
			continue
		}
		var fl fixtureLine
		if err := json.Unmarshal(sc.Bytes(), &fl); err != nil {
			t.Fatalf("fixture line %d: %v", line, err)
		}
		switch fl.Subject {
		case SubjectJobState:
			if err := a.HandleState(fl.Payload); err != nil {
				t.Fatalf("fixture line %d: %v", line, err)
			}
		case SubjectJobProgress:
			if err := a.HandleProgress(fl.Payload); err != nil {
				t.Fatalf("fixture line %d: %v", line, err)
			}
		default:
			t.Fatalf("fixture line %d: unknown subject %q", line, fl.Subject)
		}
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
}

func almost(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

func TestAggregateMathFromRecordedLifecycle(t *testing.T) {
	h := hub.New(64)
	sub := h.Subscribe(nil)
	a := New(h)
	feedFixture(t, a, "lifecycle.jsonl")

	agg := a.Snapshot()
	// After the fixture: job-a completed, job-c failed, job-b running,
	// job-d still queued.
	if agg.Queued != 1 || agg.InFlight != 1 || agg.Completed != 1 || agg.Failed != 1 {
		t.Fatalf("counts = %+v", agg)
	}
	// Only job-b is still running: fps 60.25, speedX 1.004.
	if !almost(agg.FarmFPS, 60.25) {
		t.Fatalf("FarmFPS = %v, want 60.25", agg.FarmFPS)
	}
	if !almost(agg.AggregateSpeedX, 1.004) {
		t.Fatalf("AggregateSpeedX = %v, want 1.004", agg.AggregateSpeedX)
	}

	// Mid-fixture aggregate math check: replay a fresh aggregator up to the
	// point where a, b, c are all running.
	h2 := hub.New(64)
	a2 := New(h2)
	feedFixture(t, a2, "lifecycle_midpoint.jsonl")
	agg2 := a2.Snapshot()
	if agg2.Queued != 1 || agg2.InFlight != 3 || agg2.Completed != 0 || agg2.Failed != 0 {
		t.Fatalf("midpoint counts = %+v", agg2)
	}
	if !almost(agg2.FarmFPS, 120.5+60.25+30) {
		t.Fatalf("midpoint FarmFPS = %v", agg2.FarmFPS)
	}
	if !almost(agg2.AggregateSpeedX, 2.01+1.004+0.5) {
		t.Fatalf("midpoint AggregateSpeedX = %v", agg2.AggregateSpeedX)
	}

	// Per-job passthrough: the subscriber saw job events including the last
	// progress passthrough for job-a before completion.
	evs, _ := sub.Drain()
	var sawProgress, sawCompleted bool
	for _, ev := range evs {
		if ev.Name != "job" {
			continue
		}
		var je map[string]any
		if err := json.Unmarshal(ev.Data, &je); err != nil {
			t.Fatal(err)
		}
		if je["jobId"] == "job-a" && je["state"] == StateRunning && almost(je["fps"].(float64), 110) &&
			almost(je["speedX"].(float64), 1.85) && almost(je["etaSeconds"].(float64), 120) &&
			almost(je["progressPct"].(float64), 80) {
			sawProgress = true
		}
		if je["jobId"] == "job-a" && je["state"] == StateCompleted {
			sawCompleted = true
		}
	}
	if !sawProgress {
		t.Fatal("per-job fps/speed/eta passthrough event not observed")
	}
	if !sawCompleted {
		t.Fatal("completed transition event not observed")
	}
}

func TestRejectsInvalidPayloads(t *testing.T) {
	h := hub.New(8)
	a := New(h)
	cases := [][2]string{
		{"state missing jobId", `{"workspaceId":"ws","state":"queued","at":"2026-08-06T10:00:00Z"}`},
		{"state unknown state", `{"jobId":"j","workspaceId":"ws","state":"paused","at":"2026-08-06T10:00:00Z"}`},
		{"state unknown field", `{"jobId":"j","workspaceId":"ws","state":"queued","at":"2026-08-06T10:00:00Z","extra":1}`},
		{"state not json", `not json`},
	}
	for _, c := range cases {
		if err := a.HandleState([]byte(c[1])); err == nil {
			t.Fatalf("%s: expected rejection", c[0])
		}
	}
	pcases := [][2]string{
		{"progress missing at", `{"jobId":"j","workspaceId":"ws","fps":1,"speedX":1,"etaSeconds":1,"progressPct":1}`},
		{"progress pct out of range", `{"jobId":"j","workspaceId":"ws","fps":1,"speedX":1,"etaSeconds":1,"progressPct":101,"at":"2026-08-06T10:00:00Z"}`},
		{"progress negative fps", `{"jobId":"j","workspaceId":"ws","fps":-1,"speedX":1,"etaSeconds":1,"progressPct":1,"at":"2026-08-06T10:00:00Z"}`},
	}
	for _, c := range pcases {
		if err := a.HandleProgress([]byte(c[1])); err == nil {
			t.Fatalf("%s: expected rejection", c[0])
		}
	}
	agg := a.Snapshot()
	if agg != (Aggregate{}) {
		t.Fatalf("rejected payloads mutated state: %+v", agg)
	}
}
