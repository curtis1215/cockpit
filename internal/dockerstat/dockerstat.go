package dockerstat

import (
	"context"
	"encoding/json"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Service holds the info for a single docker container.
type Service struct {
	Name   string  `json:"name"`
	Kind   string  `json:"kind"`
	Status string  `json:"status"`
	CPU    *float64 `json:"cpu,omitempty"`
	Mem    *float64 `json:"mem,omitempty"`
	Port   int     `json:"port"`
}

// Collector collects docker service info. Run is injectable for testing.
type Collector struct {
	Run func(args ...string) (string, error)
}

// New returns a Collector using the real docker executable.
func New() *Collector {
	return &Collector{Run: execDocker}
}

func execDocker(args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", args...).Output()
	return string(out), err
}

// Collect returns the list of running docker services.
// Returns nil (not empty slice) when docker is not available at all.
func (c *Collector) Collect() []Service {
	psOut, err := c.Run("ps", "--format", `{{json .}}`)
	if err != nil {
		return nil
	}
	statsOut, _ := c.Run("stats", "--no-stream", "--format", `{{json .}}`)
	return parse(psOut, statsOut)
}

type psRow struct {
	Names string `json:"Names"`
	State string `json:"State"`
	Ports string `json:"Ports"`
}

type stRow struct {
	Name    string `json:"Name"`
	CPUPerc string `json:"CPUPerc"`
	MemPerc string `json:"MemPerc"`
}

func parse(psOut, statsOut string) []Service {
	stats := map[string]stRow{}
	for _, line := range strings.Split(strings.TrimSpace(statsOut), "\n") {
		if line == "" {
			continue
		}
		var r stRow
		if json.Unmarshal([]byte(line), &r) == nil {
			stats[r.Name] = r
		}
	}

	var out []Service
	for _, line := range strings.Split(strings.TrimSpace(psOut), "\n") {
		if line == "" {
			continue
		}
		var p psRow
		if json.Unmarshal([]byte(line), &p) != nil {
			continue
		}
		svc := Service{
			Name:   p.Names,
			Kind:   "docker",
			Status: normState(p.State),
			Port:   firstPort(p.Ports),
		}
		if st, ok := stats[p.Names]; ok {
			if v, err := strconv.ParseFloat(strings.TrimSuffix(st.CPUPerc, "%"), 64); err == nil {
				svc.CPU = &v
			}
			if v, err := strconv.ParseFloat(strings.TrimSuffix(st.MemPerc, "%"), 64); err == nil {
				svc.Mem = &v
			}
		}
		out = append(out, svc)
	}
	return out
}

func normState(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// firstPort parses "0.0.0.0:6379->6379/tcp, ..." and returns the host port number.
func firstPort(ports string) int {
	// Take only the first entry before comma.
	part := ports
	if idx := strings.Index(ports, ","); idx >= 0 {
		part = ports[:idx]
	}
	// Look for host:port->... pattern.
	if idx := strings.Index(part, "->"); idx >= 0 {
		hostPart := strings.TrimSpace(part[:idx])
		// hostPart may be "0.0.0.0:6379" or ":::6379" or just "6379"
		if colon := strings.LastIndex(hostPart, ":"); colon >= 0 {
			hostPart = hostPart[colon+1:]
		}
		if p, err := strconv.Atoi(strings.TrimSpace(hostPart)); err == nil {
			return p
		}
	}
	return 0
}
