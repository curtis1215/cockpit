package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"time"

	"cockpit-agent/internal/config"
	"cockpit-agent/internal/executor"
	"cockpit-agent/internal/httpclient"
	"cockpit-agent/internal/jobrunner"
	"cockpit-agent/internal/reporter"
	"cockpit-agent/internal/supervisor"
)

func main() {
	cfgPath := flag.String("config", "/etc/cockpit-agent/config.json", "path to config json")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	c := httpclient.New(cfg.ServerURL, cfg.AgentToken,
		time.Duration(cfg.PollTimeoutSec+10)*time.Second)

	ctx := context.Background()

	// beszel supervisor (optional)
	sup := supervisor.New(cfg.BeszelCmd, cfg.BeszelArgs, 5*time.Second)
	go sup.Run(ctx)

	// periodic version report
	go func() {
		for {
			reportVersions(c, time.Duration(cfg.ExecTimeoutSec)*time.Second)
			time.Sleep(time.Duration(cfg.ReportIntervalSec) * time.Second)
		}
	}()

	// main long-poll loop
	execTimeout := time.Duration(cfg.ExecTimeoutSec) * time.Second
	controlInterval := time.Duration(cfg.ControlIntervalSec) * time.Second
	pollWait := cfg.PollTimeoutSec
	backoff := time.Second
	for {
		if err := pollOnce(c, execTimeout, controlInterval, pollWait); err != nil {
			log.Printf("poll error: %v (backoff %v)", err, backoff)
			time.Sleep(backoff)
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
			continue
		}
		backoff = time.Second
	}
}

// pollOnce does one long-poll; on job it runs it (bounded by execTimeout);
// on check it reports versions. The long-poll wait (25s) is bounded by the
// http client timeout configured in main (PollTimeoutSec + 10).
func pollOnce(c *httpclient.Client, execTimeout, controlInterval time.Duration, pollWait int) error {
	var resp struct {
		Type string        `json:"type"`
		Job  jobrunner.Job `json:"job"`
	}
	status, err := c.GetJSON(fmt.Sprintf("/api/agent/poll?wait=%d", pollWait), &resp)
	if err != nil {
		return err
	}
	if status == 204 {
		return nil
	}
	switch resp.Type {
	case "job":
		jobrunner.RunJob(c, resp.Job, controlInterval, execTimeout)
	case "check":
		reportVersions(c, execTimeout)
	}
	return nil
}

type installDef struct {
	Software     string `json:"software"`
	CurrentCmd   string `json:"current_cmd"`
	VersionRegex string `json:"version_regex"`
}

func reportVersions(c *httpclient.Client, execTimeout time.Duration) {
	var defs []installDef
	if _, err := c.GetJSON("/api/agent/installs", &defs); err != nil {
		log.Printf("installs error: %v", err)
		return
	}
	var reports []map[string]string
	for _, d := range defs {
		cur := ""
		executor.Run(context.Background(), d.CurrentCmd, "", execTimeout, func(l string) {
			if v := reporter.ParseVersion(l, d.VersionRegex); v != "" && cur == "" {
				cur = v
			}
		})
		reports = append(reports, map[string]string{"software": d.Software, "current_version": cur})
	}
	if len(reports) > 0 {
		c.PostJSON("/api/agent/report-versions", reports, nil)
	}
}
