// services/telemetry/internal/jobs/aggregator.go

// Package jobs consumes the FT-3 job lifecycle and progress subjects and
// maintains batch aggregates plus per-job passthrough for the jobs stream.
package jobs

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/evemeta-tony/aether-edit/services/contracts"
	"github.com/evemeta-tony/aether-edit/services/telemetry/internal/hub"
)

// Subjects consumed by the aggregator (FT-4 API addendum to contract 4).
const (
	SubjectJobState    = "aether.ft.job.state.v1"
	SubjectJobProgress = "aether.ft.job.progress.v1"
)

// Job states carried on SubjectJobState and echoed on the jobs stream.
const (
	StateQueued    = "queued"
	StateRunning   = "running"
	StateCompleted = "completed"
	StateFailed    = "failed"
)

// stateEvent is the payload of SubjectJobState.
type stateEvent struct {
	JobID       string    `json:"jobId"`
	WorkspaceID string    `json:"workspaceId"`
	State       string    `json:"state"`
	At          time.Time `json:"at"`
}

// progressEvent is the payload of SubjectJobProgress.
type progressEvent struct {
	JobID       string    `json:"jobId"`
	WorkspaceID string    `json:"workspaceId"`
	FPS         float64   `json:"fps"`
	SpeedX      float64   `json:"speedX"`
	EtaSeconds  float64   `json:"etaSeconds"`
	ProgressPct float64   `json:"progressPct"`
	At          time.Time `json:"at"`
}

// Aggregate is the payload of the "aggregate" event on the jobs stream.
type Aggregate struct {
	Queued          int64   `json:"queued"`
	InFlight        int64   `json:"inFlight"`
	Completed       int64   `json:"completed"`
	Failed          int64   `json:"failed"`
	FarmFPS         float64 `json:"farmFps"`
	AggregateSpeedX float64 `json:"aggregateSpeedX"`
}

type jobState struct {
	state       string
	fps         float64
	speedX      float64
	etaSeconds  float64
	progressPct float64
}

// Aggregator validates incoming job events, maintains counts and farm-wide
// rates, and publishes per-job and aggregate events to the jobs hub.
type Aggregator struct {
	mu        sync.Mutex
	active    map[string]*jobState // queued or running jobs only
	queued    int64
	inFlight  int64
	completed int64
	failed    int64
	hub       *hub.Hub
}

// New creates an Aggregator publishing to h.
func New(h *hub.Hub) *Aggregator {
	return &Aggregator{active: make(map[string]*jobState), hub: h}
}

// HandleState consumes one SubjectJobState message. Invalid payloads are
// rejected with an error and cause no state change.
func (a *Aggregator) HandleState(data []byte) error {
	var ev stateEvent
	if err := strictUnmarshal(data, &ev); err != nil {
		return fmt.Errorf("job state event: %w", err)
	}
	if ev.JobID == "" || ev.WorkspaceID == "" {
		return fmt.Errorf("job state event: jobId and workspaceId are required")
	}
	if ev.At.IsZero() {
		return fmt.Errorf("job state event: at is required")
	}
	switch ev.State {
	case StateQueued, StateRunning, StateCompleted, StateFailed:
	default:
		return fmt.Errorf("job state event: unknown state %q", ev.State)
	}

	a.mu.Lock()
	js, known := a.active[ev.JobID]
	switch ev.State {
	case StateQueued:
		if !known {
			a.active[ev.JobID] = &jobState{state: StateQueued}
			a.queued++
		}
	case StateRunning:
		if known && js.state == StateQueued {
			a.queued--
			a.inFlight++
			js.state = StateRunning
		} else if !known {
			a.active[ev.JobID] = &jobState{state: StateRunning}
			a.inFlight++
		}
	case StateCompleted, StateFailed:
		if known {
			if js.state == StateQueued {
				a.queued--
			} else {
				a.inFlight--
			}
			delete(a.active, ev.JobID)
		}
		if ev.State == StateCompleted {
			a.completed++
		} else {
			a.failed++
		}
	}
	out := a.jobEventLocked(ev.JobID, ev.State)
	agg := a.aggregateLocked()
	a.mu.Unlock()

	a.publish(out, agg)
	return nil
}

// HandleProgress consumes one SubjectJobProgress message. A progress event
// for an unknown job implies the job is running. Invalid payloads are
// rejected with an error and cause no state change.
func (a *Aggregator) HandleProgress(data []byte) error {
	var ev progressEvent
	if err := strictUnmarshal(data, &ev); err != nil {
		return fmt.Errorf("job progress event: %w", err)
	}
	if ev.JobID == "" || ev.WorkspaceID == "" {
		return fmt.Errorf("job progress event: jobId and workspaceId are required")
	}
	if ev.At.IsZero() {
		return fmt.Errorf("job progress event: at is required")
	}
	if ev.FPS < 0 || ev.SpeedX < 0 || ev.EtaSeconds < 0 || ev.ProgressPct < 0 || ev.ProgressPct > 100 {
		return fmt.Errorf("job progress event: values out of range")
	}

	a.mu.Lock()
	js, known := a.active[ev.JobID]
	if !known {
		js = &jobState{state: StateRunning}
		a.active[ev.JobID] = js
		a.inFlight++
	} else if js.state == StateQueued {
		a.queued--
		a.inFlight++
		js.state = StateRunning
	}
	js.fps = ev.FPS
	js.speedX = ev.SpeedX
	js.etaSeconds = ev.EtaSeconds
	js.progressPct = ev.ProgressPct
	out := a.jobEventLocked(ev.JobID, js.state)
	agg := a.aggregateLocked()
	a.mu.Unlock()

	a.publish(out, agg)
	return nil
}

// Snapshot returns the current aggregate.
func (a *Aggregator) Snapshot() Aggregate {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.aggregateLocked()
}

func (a *Aggregator) jobEventLocked(jobID, state string) contracts.JobStreamEvent {
	out := contracts.JobStreamEvent{JobID: jobID, State: state}
	if js, ok := a.active[jobID]; ok {
		out.FPS = js.fps
		out.SpeedX = js.speedX
		out.EtaSeconds = js.etaSeconds
		out.ProgressPct = js.progressPct
	} else if state == StateCompleted {
		out.ProgressPct = 100
	}
	return out
}

func (a *Aggregator) aggregateLocked() Aggregate {
	agg := Aggregate{
		Queued:    a.queued,
		InFlight:  a.inFlight,
		Completed: a.completed,
		Failed:    a.failed,
	}
	for _, js := range a.active {
		if js.state == StateRunning {
			agg.FarmFPS += js.fps
			agg.AggregateSpeedX += js.speedX
		}
	}
	return agg
}

func (a *Aggregator) publish(job contracts.JobStreamEvent, agg Aggregate) {
	if jb, err := json.Marshal(job); err == nil {
		a.hub.Publish(hub.Event{Name: "job", Data: jb})
	}
	if ab, err := json.Marshal(agg); err == nil {
		a.hub.PublishSticky(hub.Event{Name: "aggregate", Data: ab})
	}
}

// strictUnmarshal decodes JSON rejecting unknown fields and trailing data.
func strictUnmarshal(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("trailing data after JSON value")
	}
	return nil
}
