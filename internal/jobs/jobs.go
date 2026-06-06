package jobs

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/curtis1215/cockpit/internal/inventory"
	"github.com/curtis1215/cockpit/internal/store"
)

var ErrActiveJobExists = errors.New("active job exists")

func find(inv inventory.Inventory, software, machine string) (inventory.Software, inventory.Install, error) {
	for _, sw := range inv.Software {
		if sw.Name == software {
			for _, inst := range sw.Installs {
				if inst.Machine == machine {
					return sw, inst, nil
				}
			}
		}
	}
	return inventory.Software{}, inventory.Install{}, fmt.Errorf("install not found: %s@%s", software, machine)
}

func render(tmpl string, vars map[string]string) string {
	out := tmpl
	for k, v := range vars {
		out = strings.ReplaceAll(out, "{"+k+"}", v)
	}
	return out
}

// shellQuote 單引號包裹（POSIX）：把 ' 換成 '\” 。
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func BuildUpdate(inv inventory.Inventory, sw inventory.Software, inst inventory.Install, latest, current, changelogZh string) (string, inventory.Machine, error) {
	up := inst.Update
	target := up.Machine
	if target == "" {
		target = inst.Machine
	}
	machine := inv.Machines[target]
	if up.Type == "command" {
		return up.Cmd, machine, nil
	}
	prompt := render(up.Prompt, map[string]string{
		"name": sw.Name, "machine": target, "current_version": current,
		"latest_version": latest, "changelog_zh": changelogZh, "cwd": up.Cwd,
	})
	switch up.Runner {
	case "codex_exec":
		cd := ""
		if up.Cwd != "" {
			cd = "--cd " + shellQuote(up.Cwd) + " "
		}
		return "codex exec " + cd + shellQuote(prompt), machine, nil
	case "claude_p":
		cd := ""
		if up.Cwd != "" {
			cd = "cd " + shellQuote(up.Cwd) + " && "
		}
		return cd + "claude -p " + shellQuote(prompt), machine, nil
	case "custom":
		cwdq := ""
		if up.Cwd != "" {
			cwdq = shellQuote(up.Cwd)
		}
		return render(up.Invoke, map[string]string{"prompt": shellQuote(prompt), "cwd": cwdq}), machine, nil
	default:
		return "", machine, fmt.Errorf("unknown runner: %s", up.Runner)
	}
}

func StartJob(s *store.Store, inv inventory.Inventory, software, machine string) (int64, error) {
	_, inst, err := find(inv, software, machine)
	if err != nil {
		return 0, err
	}
	jid, err := s.CreateJobUnique(software, machine, inst.Update.Type, inst.Update.Runner)
	if err != nil {
		return 0, err
	}
	if jid == 0 {
		return 0, ErrActiveJobExists
	}
	return jid, nil
}

type Claimed struct {
	ID                               int64
	Software, Machine, ShellCmd, Cwd string
	CurrentCmd, VersionRegex         string
}

func ClaimNextJob(s *store.Store, inv inventory.Inventory, machine string) (*Claimed, error) {
	row, err := s.ClaimOldestQueued(machine)
	if err != nil || row == nil {
		return nil, err
	}
	sw, inst, err := find(inv, row.Software, row.Machine)
	if err != nil {
		return nil, err
	}
	latest, _ := s.LatestVersion(sw.Name)
	cur, _ := s.GetInstall(sw.Name, inst.Machine)
	cmd, _, err := BuildUpdate(inv, sw, inst, latest.VersionStr, cur.CurrentVersion, latest.ChangelogZh)
	if err != nil {
		return nil, err
	}
	cwd := ""
	if inst.Update.Type == "agent" {
		cwd = inst.Update.Cwd
	}
	s.SetJobDispatch(row.ID, cmd, cwd, inst.CurrentCmd, inst.VersionRegex)
	return &Claimed{ID: row.ID, Software: sw.Name, Machine: inst.Machine, ShellCmd: cmd, Cwd: cwd, CurrentCmd: inst.CurrentCmd, VersionRegex: inst.VersionRegex}, nil
}

func RecordResult(s *store.Store, jobID int64, status string, exit int, newVersion string) error {
	job, err := s.GetJob(jobID)
	if err != nil {
		return err
	}
	if status == "success" && newVersion != "" {
		s.UpsertInstall(job.Software, job.Machine, newVersion, "up_to_date", time.Now().UTC().Format(time.RFC3339))
	}
	s.FinishJob(jobID, status, exit, newVersion)
	s.AddEvent("update", job.Software, job.Machine, fmt.Sprintf("job %d %s exit=%d new=%s", jobID, status, exit, newVersion))
	return nil
}

func RequestAbort(s *store.Store, jobID int64) (store.Job, error) {
	job, err := s.GetJob(jobID)
	if err != nil {
		return store.Job{}, err
	}
	switch job.Status {
	case "queued":
		s.FinishJob(jobID, "aborted", -1, "")
		s.AddEvent("update", job.Software, job.Machine, fmt.Sprintf("job %d aborted (queued)", jobID))
	case "running":
		s.RequestAbort(jobID)
	}
	return s.GetJob(jobID)
}
