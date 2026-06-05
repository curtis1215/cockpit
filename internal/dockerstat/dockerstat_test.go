package dockerstat

import "testing"

const psOut = `{"Names":"redis","State":"running","Ports":"0.0.0.0:6379->6379/tcp"}
{"Names":"caddy","State":"running","Ports":"0.0.0.0:80->80/tcp, 0.0.0.0:443->443/tcp"}
`
const statsOut = `{"Name":"redis","CPUPerc":"1.25%","MemPerc":"0.80%"}
{"Name":"caddy","CPUPerc":"0.10%","MemPerc":"2.00%"}
`

func TestParse(t *testing.T) {
	svcs := parse(psOut, statsOut)
	if len(svcs) != 2 {
		t.Fatalf("n=%d", len(svcs))
	}
	r := svcs[0]
	if r.Name != "redis" || r.Kind != "docker" || r.Status != "running" || r.Port != 6379 {
		t.Fatalf("redis: %+v", r)
	}
	if r.CPU == nil || *r.CPU != 1.25 || *r.Mem != 0.8 {
		t.Fatalf("redis stats: %+v", r)
	}
}

func TestCollectNoDocker(t *testing.T) {
	c := &Collector{Run: func(args ...string) (string, error) { return "", errNo{} }}
	if svcs := c.Collect(); svcs != nil {
		t.Fatalf("ps error → nil, got %+v", svcs)
	}
}

// TestCollectEmptyPS: docker ps succeeds but returns no containers.
// Collect must return a non-nil empty slice so the caller can POST and clear
// stale server rows.
func TestCollectEmptyPS(t *testing.T) {
	calls := 0
	c := &Collector{Run: func(args ...string) (string, error) {
		calls++
		return "", nil // ps succeeds with empty output; stats also empty
	}}
	svcs := c.Collect()
	if svcs == nil {
		t.Fatal("ps success with empty output → must return non-nil slice, got nil")
	}
	if len(svcs) != 0 {
		t.Fatalf("expected empty slice, got %+v", svcs)
	}
}

type errNo struct{}

func (errNo) Error() string { return "docker not found" }
