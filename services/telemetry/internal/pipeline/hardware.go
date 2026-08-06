// services/telemetry/internal/pipeline/hardware.go

// Package pipeline runs the 1 Hz sampler-to-stream loop for the hardware
// telemetry stream.
package pipeline

import (
	"context"
	"encoding/json"
	"log/slog"
	"math"
	"time"

	"github.com/evemeta-tony/aether-edit/services/contracts"
	"github.com/evemeta-tony/aether-edit/services/telemetry/internal/hub"
	"github.com/evemeta-tony/aether-edit/services/telemetry/internal/sampler"
)

// hardwareStatus is the typed status event for the hardware stream. The
// console derives its unavailable placeholder rendering from exactly this
// event; GPU metrics are never fabricated as zeros.
type hardwareStatus struct {
	Stream string `json:"stream"`
	GPU    string `json:"gpu"`
	Reason string `json:"reason,omitempty"`
}

// RunHardware samples s every interval and publishes "sample" events plus a
// sticky "status" event (republished on state change) to h until ctx ends.
func RunHardware(ctx context.Context, s sampler.Sampler, h *hub.Hub, interval time.Duration, log *slog.Logger) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	lastStatus := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		smp, err := s.Sample(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Error("hardware sample failed", "err", err)
			continue
		}
		publishStatus(h, smp.GPUStatus, &lastStatus)
		payload := contracts.HardwareSample{}
		if smp.CPUValid {
			payload.CPUUtilPct = ptr(round1(smp.CPUUtilPct))
		}
		if smp.GPU != nil {
			payload.GPUUtilPct = ptr(round1(smp.GPU.UtilPct))
			payload.VRAMUsedMB = ptr(round1(smp.GPU.VRAMUsedMB))
			payload.VRAMTotalMB = ptr(round1(smp.GPU.VRAMTotalMB))
			payload.JunctionC = ptr(round1(smp.GPU.JunctionC))
			payload.PowerW = ptr(round1(smp.GPU.PowerW))
			sessions := smp.GPU.EncoderSessions
			payload.EncoderSessions = &sessions
		}
		// Suppress a fully-empty frame: with no GPU and CPU not yet
		// primed (first sample before a /proc/stat delta), every field
		// is nil and the payload would marshal to {}, a meaningless
		// event. The GPUStatus event above already carries the
		// hardware-unavailable signal the console needs; the next tick
		// carries at least cpuUtilPct.
		if !smp.CPUValid && smp.GPU == nil {
			continue
		}
		data, err := json.Marshal(payload)
		if err != nil {
			log.Error("marshal hardware sample", "err", err)
			continue
		}
		h.Publish(hub.Event{Name: "sample", Data: data})
	}
}

func publishStatus(h *hub.Hub, st sampler.GPUStatus, last *string) {
	key := st.State + "|" + st.Reason
	if key == *last {
		return
	}
	*last = key
	data, err := json.Marshal(hardwareStatus{Stream: "hardware", GPU: st.State, Reason: st.Reason})
	if err != nil {
		return
	}
	h.PublishSticky(hub.Event{Name: "status", Data: data})
}

func ptr(f float64) *float64 { return &f }

// round1 rounds to one decimal place, correct for negative inputs too
// (unclamped readings such as powerW or junctionC could go negative on a
// misreporting sensor and must not round toward zero).
func round1(f float64) float64 {
	return math.Round(f*10) / 10
}
