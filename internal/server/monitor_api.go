package server

import (
	"encoding/json"
	"net/http"

	"github.com/curtis1215/cockpit/internal/inventory"
	"github.com/curtis1215/cockpit/internal/store"
)

func (s *Server) registerMonitorAPI() {
	s.mux.HandleFunc("/api/agent/report-metrics", s.reportMetrics)
	s.mux.HandleFunc("/api/agent/report-services", s.reportServices)
	s.mux.HandleFunc("/api/agent/report-vms", s.reportVMs)
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
	// 對帳：label==vm name 的 system → LinkVM
	systems, _ := s.st.ListSystems()
	byLabel := map[string]string{}
	for _, x := range systems {
		byLabel[x.Label] = x.ID
	}
	linked := 0
	for _, vm := range rows {
		if gid, ok := byLabel[vm.Name]; ok && gid != sysID {
			s.st.LinkVM(sysID, vm.UUID, gid)
			linked++
		}
	}
	writeJSON(w, 200, map[string]int{"applied": len(rows), "linked": linked})
}
