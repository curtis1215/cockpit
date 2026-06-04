package agent

import (
	"encoding/json"
	"time"

	"github.com/curtis1215/cockpit/internal/collect"
	"github.com/curtis1215/cockpit/internal/dockerstat"
	"github.com/curtis1215/cockpit/internal/vmenum"
)

// MonitorOnce collects metrics and reports them to the server once.
func (a *Agent) MonitorOnce() {
	if a.col == nil {
		a.col = collect.New()
	}
	m, err := a.col.Collect()
	if err != nil {
		return
	}
	b, err := json.Marshal(m)
	if err != nil {
		return
	}
	var payload map[string]any
	if err := json.Unmarshal(b, &payload); err != nil {
		return
	}
	a.c().PostJSON("/api/agent/report-metrics", a.Token, payload, nil)
}

// ServicesOnce collects Docker/container services and reports to the server.
// Safe to call when Docker is not running — returns silently in that case.
func (a *Agent) ServicesOnce() {
	if a.docker == nil {
		a.docker = dockerstat.New()
	}
	svcs := a.docker.Collect()
	if len(svcs) == 0 {
		return
	}
	a.c().PostJSON("/api/agent/report-services", a.Token, svcs, nil)
}

// VMsOnce enumerates VMs (hypervisor hosts only) and reports to the server.
// Safe to call on non-hypervisor hosts — returns silently when vmenum returns nil.
func (a *Agent) VMsOnce() {
	if a.vmenum == nil {
		a.vmenum = vmenum.New()
	}
	vms, err := a.vmenum.Enumerate()
	if err != nil || len(vms) == 0 {
		return
	}
	a.c().PostJSON("/api/agent/report-vms", a.Token, vms, nil)
}

// monitorLoop runs the three monitor goroutines on their respective cadences:
//   - metrics: every 15 seconds
//   - services: every 60 seconds
//   - vms: every 5 minutes
func (a *Agent) monitorLoop() {
	// Warm-up calls — run once immediately.
	a.MonitorOnce()
	a.ServicesOnce()
	a.VMsOnce()

	t15 := time.NewTicker(15 * time.Second)
	t60 := time.NewTicker(60 * time.Second)
	t5m := time.NewTicker(5 * time.Minute)
	defer t15.Stop()
	defer t60.Stop()
	defer t5m.Stop()

	for {
		select {
		case <-t15.C:
			a.MonitorOnce()
		case <-t60.C:
			a.ServicesOnce()
		case <-t5m.C:
			a.VMsOnce()
		}
	}
}
