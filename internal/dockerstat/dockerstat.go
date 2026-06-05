package dockerstat

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Service holds the info for a single docker container.
type Service struct {
	Name   string   `json:"name"`
	Kind   string   `json:"kind"`
	Status string   `json:"status"`
	CPU    *float64 `json:"cpu,omitempty"`
	Mem    *float64 `json:"mem,omitempty"`
	Port   int      `json:"port"`
}

// Collector collects docker service info. Run is injectable for testing.
type Collector struct {
	Run func(args ...string) (string, error)
}

// dockerOnce caches the resolved docker path so we only search once per process.
var (
	dockerOnce sync.Once
	dockerPath string // absolute path, or "" if not found
)

// findDocker returns the absolute path to the docker binary.
// It tries exec.LookPath first (respects PATH), then falls back to a list of
// well-known locations that are often absent from the launchd daemon PATH.
// Returns "" when docker cannot be found anywhere.
//
// The candidate list is the exported variable DockerCandidates to allow tests to
// override it without patching os.Stat.
var DockerCandidates = []string{
	"/usr/local/bin/docker",   // OrbStack / Docker Desktop symlink
	"/opt/homebrew/bin/docker", // Homebrew on Apple Silicon
	"/usr/bin/docker",          // system fallback
}

// findDocker resolves the docker binary path.
// lookPath and stat are injectable for unit tests; production callers pass nil
// to use the real exec.LookPath and os.Stat.
func findDocker(candidates []string, lookPath func(string) (string, error), stat func(string) error) string {
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	if stat == nil {
		stat = func(p string) error { _, err := os.Stat(p); return err }
	}
	if p, err := lookPath("docker"); err == nil {
		return p
	}
	for _, p := range candidates {
		if stat(p) == nil {
			return p
		}
	}
	return ""
}

func resolvedDockerPath() string {
	dockerOnce.Do(func() {
		dockerPath = findDocker(DockerCandidates, nil, nil)
	})
	return dockerPath
}

// New returns a Collector using the real docker executable.
func New() *Collector {
	return &Collector{Run: execDocker}
}

func execDocker(args ...string) (string, error) {
	p := resolvedDockerPath()
	if p == "" {
		return "", errors.New("docker not found")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, p, args...).Output()
	return string(out), err
}

// Collect returns the list of running docker services.
//
// Nil vs empty contract:
//   - nil  → docker command failed (not installed, daemon down, etc.); caller
//             should NOT report to the server, because we have no ground truth.
//   - []Service{} (non-nil, len==0) → docker is healthy, zero containers are
//             running; caller SHOULD POST the empty list so the server can clear
//             any stale rows from a previous state.
func (c *Collector) Collect() []Service {
	psOut, err := c.Run("ps", "--format", `{{json .}}`)
	if err != nil {
		return nil
	}
	statsOut, _ := c.Run("stats", "--no-stream", "--format", `{{json .}}`)
	svcs := parse(psOut, statsOut)
	if svcs == nil {
		svcs = []Service{} // ps succeeded → always non-nil, even when empty
	}
	return svcs
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
