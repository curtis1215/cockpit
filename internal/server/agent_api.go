package server

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/curtis1215/cockpit/internal/store"
)

func (s *Server) registerAgentAPI() {
	s.mux.HandleFunc("/api/agent/enroll", s.handleEnroll)
	s.mux.HandleFunc("/api/agent/heartbeat", s.handleHeartbeat)
}

func (s *Server) handleEnroll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	var body struct {
		Label        string `json:"label"`
		OS           string `json:"os"`
		Arch         string `json:"arch"`
		EnrollSecret string `json:"enroll_secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}
	if s.enrollSecret == "" || body.EnrollSecret != s.enrollSecret {
		writeJSON(w, 401, map[string]string{"error": "invalid enroll secret"})
		return
	}
	label := body.Label
	if label == "" {
		label = "unnamed"
	}
	id, token, err := s.st.RegisterSystem(label, body.OS, body.Arch)
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]string{"system_id": id, "agent_token": token})
}

func (s *Server) bearer(r *http.Request) (store.System, bool) {
	h := r.Header.Get("Authorization")
	if !strings.HasPrefix(h, "Bearer ") {
		return store.System{}, false
	}
	sys, err := s.st.SystemByAgentToken(strings.TrimSpace(h[len("Bearer "):]))
	if err != nil {
		return store.System{}, false
	}
	return sys, true
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(405)
		return
	}
	sysID, ok := s.agentSystem(r)
	if !ok {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	var body struct {
		AgentVersion string `json:"agent_version"`
	}
	json.NewDecoder(r.Body).Decode(&body)
	if err := s.st.HeartbeatByID(sysID, body.AgentVersion); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(204)
}
