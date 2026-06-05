package store

import "testing"

func TestNormalizeUUID(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"E4984D56-9F39-808E-A3EC-5A13A0A490F5", "e4984d569f39808ea3ec5a13a0a490f5"},
		{"564d98e4399f8e80-a3ec5a13a0a490f5", "564d98e4399f8e80a3ec5a13a0a490f5"},
		{"564d98e4-399f-8e80-a3ec-5a13a0a490f5", "564d98e4399f8e80a3ec5a13a0a490f5"},
		{"", ""},
		{"not-hex-at-all-xxx", ""},
		{"tooshort", ""},
	}
	for _, c := range cases {
		got := normalizeUUID(c.in)
		if got != c.want {
			t.Errorf("normalizeUUID(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestSmbiosSwap(t *testing.T) {
	// Real sample: vmx bios UUID → guest OS UUID
	vmxNorm := "564d98e4399f8e80a3ec5a13a0a490f5"
	guestNorm := "e4984d569f39808ea3ec5a13a0a490f5"

	swapped := smbiosSwap(vmxNorm)
	if swapped != guestNorm {
		t.Errorf("smbiosSwap(%q) = %q, want %q", vmxNorm, swapped, guestNorm)
	}

	// Applying swap twice should return the original (it's its own inverse).
	doubleSwap := smbiosSwap(swapped)
	if doubleSwap != vmxNorm {
		t.Errorf("double smbiosSwap: got %q, want %q", doubleSwap, vmxNorm)
	}
}

func TestUUIDMatch(t *testing.T) {
	// Real pair from the plan: vmx "564d98e4399f8e80-a3ec5a13a0a490f5" ↔ guest "E4984D56-9F39-808E-A3EC-5A13A0A490F5"
	vmxRaw := "564d98e4399f8e80-a3ec5a13a0a490f5"
	guestRaw := "E4984D56-9F39-808E-A3EC-5A13A0A490F5"

	if !uuidMatch(vmxRaw, guestRaw) {
		t.Errorf("real pair should match: vmx=%q guest=%q", vmxRaw, guestRaw)
	}
	// Commutative
	if !uuidMatch(guestRaw, vmxRaw) {
		t.Errorf("real pair (reversed) should match")
	}

	// Same-format (already normalized the same way) should match
	norm := "e4984d569f39808ea3ec5a13a0a490f5"
	if !uuidMatch(norm, norm) {
		t.Errorf("same UUID should match itself")
	}

	// Different UUIDs should not match
	other := "00000000000000000000000000000001"
	if uuidMatch(norm, other) {
		t.Errorf("different UUIDs should not match")
	}

	// Garbage / invalid → no match
	if uuidMatch("garbage", guestRaw) {
		t.Errorf("garbage a should not match")
	}
	if uuidMatch(guestRaw, "bad-input!") {
		t.Errorf("garbage b should not match")
	}
	if uuidMatch("", "") {
		t.Errorf("empty strings should not match")
	}
}
