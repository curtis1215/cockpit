package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/curtis1215/cockpit/internal/store"
)

// registerManageAPI wires manage-related routes. Note: /api/systems and /api/systems/ are
// already registered in monitor_api routes; this file only provides the handler implementations
// called from those handlers.
func (s *Server) registerManageAPI() {
	// Nothing to register here — POST /api/systems is dispatched from apiSystemsEnriched,
	// and PATCH/DELETE/enroll-token are dispatched from apiSystemSub.
}

// createSystem handles POST /api/systems: creates a pending system with an enroll token.
func (s *Server) createSystem(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Label string `json:"label"`
		Role  string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}
	if body.Label == "" {
		writeJSON(w, 400, map[string]string{"error": "label required"})
		return
	}
	id, enrollToken, err := s.st.CreateSystemPending(body.Label, body.Role)
	if err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeJSON(w, 409, map[string]string{"error": "label already exists"})
			return
		}
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]string{
		"id":          id,
		"label":       body.Label,
		"enroll_token": enrollToken,
	})
}

// patchSystem handles PATCH /api/systems/{id}: updates label and/or role.
func (s *Server) patchSystem(w http.ResponseWriter, r *http.Request, id string) {
	var body struct {
		Label *string `json:"label"`
		Role  *string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}

	// Check if the current system exists
	sys, err := s.st.SystemByID(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, 404, map[string]string{"error": "system not found"})
			return
		}
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}

	newLabel := ""
	if body.Label != nil {
		newLabel = *body.Label
	}
	newRole := ""
	if body.Role != nil {
		newRole = *body.Role
	}

	// If label is being changed, check if current label has installs in inventory
	if newLabel != "" && newLabel != sys.Label {
		inv := s.inv
		for _, sw := range inv.Software {
			for _, ins := range sw.Installs {
				if ins.Machine == sys.Label {
					writeJSON(w, 409, map[string]string{"error": "machine has installs; rename not supported yet"})
					return
				}
			}
		}
	}

	if err := s.st.UpdateSystem(id, newLabel, newRole); err != nil {
		if errors.Is(err, store.ErrConflict) {
			writeJSON(w, 409, map[string]string{"error": "label already exists"})
			return
		}
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, 404, map[string]string{"error": "system not found"})
			return
		}
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}

	// Return updated system
	rows, err := s.st.SystemsWithLatest()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	for _, x := range rows {
		if x.ID == id {
			writeJSON(w, 200, systemMap(x))
			return
		}
	}
	writeJSON(w, 404, map[string]string{"error": "system not found"})
}

// regenEnrollToken handles POST /api/systems/{id}/enroll-token: generates a new enroll token.
func (s *Server) regenEnrollToken(w http.ResponseWriter, r *http.Request, id string) {
	token, err := s.st.RegenEnrollToken(id)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeJSON(w, 404, map[string]string{"error": "system not found"})
			return
		}
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]string{"enroll_token": token})
}
