package collect

import (
	"context"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ParseNvidiaSmi parses the output of:
//
//	nvidia-smi --query-gpu=utilization.gpu,temperature.gpu --format=csv,noheader,nounits
//
// It returns (utilization%, temperature°C, ok).
// Exported so it can be unit-tested directly.
func ParseNvidiaSmi(output string) (util float64, temp float64, ok bool) {
	line := strings.TrimSpace(output)
	if line == "" {
		return 0, 0, false
	}
	// Take only the first line in case there are multiple GPUs
	if idx := strings.Index(line, "\n"); idx >= 0 {
		line = line[:idx]
	}
	parts := strings.SplitN(line, ",", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	u, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	t, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return u, t, true
}

// nvidiaSmiQuery is the default GPU query implementation using nvidia-smi.
func nvidiaSmiQuery() (util float64, temp float64, ok bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx,
		"nvidia-smi",
		"--query-gpu=utilization.gpu,temperature.gpu",
		"--format=csv,noheader,nounits",
	).Output()
	if err != nil {
		return 0, 0, false
	}
	return ParseNvidiaSmi(string(out))
}
