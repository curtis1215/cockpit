package jobrunner

import (
	"context"
	"fmt"
	"time"

	"cockpit-agent/internal/executor"
	"cockpit-agent/internal/httpclient"
	"cockpit-agent/internal/reporter"
)

type Job struct {
	ID           int    `json:"id"`
	Software     string `json:"software"`
	Machine      string `json:"machine"`
	ShellCmd     string `json:"shell_cmd"`
	Cwd          string `json:"cwd"`
	CurrentCmd   string `json:"current_cmd"`
	VersionRegex string `json:"version_regex"`
}

// RunJob executes the server-rendered command, streams log lines, polls the
// control endpoint for abort, verifies the new version on success, and reports
// the result. Single job at a time (caller guarantees).
func RunJob(c *httpclient.Client, job Job, controlInterval, execTimeout time.Duration) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	aborted := make(chan struct{}) // closed by control poller on abort (synchronizes the signal)

	// control poller → cancels exec on abort
	go func() {
		t := time.NewTicker(controlInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				var ctrl struct{ Abort bool `json:"abort"` }
				if _, err := c.GetJSON(fmt.Sprintf("/api/agent/jobs/%d/control", job.ID), &ctrl); err == nil && ctrl.Abort {
					close(aborted)
					cancel()
					return
				}
			}
		}
	}()

	postLine := func(line string) {
		c.PostJSON(fmt.Sprintf("/api/agent/jobs/%d/log", job.ID),
			map[string]any{"lines": []string{line}}, nil)
	}

	res := executor.Run(ctx, job.ShellCmd, job.Cwd, execTimeout, postLine)

	select {
	case <-aborted: // abort signalled (channel close happens-before this read)
		postLine("■ 已由使用者中止")
		report(c, job.ID, "aborted", res.ExitCode, "")
		return
	default:
	}

	if res.ExitCode != 0 {
		report(c, job.ID, "failed", res.ExitCode, "")
		return
	}

	// verify new version
	newVersion := ""
	vres := executor.Run(context.Background(), job.CurrentCmd, job.Cwd, execTimeout, func(l string) {
		if v := reporter.ParseVersion(l, job.VersionRegex); v != "" && newVersion == "" {
			newVersion = v
		}
	})
	_ = vres
	report(c, job.ID, "success", res.ExitCode, newVersion)
}

func report(c *httpclient.Client, jobID int, status string, exit int, newVersion string) {
	c.PostJSON(fmt.Sprintf("/api/agent/jobs/%d/result", jobID),
		map[string]any{"status": status, "exit_code": exit, "new_version": newVersion}, nil)
}
