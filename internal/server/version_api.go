package server

import (
	"net/http"
	"strings"

	"github.com/curtis1215/cockpit/internal/jobs"
	"github.com/curtis1215/cockpit/internal/store"
	"github.com/curtis1215/cockpit/internal/version"
)

func (s *Server) registerVersionAPI() {
	s.mux.HandleFunc("/api/installs", s.handleInstalls)    // GET list
	s.mux.HandleFunc("/api/changelog/", s.handleChangelog) // GET /api/changelog/{sw}/{ver}
	s.mux.HandleFunc("/api/jobs", s.handleJobs)            // GET recent
	s.mux.HandleFunc("/api/installs/", s.handleInstallSub) // POST /api/installs/{sw}/{m}/update
	s.mux.HandleFunc("/api/check", s.handleCheck)          // POST
	// NOTE: "/api/jobs/" (trailing slash) is registered ONCE in sse.go's registerSSE.
}

func (s *Server) handleInstalls(w http.ResponseWriter, r *http.Request) {
	inv := s.getInv()
	latest := s.st.LatestVersionMap()
	kindOf := map[string]string{}
	updKind := map[string]string{}
	for _, sw := range inv.Software {
		kindOf[sw.Name] = sw.Kind
		for _, ins := range sw.Installs {
			updKind[sw.Name+"::"+ins.Machine] = ins.Update.Type
		}
	}
	rows, _ := s.st.ListInstalls()
	out := []map[string]any{}
	for _, in := range rows {
		lv := latest[in.Software]
		liveStatus, behind := version.Compare(in.CurrentVersion, lv)
		status := liveStatus
		if in.Status == "error" {
			status = "error"
		}
		var errMsg any
		if status == "error" {
			errMsg = s.st.LastError(in.Software, in.Machine)
		}
		out = append(out, map[string]any{
			"id": in.Software + "::" + in.Machine, "software": in.Software, "machine": in.Machine,
			"kind": kindOf[in.Software], "current_version": in.CurrentVersion, "latest_version": nilIfEmpty(lv),
			"status": status, "behind_count": behind, "update_kind": updKind[in.Software+"::"+in.Machine],
			"error": errMsg, "last_checked": in.LastChecked,
		})
	}
	writeJSON(w, 200, out)
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func (s *Server) handleChangelog(w http.ResponseWriter, r *http.Request) {
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/changelog/"), "/")
	if len(parts) != 2 {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	v, err := s.st.GetVersion(parts[0], parts[1])
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "version not found"})
		return
	}
	writeJSON(w, 200, map[string]any{"software": parts[0], "version": parts[1],
		"changelog_zh": v.ChangelogZh, "changelog_raw": v.ChangelogRaw, "released_at": v.ReleasedAt})
}

func (s *Server) handleJobs(w http.ResponseWriter, r *http.Request) {
	list, _ := s.st.ListJobs(50)
	out := []map[string]any{}
	for _, j := range list {
		out = append(out, jobMap(j))
	}
	writeJSON(w, 200, out)
}

func jobMap(j store.Job) map[string]any {
	return map[string]any{"id": j.ID, "software": j.Software, "machine": j.Machine, "kind": j.Kind,
		"runner": j.Runner, "status": j.Status, "started_at": j.StartedAt, "finished_at": j.FinishedAt,
		"exit_code": j.ExitCode, "new_version": j.NewVersion, "log": j.Log, "cmd": j.Cmd}
}

func (s *Server) handleInstallSub(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/installs/")
	parts := strings.Split(rest, "/")
	if len(parts) != 3 || parts[2] != "update" || r.Method != http.MethodPost {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	jid, err := jobs.StartJob(s.st, s.getInv(), parts[0], parts[1])
	if err == jobs.ErrActiveJobExists {
		writeJSON(w, 409, map[string]string{"error": "update already in progress"})
		return
	}
	if err != nil {
		writeJSON(w, 404, map[string]string{"error": "install not found"})
		return
	}
	writeJSON(w, 200, map[string]any{"job_id": jid})
}

func (s *Server) handleJobSub(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/jobs/")
	parts := strings.Split(rest, "/")
	id := parseInt64(parts[0])
	if len(parts) == 1 {
		j, err := s.st.GetJob(id)
		if err != nil {
			writeJSON(w, 404, map[string]string{"error": "job not found"})
			return
		}
		writeJSON(w, 200, jobMap(j))
		return
	}
	if len(parts) == 2 && parts[1] == "abort" && r.Method == http.MethodPost {
		job, err := jobs.RequestAbort(s.st, id)
		if err != nil {
			writeJSON(w, 404, map[string]string{"error": "job not found"})
			return
		}
		writeJSON(w, 200, jobMap(job))
		return
	}
	writeJSON(w, 404, map[string]string{"error": "not found"})
}

func (s *Server) handleCheck(w http.ResponseWriter, r *http.Request) {
	inv := s.getInv()
	if s.onCheck != nil {
		go s.onCheck()
	}
	// inventory 機器與所有 DB systems（label）都設旗標——enrolled 機器不一定在 inventory.machines。
	for name := range inv.Machines {
		s.st.SetCheckRequested(name)
	}
	if systems, err := s.st.ListSystems(); err == nil {
		for _, sys := range systems {
			s.st.SetCheckRequested(sys.Label)
		}
	}
	writeJSON(w, 200, map[string]bool{"started": true})
}

func parseInt64(str string) int64 {
	var n int64
	for _, c := range str {
		if c < '0' || c > '9' {
			return 0
		}
		n = n*10 + int64(c-'0')
	}
	return n
}
