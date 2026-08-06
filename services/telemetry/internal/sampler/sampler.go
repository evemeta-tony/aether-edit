// services/telemetry/internal/sampler/sampler.go

// Package sampler defines the hardware Sampler interface and its real
// implementations: CPU utilization from /proc/stat and GPU metrics via NVML.
package sampler

import (
	"context"
	"fmt"
)

// GPU state values used in the typed hardware status event.
const (
	GPUStateOK          = "ok"
	GPUStateUnavailable = "unavailable"
	GPUStateError       = "error"
)

// GPUSample carries one reading of the GPU metrics required by contract 4.
type GPUSample struct {
	UtilPct         float64
	VRAMUsedMB      float64
	VRAMTotalMB     float64
	JunctionC       float64
	PowerW          float64
	EncoderSessions int64
}

// GPUStatus is the typed status of the GPU side of the hardware stream.
// State is one of GPUStateOK, GPUStateUnavailable, GPUStateError. Reason is
// human readable and set whenever State is not GPUStateOK.
type GPUStatus struct {
	State  string
	Reason string
}

// Sample is one full hardware sample. GPU is nil exactly when GPUStatus.State
// is not GPUStateOK; absent hardware is reported, never zero-filled.
// CPUValid is false when a CPU utilization delta could not be computed yet.
type Sample struct {
	CPUUtilPct float64
	CPUValid   bool
	GPU        *GPUSample
	GPUStatus  GPUStatus
}

// Sampler produces hardware samples at the pipeline's cadence.
type Sampler interface {
	Sample(ctx context.Context) (Sample, error)
	Close() error
}

// UnavailableError is returned by GPU reader constructors when no usable GPU
// backend exists on this host (no NVML library, no devices, init failure).
type UnavailableError struct {
	Reason string
}

func (e *UnavailableError) Error() string {
	return fmt.Sprintf("gpu unavailable: %s", e.Reason)
}

// GPUReader reads GPU metrics from a concrete backend (NVML in production).
type GPUReader interface {
	Read() (GPUSample, error)
	Close() error
}

// System is the production Sampler: CPU from /proc/stat plus an optional
// GPUReader. If gpu is nil the status is permanently unavailable with the
// supplied reason.
type System struct {
	cpu          *CPU
	gpu          GPUReader
	gpuAbsentWhy string
}

// NewSystem builds the production sampler. gpu may be nil when the GPU
// backend is unavailable; absentReason then explains why.
func NewSystem(cpu *CPU, gpu GPUReader, absentReason string) *System {
	return &System{cpu: cpu, gpu: gpu, gpuAbsentWhy: absentReason}
}

// Sample implements Sampler.
func (s *System) Sample(ctx context.Context) (Sample, error) {
	if err := ctx.Err(); err != nil {
		return Sample{}, err
	}
	out := Sample{}
	cpuPct, err := s.cpu.Sample()
	if err == nil {
		out.CPUUtilPct = cpuPct
		out.CPUValid = true
	}
	if s.gpu == nil {
		out.GPUStatus = GPUStatus{State: GPUStateUnavailable, Reason: s.gpuAbsentWhy}
		return out, nil
	}
	gs, err := s.gpu.Read()
	if err != nil {
		out.GPUStatus = GPUStatus{State: GPUStateError, Reason: err.Error()}
		return out, nil
	}
	out.GPU = &gs
	out.GPUStatus = GPUStatus{State: GPUStateOK}
	return out, nil
}

// Close implements Sampler.
func (s *System) Close() error {
	if s.gpu != nil {
		return s.gpu.Close()
	}
	return nil
}
