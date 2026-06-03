package server

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"github.com/curtis1215/cockpit/internal/collector"
	"github.com/curtis1215/cockpit/internal/inventory"
	"github.com/curtis1215/cockpit/internal/jobs"
)

func (s *Server) registerAgentVT() {
	s.mux.HandleFunc("/api/agent/installs", s.vtInstalls)
	s.mux.HandleFunc("/api/agent/poll", s.vtPoll)
	s.mux.HandleFunc("/api/agent/report-versions", s.vtReportVersions)
	s.mux.HandleFunc("/api/agent/jobs/", s.vtJobSub)
}

// vtMachine 用 inventory 的 agent_token 解析 Bearer → machine 名（版本 API 認證；P0 systems token 是另一套，P3 收斂）。
func (s *Server) vtMachine(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return "", false
	}
	m := inventory.MachineForToken(s.inv, strings.TrimSpace(h[len("Bearer "):]))
	return m, m != ""
}

func (s *Server) vtInstalls(w http.ResponseWriter, r *http.Request) {
	machine, ok := s.vtMachine(r)
	if !ok {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	out := []map[string]any{}
	for _, sw := range s.inv.Software {
		for _, ins := range sw.Installs {
			if ins.Machine == machine {
				out = append(out, map[string]any{"software": sw.Name, "current_cmd": ins.CurrentCmd, "version_regex": nilIfEmpty(ins.VersionRegex)})
			}
		}
	}
	writeJSON(w, 200, out)
}

func (s *Server) vtPoll(w http.ResponseWriter, r *http.Request) {
	machine, ok := s.vtMachine(r)
	if !ok {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	waitSec := 0
	for _, c := range r.URL.Query().Get("wait") {
		if c >= '0' && c <= '9' {
			waitSec = waitSec*10 + int(c-'0')
		}
	}
	if waitSec > 25 {
		waitSec = 25
	}
	deadline := time.Now().Add(time.Duration(waitSec) * time.Second)
	for {
		claimed, _ := jobs.ClaimNextJob(s.st, s.inv, machine)
		if claimed != nil {
			writeJSON(w, 200, map[string]any{"type": "job", "job": map[string]any{
				"id": claimed.ID, "software": claimed.Software, "machine": claimed.Machine,
				"shell_cmd": claimed.ShellCmd, "cwd": claimed.Cwd, "current_cmd": claimed.CurrentCmd,
				"version_regex": nilIfEmpty(claimed.VersionRegex)}})
			return
		}
		if s.st.TakeCheckRequested(machine) {
			writeJSON(w, 200, map[string]string{"type": "check"})
			return
		}
		if !time.Now().Before(deadline) {
			w.WriteHeader(204)
			return
		}
		select {
		case <-r.Context().Done():
			w.WriteHeader(204)
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func (s *Server) vtReportVersions(w http.ResponseWriter, r *http.Request) {
	machine, ok := s.vtMachine(r)
	if !ok {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	var reports []collector.Report
	json.NewDecoder(r.Body).Decode(&reports)
	n := collector.ApplyVersionReport(s.st, machine, reports)
	writeJSON(w, 200, map[string]int{"applied": n})
}

func (s *Server) vtJobSub(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.vtMachine(r); !ok {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/agent/jobs/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	id := parseInt64(parts[0])
	switch parts[1] {
	case "log":
		var body struct {
			Lines []string `json:"lines"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		for _, line := range body.Lines {
			s.st.AppendJobLog(id, line)
		}
		w.WriteHeader(204)
	case "result":
		var body struct {
			Status     string `json:"status"`
			ExitCode   int    `json:"exit_code"`
			NewVersion string `json:"new_version"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		jobs.RecordResult(s.st, id, body.Status, body.ExitCode, body.NewVersion)
		j, _ := s.st.GetJob(id)
		writeJSON(w, 200, jobMap(j))
	case "control":
		writeJSON(w, 200, map[string]bool{"abort": s.st.AbortRequested(id)})
	default:
		writeJSON(w, 404, map[string]string{"error": "not found"})
	}
}
