package reporter

import "testing"

func TestParseVersionSemver(t *testing.T) {
	if v := ParseVersion("claude 2.1.98 (Claude Code)", ""); v != "2.1.98" {
		t.Fatalf("got %q", v)
	}
	if v := ParseVersion("v0.9.0", ""); v != "0.9.0" {
		t.Fatalf("got %q", v)
	}
}

func TestParseVersionCustomRegex(t *testing.T) {
	if v := ParseVersion("image: multica:0.8.2", `multica:([0-9.]+)`); v != "0.8.2" {
		t.Fatalf("got %q", v)
	}
	// 無 capture group → 回整段 match
	if v := ParseVersion("app:1.2.3", `app:[0-9.]+`); v != "app:1.2.3" {
		t.Fatalf("got %q", v)
	}
	// 非法 regex → 空字串
	if v := ParseVersion("whatever", `([0-9`); v != "" {
		t.Fatalf("got %q", v)
	}
}

func TestParseNone(t *testing.T) {
	if v := ParseVersion("no version here", ""); v != "" {
		t.Fatalf("got %q", v)
	}
}
