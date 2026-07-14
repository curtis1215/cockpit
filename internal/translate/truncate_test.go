package translate

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestTruncateForTranslate(t *testing.T) {
	raw := strings.Repeat("a", MaxTranslateRawBytes+100)
	out := TruncateForTranslate(raw)
	if len(out) <= MaxTranslateRawBytes {
		t.Fatalf("expected prefix+suffix longer note, len=%d", len(out))
	}
	if !strings.Contains(out, "truncated for translation") {
		t.Fatalf("%q", out[:80])
	}
	// rune 安全：含多位元組字元不 panic、不破壞 UTF-8
	raw2 := strings.Repeat("中", 5000)
	out2 := TruncateForTranslate(raw2)
	if !utf8.ValidString(out2) {
		t.Fatal("invalid utf8")
	}
}

func TestTruncateForTranslateNoOp(t *testing.T) {
	raw := "short"
	if out := TruncateForTranslate(raw); out != raw {
		t.Fatalf("got %q", out)
	}
	exact := strings.Repeat("b", MaxTranslateRawBytes)
	if out := TruncateForTranslate(exact); out != exact {
		t.Fatalf("exact boundary should pass through, len=%d", len(out))
	}
}
