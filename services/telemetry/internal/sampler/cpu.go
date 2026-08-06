// services/telemetry/internal/sampler/cpu.go

package sampler

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// CPU computes total CPU utilization percent from successive reads of the
// aggregate "cpu" line in /proc/stat.
type CPU struct {
	path      string
	prevIdle  uint64
	prevTotal uint64
	primed    bool
}

// NewCPU creates a CPU sampler reading from path (normally /proc/stat) and
// primes the previous counters so the first Sample call after one interval
// yields a real delta.
func NewCPU(path string) (*CPU, error) {
	c := &CPU{path: path}
	idle, total, err := c.read()
	if err != nil {
		return nil, fmt.Errorf("prime cpu counters: %w", err)
	}
	c.prevIdle, c.prevTotal, c.primed = idle, total, true
	return c, nil
}

// Sample returns utilization percent over the window since the previous call.
func (c *CPU) Sample() (float64, error) {
	if !c.primed {
		return 0, errors.New("cpu sampler not primed")
	}
	idle, total, err := c.read()
	if err != nil {
		return 0, err
	}
	dIdle := idle - c.prevIdle
	dTotal := total - c.prevTotal
	c.prevIdle, c.prevTotal = idle, total
	if dTotal == 0 {
		return 0, errors.New("cpu counters did not advance")
	}
	util := 100.0 * (1.0 - float64(dIdle)/float64(dTotal))
	if util < 0 {
		util = 0
	}
	if util > 100 {
		util = 100
	}
	return util, nil
}

// read parses the aggregate cpu line. idle includes iowait; total is the sum
// of all fields.
func (c *CPU) read() (idle, total uint64, err error) {
	raw, err := os.ReadFile(c.path)
	if err != nil {
		return 0, 0, err
	}
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "cpu ") {
			continue
		}
		fields := strings.Fields(line)[1:]
		if len(fields) < 5 {
			return 0, 0, fmt.Errorf("malformed cpu line in %s", c.path)
		}
		for i, f := range fields {
			v, perr := strconv.ParseUint(f, 10, 64)
			if perr != nil {
				return 0, 0, fmt.Errorf("malformed cpu field %q in %s", f, c.path)
			}
			total += v
			if i == 3 || i == 4 { // idle, iowait
				idle += v
			}
		}
		return idle, total, nil
	}
	return 0, 0, fmt.Errorf("no aggregate cpu line in %s", c.path)
}
