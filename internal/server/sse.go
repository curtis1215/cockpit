package server

import (
	"fmt"
	"net/http"
	"strings"
	"time"
)

// registerSSE 是 "/api/jobs/"（含子路徑）唯一的註冊點：
// /api/jobs/{id}/log/stream → SSE；其餘 → handleJobSub（version_api.go）。
func (s *Server) registerSSE() {
	s.mux.HandleFunc("/api/jobs/", s.dispatchJobsWithSSE)
}

func (s *Server) dispatchJobsWithSSE(w http.ResponseWriter, r *http.Request) {
	if strings.HasSuffix(r.URL.Path, "/log/stream") {
		s.streamLog(w, r)
		return
	}
	s.handleJobSub(w, r)
}

func (s *Server) streamLog(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/jobs/")
	id := parseInt64(strings.TrimSuffix(rest, "/log/stream"))
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	flusher, _ := w.(http.Flusher)
	sent := 0
	for {
		job, err := s.st.GetJob(id)
		if err != nil {
			fmt.Fprintf(w, "event: error\ndata: job not found\n\n")
			return
		}
		var ready []string
		if job.Log != "" {
			ready = strings.Split(strings.TrimSuffix(job.Log, "\n"), "\n")
			if !strings.HasSuffix(job.Log, "\n") && len(ready) > 0 {
				ready = ready[:len(ready)-1] // 最後一行未完整寫入（無結尾換行）→ 下一輪再送
			}
		}
		for ; sent < len(ready); sent++ {
			fmt.Fprintf(w, "event: log\ndata: %s\n\n", ready[sent])
		}
		done := job.Status == "success" || job.Status == "failed" || job.Status == "aborted"
		if done {
			fmt.Fprintf(w, "event: done\ndata: %s\n\n", job.Status)
		}
		if flusher != nil {
			flusher.Flush()
		}
		if done {
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(500 * time.Millisecond):
		}
	}
}
