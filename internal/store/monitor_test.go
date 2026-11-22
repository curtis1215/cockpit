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

func TestDownsampleSkipsPartiallyPrunedBucket(t *testing.T) {
	s := mOpen(t)
	id, _ := s.EnsureSystemForMachine("mac")
	// 15m 桶 [0,900)：完整 15 筆 1m（ts 0..840，cpu 10..24，平均 17）
	for i := 0; i < 15; i++ {
		s.InsertMetric(id, "1m", MetricRow{TS: int64(60 * i), CPU: f(float64(10 + i)), Mem: f(50)})
	}
	if err := s.Downsample(1200); err != nil {
		t.Fatal(err)
	}
	before, _ := s.QueryMetrics(id, "15m", 0)
	if len(before) != 1 || *before[0].CPU != 17 {
		t.Fatalf("15m before prune: %+v", before)
	}
	// 真實 prune：now=7650 → 1m cutoff=450，刪掉該桶前半段（ts < 450）並記下水位 450
	if err := s.PruneMetrics(7650); err != nil {
		t.Fatal(err)
	}
	// now=9000：水位 450 向上取整到 900 邊界得 lb=900，該桶 0 < 900 不再重算
	if err := s.Downsample(9000); err != nil {
		t.Fatal(err)
	}
	after, _ := s.QueryMetrics(id, "15m", 0)
	if len(after) != 1 || *after[0].CPU != 17 {
		t.Fatalf("15m recomputed from pruned sources: %+v", after)
	}
}

func TestDownsampleCatchesUpAfterOutageLongerThanRetention(t *testing.T) {
	s := mOpen(t)
	id, _ := s.EnsureSystemForMachine("mac")
	// 停機期間 prune 沒跑：15m 桶 [0,900) 的 15 筆 1m（cpu 10..24，平均 17）仍在 DB，從未降採樣。
	for i := 0; i < 15; i++ {
		s.InsertMetric(id, "1m", MetricRow{TS: int64(60 * i), CPU: f(float64(10 + i)), Mem: f(50)})
	}
	// now=9000：最新一筆 1m（ts=840）已超過 1m 保留期 7200s，舊實作的 lb 會把這整桶跳過。
	if err := s.Downsample(9000); err != nil {
		t.Fatal(err)
	}
	pts, _ := s.QueryMetrics(id, "15m", 0)
	if len(pts) != 1 || *pts[0].CPU != 17 {
		t.Fatalf("15m after outage: %+v", pts)
	}
}

func TestDownsampleAggregatesFirstBucketWhenNotOnBoundary(t *testing.T) {
	s := mOpen(t)
	id, _ := s.EnsureSystemForMachine("mac")
	// 從未 prune，第一筆 ts=60 不在 900 邊界上：[0,900) 桶仍須聚合（ts 60..840，cpu 11..24，平均 17.5）
	for i := 1; i < 15; i++ {
		s.InsertMetric(id, "1m", MetricRow{TS: int64(60 * i), CPU: f(float64(10 + i)), Mem: f(50)})
	}
	if err := s.Downsample(1200); err != nil {
		t.Fatal(err)
	}
	pts, _ := s.QueryMetrics(id, "15m", 0)
	if len(pts) != 1 || *pts[0].CPU != 17.5 {
		t.Fatalf("first bucket not aggregated: %+v", pts)
	}
}

func TestDownsampleKeepsPartiallyPrunedBucketAfterLateSample(t *testing.T) {
	s := mOpen(t)
	id, _ := s.EnsureSystemForMachine("mac")
	for i := 0; i < 15; i++ {
		s.InsertMetric(id, "1m", MetricRow{TS: int64(60 * i), CPU: f(float64(10 + i)), Mem: f(50)})
	}
	if err := s.Downsample(1200); err != nil {
		t.Fatal(err)
	}
	// 真實 prune 到水位 450（刪 ts < 450），再補一筆比水位更舊的遲到樣本 ts=0
	if err := s.PruneMetrics(7650); err != nil {
		t.Fatal(err)
	}
	s.InsertMetric(id, "1m", MetricRow{TS: 0, CPU: f(100), Mem: f(50)})
	if err := s.Downsample(9000); err != nil {
		t.Fatal(err)
	}
	// 舊實作 MIN(ts)=0 會把部分 prune 的 [0,900) 桶用殘缺資料重算；水位 450 須保住原值 17
	pts, _ := s.QueryMetrics(id, "15m", 0)
	if len(pts) != 1 || *pts[0].CPU != 17 {
		t.Fatalf("partially pruned bucket recomputed: %+v", pts)
	}
}

func TestPruneWatermarkNeverMovesBack(t *testing.T) {
	s := mOpen(t)
	if err := s.PruneMetrics(7650); err != nil {
		t.Fatal(err)
	}
	if err := s.PruneMetrics(7000); err != nil {
		t.Fatal(err)
	}
	if got := s.GetSetting("metrics_prune_wm:1m"); got != "450" {
		t.Fatalf("watermark moved back: %q", got)
	}
}

func TestPruneRollsBackDeleteWhenWatermarkWriteFails(t *testing.T) {
	s := mOpen(t)
	id, _ := s.EnsureSystemForMachine("mac")
	for i := 0; i < 15; i++ {
		s.InsertMetric(id, "1m", MetricRow{TS: int64(60 * i), CPU: f(float64(10 + i)), Mem: f(50)})
	}
	// 讓水位寫入失敗：寫 metrics_prune_wm:* 一律 ABORT
	if _, err := s.db.Exec(`CREATE TRIGGER fail_wm BEFORE INSERT ON settings
		WHEN NEW.key LIKE 'metrics_prune_wm:%'
		BEGIN SELECT RAISE(ABORT, 'watermark write failed'); END`); err != nil {
		t.Fatal(err)
	}
	// 本來會刪 ts < 450 的列；水位寫失敗時刪除也必須回滾，否則下次 Downsample 會用殘缺來源重算
	if err := s.PruneMetrics(7650); err == nil {
		t.Fatal("PruneMetrics: want error when watermark write fails")
	}
	pts, _ := s.QueryMetrics(id, "1m", 0)
	if len(pts) != 15 {
		t.Fatalf("1m rows after failed prune: %d, want 15 (delete must roll back with watermark)", len(pts))
	}
}

func TestDownsampleDoesNotRecomputePrePruneBucketWithoutWatermark(t *testing.T) {
	s := mOpen(t)
	id, _ := s.EnsureSystemForMachine("mac")
	for i := 0; i < 15; i++ {
		s.InsertMetric(id, "1m", MetricRow{TS: int64(60 * i), CPU: f(float64(10 + i)), Mem: f(50)})
	}
	if err := s.Downsample(1200); err != nil {
		t.Fatal(err)
	}
	// 模擬升級前已 prune：刪掉 [0,900) 桶的前半段（ts < 450），但沒有寫入水位。
	if _, err := s.db.Exec(`DELETE FROM metrics WHERE type='1m' AND ts < 450`); err != nil {
		t.Fatal(err)
	}
	if wm := s.GetSetting("metrics_prune_wm:1m"); wm != "" {
		t.Fatalf("precondition: unexpected watermark %q", wm)
	}
	if err := s.Downsample(9000); err != nil {
		t.Fatal(err)
	}
	// 殘缺來源（ts 480..840，cpu 18..24，平均 21）不得覆蓋原本完整聚合值 17
	pts, _ := s.QueryMetrics(id, "15m", 0)
	if len(pts) != 1 || *pts[0].CPU != 17 {
		t.Fatalf("pre-prune bucket recomputed without watermark: %+v", pts)
	}
}
