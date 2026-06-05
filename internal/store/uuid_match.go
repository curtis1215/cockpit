package store

// normalizeUUID strips dashes/spaces, lowercases, and returns the 32-hex string.
// Returns "" if the result is not exactly 32 hex characters.
func normalizeUUID(s string) string {
	out := make([]byte, 0, 32)
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '-' || c == ' ':
			// skip
		case c >= '0' && c <= '9':
			out = append(out, c)
		case c >= 'a' && c <= 'f':
			out = append(out, c)
		case c >= 'A' && c <= 'F':
			out = append(out, c+'a'-'A')
		default:
			return "" // non-hex character → invalid
		}
	}
	if len(out) != 32 {
		return ""
	}
	return string(out)
}

// reverseHexBytes reverses the byte order of a hex string slice [start, end).
// Each "byte" is 2 hex characters. start/end are byte indices (not char indices).
func reverseHexBytes(s string, startByte, endByte int) string {
	b := []byte(s)
	// Work in 2-char units (hex bytes).
	for lo, hi := startByte*2, endByte*2-2; lo < hi; lo, hi = lo+2, hi-2 {
		b[lo], b[hi] = b[hi], b[lo]
		b[lo+1], b[hi+1] = b[hi+1], b[lo+1]
	}
	return string(b)
}

// smbiosSwap applies the SMBIOS little-endian byte swap to a normalised 32-hex UUID.
// The first three groups of a SMBIOS UUID are stored in little-endian byte order on the
// host but reported as big-endian text in the guest OS. We reverse:
//   - bytes 0-3  (group 1: 4 bytes = chars 0-7)
//   - bytes 4-5  (group 2: 2 bytes = chars 8-11)
//   - bytes 6-7  (group 3: 2 bytes = chars 12-15)
//
// Groups 4-5 (bytes 8-15) are already in network byte order — leave untouched.
//
// Real pair test:
//
//	vmx (raw bios): 564d98e4-399f-8e80-a3ec-5a13a0a490f5
//	guest (OS):     e4984d56-9f39-808e-a3ec-5a13a0a490f5
//
// normalizeUUID("564d98e4399f8e80a3ec5a13a0a490f5") → "564d98e4399f8e80a3ec5a13a0a490f5"
// smbiosSwap → reverse bytes[0:4] → e4984d56, reverse bytes[4:6] → 9f39, reverse bytes[6:8] → 808e
// → "e4984d569f39808ea3ec5a13a0a490f5" ✓
func smbiosSwap(hex32 string) string {
	if len(hex32) != 32 {
		return hex32
	}
	s := reverseHexBytes(hex32, 0, 4)  // group 1: bytes 0-3
	s = reverseHexBytes(s, 4, 6)       // group 2: bytes 4-5
	s = reverseHexBytes(s, 6, 8)       // group 3: bytes 6-7
	return s
}

// uuidMatch returns true if a and b refer to the same physical UUID, accounting for
// the SMBIOS little-endian encoding difference between VMX bios UUID and guest OS UUID.
// Both "direct equal" and "one is the smbios-swap of the other" count as a match.
// Returns false if either normalises to "".
func uuidMatch(a, b string) bool {
	na := normalizeUUID(a)
	nb := normalizeUUID(b)
	if na == "" || nb == "" {
		return false
	}
	return na == nb || smbiosSwap(na) == nb || na == smbiosSwap(nb)
}

// UUIDMatch is the exported version for use from other packages (e.g. server).
func UUIDMatch(a, b string) bool { return uuidMatch(a, b) }
