package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/curtis1215/cockpit/internal/inventory"
	"github.com/curtis1215/cockpit/internal/store"
)

// registerManageAPI wires manage-related routes. Note: /api/systems and /api/systems/ are
// already registered in monitor_api routes; this file only provides the handler implementations
// called from those handlers.
func (s *Server) registerManageAPI() {
	// POST /api/systems is dispatched from apiSystemsEnriched,
	// and PATCH/DELETE/enroll-token are dispatched from apiSystemSub.
	s.mux.HandleFunc("/api/software", s.handleSoftwareCollection)
	s.mux.HandleFunc("/api/software/", s.handleSoftwareSub)
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
		inv := s.getInv()
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

// ── Software CRUD ──────────────────────────────────────────────────────────

// handleSoftwareCollection handles POST /api/software.
func (s *Server) handleSoftwareCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		s.createSoftwareInstall(w, r)
		return
	}
	w.WriteHeader(405)
}

// handleSoftwareSub handles PATCH /api/software/{name}/{machine} and
// DELETE /api/software/{name}/{machine}.
func (s *Server) handleSoftwareSub(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/software/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	name, machine := parts[0], parts[1]
	switch r.Method {
	case http.MethodPatch:
		s.patchSoftwareInstall(w, r, name, machine)
	case http.MethodDelete:
		s.deleteSoftwareInstall(w, r, name, machine)
	default:
		w.WriteHeader(405)
	}
}

// softwareBody is the POST /api/software request body.
type softwareBody struct {
	Name         string         `json:"name"`
	Kind         string         `json:"kind"`
	LatestSource string         `json:"latest_source"`
	Changelog    string         `json:"changelog"`
	Machine      string         `json:"machine"`
	CurrentCmd   string         `json:"current_cmd"`
	VersionRegex string         `json:"version_regex"`
	Update       *softwareUpdate `json:"update"`
}

type softwareUpdate struct {
	Type    string `json:"type"`
	Cmd     string `json:"cmd"`
	Runner  string `json:"runner"`
	Prompt  string `json:"prompt"`
	Machine string `json:"machine"`
	Cwd     string `json:"cwd"`
	Invoke  string `json:"invoke"`
}

// machineKnown returns true if the machine label is present in inventory
// machines or as a registered system in the store.
func (s *Server) machineKnown(inv inventory.Inventory, machine string) bool {
	if _, ok := inv.Machines[machine]; ok {
		return true
	}
	return s.st.LabelExists(machine)
}

func parseUpdateBody(u *softwareUpdate) (inventory.Update, error) {
	if u == nil {
		return inventory.Update{}, nil
	}
	switch u.Type {
	case "command":
		if u.Cmd == "" {
			return inventory.Update{}, errors.New("update type 'command' requires cmd")
		}
		return inventory.Update{Type: "command", Cmd: u.Cmd}, nil
	case "agent":
		if u.Runner == "" {
			return inventory.Update{}, errors.New("update type 'agent' requires runner")
		}
		if u.Prompt == "" {
			return inventory.Update{}, errors.New("update type 'agent' requires prompt")
		}
		return inventory.Update{
			Type:    "agent",
			Runner:  u.Runner,
			Prompt:  u.Prompt,
			Machine: u.Machine,
			Cwd:     u.Cwd,
			Invoke:  u.Invoke,
		}, nil
	default:
		return inventory.Update{}, errors.New("update type must be 'command' or 'agent'")
	}
}

// createSoftwareInstall handles POST /api/software.
func (s *Server) createSoftwareInstall(w http.ResponseWriter, r *http.Request) {
	var body softwareBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}
	if body.Name == "" {
		writeJSON(w, 400, map[string]string{"error": "name required"})
		return
	}
	if body.Machine == "" {
		writeJSON(w, 400, map[string]string{"error": "machine required"})
		return
	}
	if body.CurrentCmd == "" {
		writeJSON(w, 400, map[string]string{"error": "current_cmd required"})
		return
	}

	inv := s.getInv()

	// Validate machine is known.
	if !s.machineKnown(inv, body.Machine) {
		writeJSON(w, 400, map[string]string{"error": "machine not found in inventory or systems"})
		return
	}

	// Parse and validate update.
	upd, err := parseUpdateBody(body.Update)
	if err != nil {
		writeJSON(w, 400, map[string]string{"error": err.Error()})
		return
	}

	// Check for duplicate (name, machine).
	for _, sw := range inv.Software {
		if sw.Name == body.Name {
			for _, ins := range sw.Installs {
				if ins.Machine == body.Machine {
					writeJSON(w, 409, map[string]string{"error": "install already exists"})
					return
				}
			}
		}
	}

	// Build new install.
	newInstall := inventory.Install{
		Machine:      body.Machine,
		CurrentCmd:   body.CurrentCmd,
		VersionRegex: body.VersionRegex,
		Update:       upd,
	}

	// Deep-copy software slice and append.
	newSW := make([]inventory.Software, len(inv.Software))
	copy(newSW, inv.Software)

	found := false
	for i, sw := range newSW {
		if sw.Name == body.Name {
			// Append install to existing software.
			instsCopy := make([]inventory.Install, len(sw.Installs)+1)
			copy(instsCopy, sw.Installs)
			instsCopy[len(sw.Installs)] = newInstall
			newSW[i] = inventory.Software{
				Name:         sw.Name,
				Kind:         sw.Kind,
				LatestSource: sw.LatestSource,
				Changelog:    sw.Changelog,
				Installs:     instsCopy,
			}
			found = true
			break
		}
	}

	if !found {
		// New software — latest_source required.
		if body.LatestSource == "" {
			writeJSON(w, 400, map[string]string{"error": "latest_source required for new software"})
			return
		}
		newSW = append(newSW, inventory.Software{
			Name:         body.Name,
			Kind:         body.Kind,
			LatestSource: body.LatestSource,
			Changelog:    body.Changelog,
			Installs:     []inventory.Install{newInstall},
		})
	}

	// Deep-copy machines map.
	newMachines := make(map[string]inventory.Machine, len(inv.Machines))
	for k, v := range inv.Machines {
		newMachines[k] = v
	}

	newInv := inventory.Inventory{Machines: newMachines, Software: newSW}
	if err := s.setInv(newInv, true); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}

	// Trigger upstream check for the new software in background.
	if s.onCheck != nil {
		go s.onCheck()
	}

	writeJSON(w, 200, map[string]bool{"ok": true})
}

// patchSoftwareInstall handles PATCH /api/software/{name}/{machine}.
func (s *Server) patchSoftwareInstall(w http.ResponseWriter, r *http.Request, name, machine string) {
	var body struct {
		Kind         *string         `json:"kind"`
		LatestSource *string         `json:"latest_source"`
		Changelog    *string         `json:"changelog"`
		CurrentCmd   *string         `json:"current_cmd"`
		VersionRegex *string         `json:"version_regex"`
		Update       *softwareUpdate `json:"update"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}

	inv := s.getInv()

	// Deep-copy software slice.
	newSW := make([]inventory.Software, len(inv.Software))
	copy(newSW, inv.Software)

	found := false
	for si, sw := range newSW {
		if sw.Name != name {
			continue
		}
		instsCopy := make([]inventory.Install, len(sw.Installs))
		copy(instsCopy, sw.Installs)
		for ii, ins := range instsCopy {
			if ins.Machine != machine {
				continue
			}
			// Apply install-level patches.
			if body.CurrentCmd != nil {
				ins.CurrentCmd = *body.CurrentCmd
			}
			if body.VersionRegex != nil {
				ins.VersionRegex = *body.VersionRegex
			}
			if body.Update != nil {
				upd, err := parseUpdateBody(body.Update)
				if err != nil {
					writeJSON(w, 400, map[string]string{"error": err.Error()})
					return
				}
				ins.Update = upd
			}
			instsCopy[ii] = ins
			found = true
		}
		// Apply software-level patches.
		updSW := inventory.Software{
			Name:         sw.Name,
			Kind:         sw.Kind,
			LatestSource: sw.LatestSource,
			Changelog:    sw.Changelog,
			Installs:     instsCopy,
		}
		if body.Kind != nil {
			updSW.Kind = *body.Kind
		}
		if body.LatestSource != nil {
			updSW.LatestSource = *body.LatestSource
		}
		if body.Changelog != nil {
			updSW.Changelog = *body.Changelog
		}
		newSW[si] = updSW
	}

	if !found {
		writeJSON(w, 404, map[string]string{"error": "install not found"})
		return
	}

	// Deep-copy machines map.
	newMachines := make(map[string]inventory.Machine, len(inv.Machines))
	for k, v := range inv.Machines {
		newMachines[k] = v
	}

	newInv := inventory.Inventory{Machines: newMachines, Software: newSW}
	if err := s.setInv(newInv, true); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// deleteSoftwareInstall handles DELETE /api/software/{name}/{machine}.
func (s *Server) deleteSoftwareInstall(w http.ResponseWriter, r *http.Request, name, machine string) {
	inv := s.getInv()

	newSW := make([]inventory.Software, 0, len(inv.Software))
	found := false
	for _, sw := range inv.Software {
		if sw.Name != name {
			newSW = append(newSW, sw)
			continue
		}
		var remaining []inventory.Install
		for _, ins := range sw.Installs {
			if ins.Machine == machine {
				found = true
				continue
			}
			remaining = append(remaining, ins)
		}
		if !found {
			newSW = append(newSW, sw)
			continue
		}
		// Only keep software if it still has installs.
		if len(remaining) > 0 {
			newSW = append(newSW, inventory.Software{
				Name:         sw.Name,
				Kind:         sw.Kind,
				LatestSource: sw.LatestSource,
				Changelog:    sw.Changelog,
				Installs:     remaining,
			})
		}
	}

	if !found {
		writeJSON(w, 404, map[string]string{"error": "install not found"})
		return
	}

	// Remove from store.
	_ = s.st.DeleteInstall(name, machine)

	// Deep-copy machines map.
	newMachines := make(map[string]inventory.Machine, len(inv.Machines))
	for k, v := range inv.Machines {
		newMachines[k] = v
	}

	newInv := inventory.Inventory{Machines: newMachines, Software: newSW}
	if err := s.setInv(newInv, true); err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	w.WriteHeader(204)
}
