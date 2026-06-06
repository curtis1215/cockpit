package server

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/curtis1215/cockpit/internal/inventory"
	"github.com/curtis1215/cockpit/internal/store"
)

// looseName normalises a string to lowercase alphanumeric only (for loose name matching).
var looseNonAlnum = regexp.MustCompile(`[^a-z0-9]`)

func looseName(s string) string {
	return looseNonAlnum.ReplaceAllString(strings.ToLower(s), "")
}

func (s *Server) registerMonitorAPI() {
	s.mux.HandleFunc("/api/agent/report-metrics", s.reportMetrics)
	s.mux.HandleFunc("/api/agent/report-services", s.reportServices)
	s.mux.HandleFunc("/api/agent/report-vms", s.reportVMs)
	s.mux.HandleFunc("/api/services", s.apiServices)
	s.mux.HandleFunc("/api/vms", s.apiVMs)
	s.mux.HandleFunc("/api/vms/", s.apiVMSub)
	s.mux.HandleFunc("/api/systems/", s.apiSystemSub)
}

// agentSystem：統一識別——先試 systems token（enroll 取得），再試 inventory token（自動 find-or-create systems 列）。
func (s *Server) agentSystem(r *http.Request) (string, bool) {
	h := r.Header.Get("Authorization")
	const p = "Bearer "
	if len(h) <= len(p) || h[:len(p)] != p {
		return "", false
	}
	tok := h[len(p):]
	if sys, err := s.st.SystemByAgentToken(tok); err == nil {
		return sys.ID, true
	}
	if m := inventory.MachineForToken(s.inv, tok); m != "" {
		id, err := s.st.EnsureSystemForMachine(m)
		return id, err == nil
	}
	return "", false
}

type metricsBody struct {
	TS      int64    `json:"ts"`
	CPU     *float64 `json:"cpu"`
	Mem     *float64 `json:"mem"`
	Disk    *float64 `json:"disk"`
	GPU     *float64 `json:"gpu"`
	NetUp   *float64 `json:"net_up"`
	NetDown *float64 `json:"net_down"`
	Load    *float64 `json:"load"`
	Temp    *float64 `json:"temp"`
	Uptime  *float64 `json:"uptime"`
}

func (s *Server) reportMetrics(w http.ResponseWriter, r *http.Request) {
	sysID, ok := s.agentSystem(r)
	if !ok {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	var b metricsBody
	if err := json.NewDecoder(r.Body).Decode(&b); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}
	m := store.MetricRow{TS: b.TS, CPU: b.CPU, Mem: b.Mem, Disk: b.Disk, GPU: b.GPU,
		NetUp: b.NetUp, NetDown: b.NetDown, Load: b.Load, Temp: b.Temp, Uptime: b.Uptime}
	s.st.UpsertMetricsLatest(sysID, m)
	s.st.InsertMetric(sysID, "1m", m)
	s.st.TouchSystem(sysID)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) reportServices(w http.ResponseWriter, r *http.Request) {
	sysID, ok := s.agentSystem(r)
	if !ok {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	var body []struct {
		Name        string   `json:"name"`
		Kind        string   `json:"kind"`
		Status      string   `json:"status"`
		CPU         *float64 `json:"cpu"`
		Mem         *float64 `json:"mem"`
		Port        int      `json:"port"`
		SoftwareIDs []string `json:"software_ids"`
		Depends     []string `json:"depends"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}
	rows := make([]store.ServiceRow, 0, len(body))
	for _, x := range body {
		sw, _ := json.Marshal(x.SoftwareIDs)
		dep, _ := json.Marshal(x.Depends)
		swS, depS := string(sw), string(dep)
		if x.SoftwareIDs == nil {
			swS = ""
		}
		if x.Depends == nil {
			depS = ""
		}
		rows = append(rows, store.ServiceRow{Name: x.Name, Kind: x.Kind, Status: x.Status,
			CPU: x.CPU, Mem: x.Mem, Port: x.Port, SoftwareIDs: swS, Depends: depS})
	}
	s.st.ReplaceServices(sysID, rows)
	writeJSON(w, 200, map[string]int{"applied": len(rows)})
}

func (s *Server) reportVMs(w http.ResponseWriter, r *http.Request) {
	sysID, ok := s.agentSystem(r)
	if !ok {
		writeJSON(w, 401, map[string]string{"error": "unauthorized"})
		return
	}
	var body []struct {
		Name    string `json:"name"`
		UUID    string `json:"uuid"`
		VmxPath string `json:"vmx_path"`
		State   string `json:"state"`
		VCPU    int    `json:"vcpu"`
		RamMB   int    `json:"ram_mb"`
		GuestOS string `json:"guest_os"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, 400, map[string]string{"error": "bad json"})
		return
	}
	rows := make([]store.VMRow, 0, len(body))
	for _, x := range body {
		rows = append(rows, store.VMRow{Name: x.Name, UUID: x.UUID, VmxPath: x.VmxPath,
			State: x.State, VCPU: x.VCPU, RamMB: x.RamMB, GuestOS: x.GuestOS})
	}
	s.st.ReplaceVMs(sysID, rows)

	// Reconcile: match each VM to a registered system.
	// Priority: (1) UUID match (with SMBIOS endian swap), (2) exact label==vm.Name, (3) loose name contains.
	systems, _ := s.st.ListSystems()

	linked := 0
	for _, vm := range rows {
		// Skip self (the host reporting VMs cannot be its own guest).
		// Also skip VMs with no UUID and no name to match.

		var guestID string

		// Priority 1: UUID match
		if vm.UUID != "" {
			for _, sys := range systems {
				if sys.ID == sysID {
					continue // skip host
				}
				if sys.MachineUUID != "" && store.UUIDMatch(vm.UUID, sys.MachineUUID) {
					guestID = sys.ID
					break
				}
			}
		}

		// Priority 2: exact label == vm.Name
		if guestID == "" && vm.Name != "" {
			for _, sys := range systems {
				if sys.ID == sysID {
					continue
				}
				if sys.Label == vm.Name {
					guestID = sys.ID
					break
				}
			}
		}

		// Priority 3: loose name — normalise both, check containment, require len>=4
		if guestID == "" && vm.Name != "" {
			lvm := looseName(vm.Name)
			if len(lvm) >= 4 {
				for _, sys := range systems {
					if sys.ID == sysID {
						continue
					}
					lsys := looseName(sys.Label)
					if len(lsys) >= 4 && (strings.Contains(lvm, lsys) || strings.Contains(lsys, lvm)) {
						guestID = sys.ID
						break
					}
				}
			}
		}

		if guestID != "" {
			s.st.LinkVM(sysID, vm.UUID, guestID)
			linked++
		}
	}
	writeJSON(w, 200, map[string]int{"applied": len(rows), "linked": linked})
}

// apiVMSub handles /api/vms/{hostSystemID}/{uuid}/link and /api/vms/{hostSystemID}/{uuid} DELETE (unlink).
func (s *Server) apiVMSub(w http.ResponseWriter, r *http.Request) {
	// Path: /api/vms/{hostSystemID}/{uuid}/link  or  /api/vms/{hostSystemID}/{uuid}
	path := strings.TrimPrefix(r.URL.Path, "/api/vms/")
	parts := strings.SplitN(path, "/", 3)
	if len(parts) < 2 {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}
	hostID := parts[0]
	uuid := parts[1]
	sub := ""
	if len(parts) == 3 {
		sub = parts[2]
	}

	switch {
	case sub == "link" && r.Method == http.MethodPost:
		// POST /api/vms/{hostSystemID}/{uuid}/link  body: {"system_id":"..."}
		var body struct {
			SystemID string `json:"system_id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.SystemID == "" {
			writeJSON(w, 400, map[string]string{"error": "bad json or missing system_id"})
			return
		}
		// Verify host system and VM exist.
		if _, err := s.st.SystemByID(hostID); err != nil {
			writeJSON(w, 404, map[string]string{"error": "host system not found"})
			return
		}
		if _, err := s.st.VMByHostAndUUID(hostID, uuid); err != nil {
			writeJSON(w, 404, map[string]string{"error": "vm not found"})
			return
		}
		target, err := s.st.SystemByID(body.SystemID)
		if err != nil {
			writeJSON(w, 404, map[string]string{"error": "target system not found"})
			return
		}
		// 防呆：不可把 VM 連到宿主機本身（會把宿主標成自己的 VM、拓樸自我循環）。
		if body.SystemID == hostID {
			writeJSON(w, 400, map[string]string{"error": "不能連結到宿主機本身——此欄位指「VM 內運行的已註冊機器」"})
			return
		}
		// 防呆：目標若本身是其它 VM 的宿主，也不應被標成 VM。
		if vms, verr := s.st.ListVMs(); verr == nil {
			for _, v := range vms {
				if v.HostSystemID == body.SystemID {
					writeJSON(w, 400, map[string]string{"error": "目標機器本身是 VM 宿主，不能標記為 VM"})
					return
				}
			}
		}
		_ = target
		if err := s.st.LinkVM(hostID, uuid, body.SystemID); err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})

	case sub == "link" && r.Method == http.MethodDelete:
		// DELETE /api/vms/{hostSystemID}/{uuid}/link  → unlink
		if _, err := s.st.SystemByID(hostID); err != nil {
			writeJSON(w, 404, map[string]string{"error": "host system not found"})
			return
		}
		if _, err := s.st.VMByHostAndUUID(hostID, uuid); err != nil {
			writeJSON(w, 404, map[string]string{"error": "vm not found"})
			return
		}
		if err := s.st.UnlinkVM(hostID, uuid); err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		w.WriteHeader(204)

	default:
		writeJSON(w, 404, map[string]string{"error": "not found"})
	}
}

// ── Browser-side Monitor API ─────────────────────────────────────────────────

// fv2 converts *float64 to an any (nil if pointer is nil).
func fv2(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}

// liveStatus derives online/warn/offline/pending from SystemWithLatest data.
// Logic:
//   - pending: status=="pending" && no metrics ever reported (Latest.TS==0)
//   - offline: last_seen > 60s ago (parse failure → never offline)
//   - warn:    cpu>90 || mem>90 || disk>90 || temp>85
//   - else:    online
func liveStatus(x store.SystemWithLatest) string {
	if x.Status == "pending" && x.Latest.TS == 0 {
		return "pending"
	}
	// Parse last_seen: SQLite datetime('now') → "2006-01-02 15:04:05"
	if x.LastSeen != "" {
		t, err := time.Parse("2006-01-02 15:04:05", x.LastSeen)
		if err == nil {
			if time.Since(t) > 60*time.Second {
				return "offline"
			}
		} else {
			// also try RFC3339 (RegisterSystem uses RFC3339)
			t2, err2 := time.Parse(time.RFC3339, x.LastSeen)
			if err2 == nil && time.Since(t2) > 60*time.Second {
				return "offline"
			}
			// parse failure → never offline (treat as online)
		}
	}
	// warn check
	over := func(p *float64, threshold float64) bool {
		return p != nil && *p > threshold
	}
	if over(x.Latest.CPU, 90) || over(x.Latest.Mem, 90) || over(x.Latest.Disk, 90) || over(x.Latest.Temp, 85) {
		return "warn"
	}
	return "online"
}

// systemMap produces the enriched JSON object for a system.
func systemMap(x store.SystemWithLatest) map[string]any {
	st := liveStatus(x)
	return map[string]any{
		"id": x.ID, "label": x.Label, "role": x.Role, "os": x.OS, "arch": x.Arch,
		"kind": x.Kind, "host_id": x.HostID, "status": st,
		"agent_version": x.AgentVersion, "agent_status": x.AgentStatus,
		"last_seen": x.LastSeen,
		"cpu":       fv2(x.Latest.CPU), "mem": fv2(x.Latest.Mem), "disk": fv2(x.Latest.Disk),
		"gpu": fv2(x.Latest.GPU), "net_up": fv2(x.Latest.NetUp), "net_down": fv2(x.Latest.NetDown),
		"load": fv2(x.Latest.Load), "temp": fv2(x.Latest.Temp), "uptime": fv2(x.Latest.Uptime),
		"spark": x.Spark,
	}
}

// apiSystemsEnriched handles GET (list) and POST (create pending) for /api/systems.
func (s *Server) apiSystemsEnriched(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		rows, err := s.st.SystemsWithLatest()
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		out := []map[string]any{}
		for _, x := range rows {
			out = append(out, systemMap(x))
		}
		writeJSON(w, 200, out)
	case http.MethodPost:
		s.createSystem(w, r)
	default:
		w.WriteHeader(405)
	}
}

// rangeTypeDef maps a range query string to a metrics type and window.
type rangeTypeDef struct {
	Typ       string
	WindowSec int64
}

var rangeMap = map[string]rangeTypeDef{
	"1h":  {"1m", 3600},
	"12h": {"10m", 12 * 3600},
	"24h": {"15m", 24 * 3600},
	"7d":  {"60m", 7 * 24 * 3600},
	"30d": {"480m", 30 * 24 * 3600},
}

// apiSystemSub handles /api/systems/{id}[/subpath] for GET, PATCH, DELETE, and subpaths.
func (s *Server) apiSystemSub(w http.ResponseWriter, r *http.Request) {
	// strip "/api/systems/" prefix
	path := strings.TrimPrefix(r.URL.Path, "/api/systems/")
	parts := strings.SplitN(path, "/", 2)
	id := parts[0]
	if id == "" {
		writeJSON(w, 404, map[string]string{"error": "not found"})
		return
	}

	sub := ""
	if len(parts) == 2 {
		sub = parts[1]
	}

	// Handle subpath actions before loading system (some don't need it)
	if sub == "enroll-token" {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		s.regenEnrollToken(w, r, id)
		return
	}

	if sub == "upgrade-agent" {
		if r.Method != http.MethodPost {
			w.WriteHeader(405)
			return
		}
		sys, err := s.st.SystemByID(id)
		if err != nil {
			writeJSON(w, 404, map[string]string{"error": "system not found"})
			return
		}
		s.st.SetUpgradeRequested(sys.Label)
		writeJSON(w, 200, map[string]bool{"ok": true})
		return
	}

	// DELETE: cascade remove
	if r.Method == http.MethodDelete && sub == "" {
		if err := s.st.DeleteSystemCascade(id); err != nil {
			if err == store.ErrNotFound {
				writeJSON(w, 404, map[string]string{"error": "system not found"})
				return
			}
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		w.WriteHeader(204)
		return
	}

	// PATCH: update label/role
	if r.Method == http.MethodPatch && sub == "" {
		s.patchSystem(w, r, id)
		return
	}

	// Find system for GET operations
	rows, err := s.st.SystemsWithLatest()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	var found *store.SystemWithLatest
	for i := range rows {
		if rows[i].ID == id {
			found = &rows[i]
			break
		}
	}
	if found == nil {
		writeJSON(w, 404, map[string]string{"error": "system not found"})
		return
	}

	if sub == "" {
		writeJSON(w, 200, systemMap(*found))
		return
	}

	if sub == "metrics" {
		rng := r.URL.Query().Get("range")
		rt, ok := rangeMap[rng]
		if !ok {
			rt = rangeMap["24h"] // default
		}
		// Use since=0 to return all stored data of the correct granularity type.
		// The range parameter selects the metric TYPE (granularity), not a server-side
		// time-window filter; clients use the "t" field to display the desired window.
		pts, err := s.st.QueryMetrics(id, rt.Typ, 0)
		if err != nil {
			writeJSON(w, 500, map[string]string{"error": err.Error()})
			return
		}
		out := []map[string]any{}
		for _, m := range pts {
			out = append(out, map[string]any{
				"t": m.TS, "cpu": fv2(m.CPU), "mem": fv2(m.Mem), "disk": fv2(m.Disk),
				"gpu": fv2(m.GPU), "net_up": fv2(m.NetUp), "net_down": fv2(m.NetDown),
				"load": fv2(m.Load), "temp": fv2(m.Temp),
			})
		}
		writeJSON(w, 200, out)
		return
	}

	writeJSON(w, 404, map[string]string{"error": "not found"})
}

// apiServices returns all services.
func (s *Server) apiServices(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.ListServices()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	out := []map[string]any{}
	for _, x := range rows {
		var swOut, depOut any
		if x.SoftwareIDs != "" {
			var sw []string
			json.Unmarshal([]byte(x.SoftwareIDs), &sw)
			swOut = sw
		}
		if x.Depends != "" {
			var dep []string
			json.Unmarshal([]byte(x.Depends), &dep)
			depOut = dep
		}
		out = append(out, map[string]any{
			"system_id": x.SystemID, "name": x.Name, "kind": x.Kind,
			"status": x.Status, "cpu": fv2(x.CPU), "mem": fv2(x.Mem), "port": x.Port,
			"software_ids": swOut, "depends": depOut,
		})
	}
	writeJSON(w, 200, out)
}

// apiVMs returns all VMs.
func (s *Server) apiVMs(w http.ResponseWriter, r *http.Request) {
	rows, err := s.st.ListVMs()
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	out := []map[string]any{}
	for _, x := range rows {
		out = append(out, map[string]any{
			"host_system_id": x.HostSystemID, "name": x.Name, "uuid": x.UUID,
			"vmx_path": x.VmxPath, "state": x.State, "vcpu": x.VCPU, "ram_mb": x.RamMB,
			"guest_os": x.GuestOS, "linked_system_id": nilIfEmpty(x.LinkedSystemID),
		})
	}
	writeJSON(w, 200, out)
}
