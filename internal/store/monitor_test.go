package store

import (
	"path/filepath"
	"testing"
)

func mOpen(t *testing.T) *Store {
	s, err := Open(filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func f(v float64) *float64 { return &v }

func TestEnsureSystemForMachine(t *testing.T) {
	s := mOpen(t)
	id1, err := s.EnsureSystemForMachine("mac")
	if err != nil || id1 == "" {
		t.Fatalf("ensure: %v %q", err, id1)
	}
	id2, _ := s.EnsureSystemForMachine("mac") // 第二次回同一筆
	if id2 != id1 {
		t.Fatalf("idempotent: %q vs %q", id1, id2)
	}
	list, _ := s.ListSystems()
	if len(list) != 1 || list[0].Label != "mac" {
		t.Fatalf("systems: %+v", list)
	}
}

func TestMetricsLatestAndSpark(t *testing.T) {
	s := mOpen(t)
	id, _ := s.EnsureSystemForMachine("mac")
	for i := 0; i < 30; i++ {
		s.UpsertMetricsLatest(id, MetricRow{TS: int64(1000 + i), CPU: f(float64(i)), Mem: f(50), Disk: f(60), NetUp: f(1), NetDown: f(2), Load: f(0.5), Uptime: f(99)})
	}
	rows, _ := s.SystemsWithLatest()
	if len(rows) != 1 || *rows[0].Latest.CPU != 29 {
		t.Fatalf("latest: %+v", rows)
	}
	if n := len(rows[0].Spark); n != 24 { // cap 24
		t.Fatalf("spark len: %d", n)
	}
	if rows[0].Spark[23] != 29 {
		t.Fatalf("spark tail: %v", rows[0].Spark)
	}
}

func TestMetricsInsertQuery(t *testing.T) {
	s := mOpen(t)
	id, _ := s.EnsureSystemForMachine("mac")
	for i := 0; i < 5; i++ {
		s.InsertMetric(id, "1m", MetricRow{TS: int64(60 * i), CPU: f(float64(10 + i)), Mem: f(50)})
	}
	pts, _ := s.QueryMetrics(id, "1m", 0)
	if len(pts) != 5 || *pts[4].CPU != 14 {
		t.Fatalf("query: %d %+v", len(pts), pts)
	}
	pts2, _ := s.QueryMetrics(id, "1m", 120) // since
	if len(pts2) != 3 {
		t.Fatalf("since: %d", len(pts2))
	}
}

func TestServicesReplace(t *testing.T) {
	s := mOpen(t)
	id, _ := s.EnsureSystemForMachine("mac")
	s.ReplaceServices(id, []ServiceRow{{Name: "redis", Kind: "docker", Status: "running", CPU: f(1), Mem: f(2), Port: 6379}})
	s.ReplaceServices(id, []ServiceRow{{Name: "caddy", Kind: "docker", Status: "running"}})
	rows, _ := s.ListServices()
	if len(rows) != 1 || rows[0].Name != "caddy" {
		t.Fatalf("replace: %+v", rows)
	}
}

func TestVMsReplaceAndLink(t *testing.T) {
	s := mOpen(t)
	host, _ := s.EnsureSystemForMachine("minihost")
	s.ReplaceVMs(host, []VMRow{{Name: "ubuntu-vm", UUID: "u-1", VmxPath: "/x.vmx", State: "running", VCPU: 4, RamMB: 4096, GuestOS: "ubuntu"}})
	vms, _ := s.ListVMs()
	if len(vms) != 1 || vms[0].HostSystemID != host {
		t.Fatalf("vms: %+v", vms)
	}
	guest, _ := s.EnsureSystemForMachine("ubuntu-vm")
	s.LinkVM(host, "u-1", guest)
	vms2, _ := s.ListVMs()
	if vms2[0].LinkedSystemID != guest {
		t.Fatalf("link: %+v", vms2)
	}
	sys, _ := s.ListSystems()
	for _, x := range sys {
		if x.ID == guest && (x.Kind != "vm" || x.HostID != host) {
			t.Fatalf("guest system not linked: %+v", x)
		}
	}
}

func TestUpgradeFlagOneShot(t *testing.T) {
	s := mOpen(t)
	// Before setting: TakeUpgradeRequested returns false.
	if s.TakeUpgradeRequested("mac") {
		t.Fatal("expected false before set")
	}
	// Set flag.
	if err := s.SetUpgradeRequested("mac"); err != nil {
		t.Fatalf("set: %v", err)
	}
	// First take: returns true and clears the flag.
	if !s.TakeUpgradeRequested("mac") {
		t.Fatal("expected true after set")
	}
	// Second take: flag is gone — must return false (one-shot).
	if s.TakeUpgradeRequested("mac") {
		t.Fatal("expected false on second take (one-shot)")
	}
	// Idempotent SET then SET: still only one take returns true.
	s.SetUpgradeRequested("mac")
	s.SetUpgradeRequested("mac")
	if !s.TakeUpgradeRequested("mac") {
		t.Fatal("expected true after double-set")
	}
	if s.TakeUpgradeRequested("mac") {
		t.Fatal("expected false after second take (double-set)")
	}
}

func TestDownsampleAndPrune(t *testing.T) {
	s := mOpen(t)
	id, _ := s.EnsureSystemForMachine("mac")
	// 寫 20 筆 1m（ts 0..1140，每 60s），cpu = 10..29
	for i := 0; i < 20; i++ {
		s.InsertMetric(id, "1m", MetricRow{TS: int64(60 * i), CPU: f(float64(10 + i)), Mem: f(50)})
	}
	// 聚合（now=1200）：10m 桶 = [0,600) 與 [600,1200) → cpu 平均 14.5 / 24.5
	if err := s.Downsample(1200); err != nil {
		t.Fatal(err)
	}
	pts, _ := s.QueryMetrics(id, "10m", 0)
	if len(pts) != 2 {
		t.Fatalf("10m buckets: %d", len(pts))
	}
	if *pts[0].CPU != 14.5 || *pts[1].CPU != 24.5 {
		t.Fatalf("10m avg: %v %v", *pts[0].CPU, *pts[1].CPU)
	}
	// 15m 桶 = [0,900) 平均 17, [900,1200) 平均 26.5
	pts15, _ := s.QueryMetrics(id, "15m", 0)
	if len(pts15) != 2 || *pts15[0].CPU != 17 {
		t.Fatalf("15m: %+v", pts15)
	}
	// Prune：now 拉到 1m 保留期(2h)之後 → 1m 全清，10m 還在
	// max 1m ts=1140，1m 保留 7200s：now > 1140+7200=8340 → 1m 全清
	// 10m 保留 50400s：now-50400=8341-50400<0 → 10m 不刪
	if err := s.PruneMetrics(1140 + 7200 + 1); err == nil {
		one, _ := s.QueryMetrics(id, "1m", 0)
		ten, _ := s.QueryMetrics(id, "10m", 0)
		if len(one) != 0 || len(ten) == 0 {
			t.Fatalf("prune: 1m=%d 10m=%d", len(one), len(ten))
		}
	} else {
		t.Fatal(err)
	}
}
