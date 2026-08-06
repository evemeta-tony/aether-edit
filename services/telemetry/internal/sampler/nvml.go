// services/telemetry/internal/sampler/nvml.go

package sampler

import (
	"fmt"

	"github.com/NVIDIA/go-nvml/pkg/nvml"
)

// NVML is the production GPUReader backed by NVIDIA's official go-nvml
// bindings. go-nvml dlopens libnvidia-ml.so at Init time, so this binary
// builds and runs on hosts without NVIDIA drivers; NewNVML then returns an
// UnavailableError and the service reports honest absence.
type NVML struct {
	dev nvml.Device
}

// NewNVML initializes NVML and binds device index 0. It returns
// *UnavailableError when no usable NVIDIA GPU backend exists on this host.
func NewNVML() (*NVML, error) {
	if ret := nvml.Init(); ret != nvml.SUCCESS {
		return nil, &UnavailableError{Reason: "nvml init: " + nvml.ErrorString(ret)}
	}
	count, ret := nvml.DeviceGetCount()
	if ret != nvml.SUCCESS {
		nvml.Shutdown()
		return nil, &UnavailableError{Reason: "nvml device count: " + nvml.ErrorString(ret)}
	}
	if count == 0 {
		nvml.Shutdown()
		return nil, &UnavailableError{Reason: "no NVIDIA devices present"}
	}
	dev, ret := nvml.DeviceGetHandleByIndex(0)
	if ret != nvml.SUCCESS {
		nvml.Shutdown()
		return nil, &UnavailableError{Reason: "nvml device handle: " + nvml.ErrorString(ret)}
	}
	return &NVML{dev: dev}, nil
}

// Read implements GPUReader.
func (n *NVML) Read() (GPUSample, error) {
	var s GPUSample
	util, ret := n.dev.GetUtilizationRates()
	if ret != nvml.SUCCESS {
		return s, fmt.Errorf("nvml utilization: %s", nvml.ErrorString(ret))
	}
	s.UtilPct = float64(util.Gpu)
	mem, ret := n.dev.GetMemoryInfo()
	if ret != nvml.SUCCESS {
		return s, fmt.Errorf("nvml memory info: %s", nvml.ErrorString(ret))
	}
	s.VRAMUsedMB = float64(mem.Used) / (1024.0 * 1024.0)
	s.VRAMTotalMB = float64(mem.Total) / (1024.0 * 1024.0)
	temp, ret := n.dev.GetTemperature(nvml.TEMPERATURE_GPU)
	if ret != nvml.SUCCESS {
		return s, fmt.Errorf("nvml temperature: %s", nvml.ErrorString(ret))
	}
	s.JunctionC = float64(temp)
	powerMW, ret := n.dev.GetPowerUsage()
	if ret != nvml.SUCCESS {
		return s, fmt.Errorf("nvml power usage: %s", nvml.ErrorString(ret))
	}
	s.PowerW = float64(powerMW) / 1000.0
	sessions, _, _, ret := n.dev.GetEncoderStats()
	if ret != nvml.SUCCESS {
		return s, fmt.Errorf("nvml encoder stats: %s", nvml.ErrorString(ret))
	}
	s.EncoderSessions = int64(sessions)
	return s, nil
}

// Close implements GPUReader.
func (n *NVML) Close() error {
	if ret := nvml.Shutdown(); ret != nvml.SUCCESS {
		return fmt.Errorf("nvml shutdown: %s", nvml.ErrorString(ret))
	}
	return nil
}
