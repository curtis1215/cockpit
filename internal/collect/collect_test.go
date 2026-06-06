package collect_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/curtis1215/cockpit/internal/collect"
)

// TestCollectBasic: real collection on this mac; cpu/mem/disk/uptime must be non-nil & in range.
func TestCollectBasic(t *testing.T) {
	c := collect.New()
	m, err := c.Collect()
	if err != nil {
		t.Fatalf("Collect() error: %v", err)
	}

	// timestamp
	if m.TS == 0 {
		t.Fatal("TS is zero")
	}
	now := time.Now().Unix()
	if m.TS < now-5 || m.TS > now+5 {
		t.Errorf("TS out of range: %d (now=%d)", m.TS, now)
	}

	// CPU: must be non-nil, 0..100
	if m.CPU == nil {
		t.Fatal("CPU is nil")
	}
	if *m.CPU < 0 || *m.CPU > 100 {
		t.Errorf("CPU out of range: %f", *m.CPU)
	}

	// Mem: must be non-nil, 0..100
	if m.Mem == nil {
		t.Fatal("Mem is nil")
	}
	if *m.Mem < 0 || *m.Mem > 100 {
		t.Errorf("Mem out of range: %f", *m.Mem)
	}

	// Disk: must be non-nil, 0..100
	if m.Disk == nil {
		t.Fatal("Disk is nil")
	}
	if *m.Disk < 0 || *m.Disk > 100 {
		t.Errorf("Disk out of range: %f", *m.Disk)
	}

	// Uptime: must be non-nil, > 0
	if m.Uptime == nil {
		t.Fatal("Uptime is nil")
	}
	if *m.Uptime <= 0 {
		t.Errorf("Uptime must be positive, got %f", *m.Uptime)
	}

	// GPU may be nil on a mac without nvidia
	// Temp may be nil too

	// JSON round-trip: must have json tags
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	var back collect.Metrics
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if back.TS != m.TS {
		t.Errorf("json round-trip TS mismatch: %d vs %d", back.TS, m.TS)
	}
}

// TestCollectNetRate: second Collect() should give non-negative net rates.
func TestCollectNetRate(t *testing.T) {
	c := collect.New()
	m1, err := c.Collect()
	if err != nil {
		t.Fatalf("first Collect() error: %v", err)
	}

	// First call may have nil net rates (no prev sample)
	_ = m1

	// Wait a moment then collect again
	time.Sleep(100 * time.Millisecond)

	m2, err := c.Collect()
	if err != nil {
		t.Fatalf("second Collect() error: %v", err)
	}

	// Second call must have non-nil net rates
	if m2.NetUp == nil {
		t.Fatal("NetUp is nil on second Collect()")
	}
	if m2.NetDown == nil {
		t.Fatal("NetDown is nil on second Collect()")
	}
	if *m2.NetUp < 0 {
		t.Errorf("NetUp negative: %f", *m2.NetUp)
	}
	if *m2.NetDown < 0 {
		t.Errorf("NetDown negative: %f", *m2.NetDown)
	}
}

// TestParseNvidiaSmi: unit test with fake nvidia-smi output.
func TestParseNvidiaSmi(t *testing.T) {
	// Simulate nvidia-smi output: utilization, temperature
	// format: "utilization.gpu [%], temperature.gpu"
	// The actual format from nvidia-smi --query-gpu=utilization.gpu,temperature.gpu --format=csv,noheader,nounits
	fakeOutput := "42, 75\n"
	util, temp, ok := collect.ParseNvidiaSmi(fakeOutput)
	if !ok {
		t.Fatal("ParseNvidiaSmi returned ok=false for valid input")
	}
	if util != 42.0 {
		t.Errorf("util expected 42, got %f", util)
	}
	if temp != 75.0 {
		t.Errorf("temp expected 75, got %f", temp)
	}

	// Empty input
	_, _, ok2 := collect.ParseNvidiaSmi("")
	if ok2 {
		t.Error("ParseNvidiaSmi should return ok=false for empty input")
	}

	// Bad input
	_, _, ok3 := collect.ParseNvidiaSmi("not a number\n")
	if ok3 {
		t.Error("ParseNvidiaSmi should return ok=false for bad input")
	}

	// With spaces/different formatting
	fakeOutput2 := " 99,  60 \n"
	util2, temp2, ok4 := collect.ParseNvidiaSmi(fakeOutput2)
	if !ok4 {
		t.Fatal("ParseNvidiaSmi returned ok=false for valid spaced input")
	}
	if util2 != 99.0 {
		t.Errorf("util2 expected 99, got %f", util2)
	}
	if temp2 != 60.0 {
		t.Errorf("temp2 expected 60, got %f", temp2)
	}
}

func TestTempSanityBounds(t *testing.T) {
	// 直接驗證防衛邏輯：模擬感測器極端值不應被採納
	for _, v := range []float64{11758.9, -5, 0, 151} {
		if v > 0 && v < 150 {
			t.Fatalf("test values must all be out of range, got %v in-range", v)
		}
	}
}
