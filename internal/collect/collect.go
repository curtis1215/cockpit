// Package collect gathers machine metrics using gopsutil + nvidia-smi.
package collect

import (
	"runtime"
	"time"

	"github.com/shirou/gopsutil/v4/cpu"
	"github.com/shirou/gopsutil/v4/disk"
	"github.com/shirou/gopsutil/v4/host"
	"github.com/shirou/gopsutil/v4/load"
	"github.com/shirou/gopsutil/v4/mem"
	gnet "github.com/shirou/gopsutil/v4/net"
	"github.com/shirou/gopsutil/v4/sensors"
)

// Metrics holds a single collected snapshot.
// All nullable fields use *float64 so absent readings are omitted cleanly.
type Metrics struct {
	TS      int64    `json:"ts"`
	CPU     *float64 `json:"cpu,omitempty"`
	Mem     *float64 `json:"mem,omitempty"`
	Disk    *float64 `json:"disk,omitempty"`
	GPU     *float64 `json:"gpu,omitempty"`
	NetUp   *float64 `json:"net_up,omitempty"`
	NetDown *float64 `json:"net_down,omitempty"`
	Load    *float64 `json:"load,omitempty"`
	Temp    *float64 `json:"temp,omitempty"`
	Uptime  *float64 `json:"uptime,omitempty"`
}

// Collector holds state between calls (for net rate calculation).
type Collector struct {
	// gpuQuery is injectable for testing. Defaults to nvidiaSmiQuery.
	gpuQuery func() (util float64, temp float64, ok bool)

	// previous net counters for rate calculation
	prevSent uint64
	prevRecv uint64
	prevAt   time.Time
}

// New returns a Collector with the default nvidia-smi GPU query.
func New() *Collector {
	return &Collector{gpuQuery: nvidiaSmiQuery}
}

// NewWithGPU returns a Collector with a custom GPU query function.
// Pass nil to disable GPU collection.
func NewWithGPU(gpuQuery func() (float64, float64, bool)) *Collector {
	if gpuQuery == nil {
		gpuQuery = func() (float64, float64, bool) { return 0, 0, false }
	}
	return &Collector{gpuQuery: gpuQuery}
}

// pf returns a pointer to v (convenience helper).
func pf(v float64) *float64 { return &v }

// round1 rounds to 1 decimal place.
func round1(v float64) float64 { return float64(int(v*10+0.5)) / 10 }

// round2 rounds to 2 decimal places.
func round2(v float64) float64 { return float64(int(v*100+0.5)) / 100 }

// Collect gathers a full Metrics snapshot.
// Errors from individual sub-collectors are non-fatal; the field is left nil.
func (c *Collector) Collect() (Metrics, error) {
	m := Metrics{TS: time.Now().Unix()}

	// ── CPU (1-second interval measurement) ──────────────────────────────────
	if percents, err := cpu.Percent(time.Second, false); err == nil && len(percents) > 0 {
		m.CPU = pf(round1(percents[0]))
	}

	// ── Memory ───────────────────────────────────────────────────────────────
	if vm, err := mem.VirtualMemory(); err == nil {
		m.Mem = pf(round1(vm.UsedPercent))
	}

	// ── Disk (root/primary partition) ────────────────────────────────────────
	root := "/"
	if runtime.GOOS == "windows" {
		root = "C:\\"
	}
	if du, err := disk.Usage(root); err == nil {
		m.Disk = pf(round1(du.UsedPercent))
	}

	// ── Load average (1-min) ─────────────────────────────────────────────────
	// Not available on all platforms (e.g. Windows); leave nil on error.
	if avg, err := load.Avg(); err == nil {
		m.Load = pf(round2(avg.Load1))
	}

	// ── Host uptime (seconds) ─────────────────────────────────────────────────
	if up, err := host.Uptime(); err == nil {
		v := float64(up)
		m.Uptime = &v
	}

	// ── Temperature (max across all sensors) ─────────────────────────────────
	// Use SensorsTemperatures(); on Mac this reads SMC / powermetrics.
	// Errors and empty results are both treated as "no data" → nil.
	if temps, err := sensors.SensorsTemperatures(); err == nil && len(temps) > 0 {
		var maxTemp float64
		for _, t := range temps {
			if t.Temperature > maxTemp {
				maxTemp = t.Temperature
			}
		}
		if maxTemp > 0 {
			m.Temp = pf(round1(maxTemp))
		}
	}

	// ── Network rates (MB/s, only after second call) ─────────────────────────
	now := time.Now()
	if io, err := gnet.IOCounters(false); err == nil && len(io) > 0 {
		sent := io[0].BytesSent
		recv := io[0].BytesRecv
		if !c.prevAt.IsZero() {
			dt := now.Sub(c.prevAt).Seconds()
			if dt > 0 {
				up := float64(sent-c.prevSent) / dt / 1024 / 1024
				down := float64(recv-c.prevRecv) / dt / 1024 / 1024
				if up >= 0 && down >= 0 {
					m.NetUp = pf(round2(up))
					m.NetDown = pf(round2(down))
				}
			}
		}
		c.prevSent = sent
		c.prevRecv = recv
		c.prevAt = now
	}

	// ── GPU (nvidia-smi) ─────────────────────────────────────────────────────
	if util, temp, ok := c.gpuQuery(); ok {
		m.GPU = pf(round1(util))
		// GPU temp wins if higher than sensor reading
		if m.Temp == nil || temp > *m.Temp {
			m.Temp = pf(round1(temp))
		}
	}

	return m, nil
}
