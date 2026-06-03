package version

import "testing"

func TestParse(t *testing.T) {
	cases := map[string]string{"2.1.101": "2.1.101", "claude 2.1.98 (x)": "2.1.98", "v0.9.0": "0.9.0", "no ver": ""}
	for in, want := range cases {
		if got := Parse(in, ""); got != want {
			t.Fatalf("Parse(%q)=%q want %q", in, got, want)
		}
	}
	if got := Parse("image: multica:0.8.2", `multica:([0-9.]+)`); got != "0.8.2" {
		t.Fatalf("custom regex got %q", got)
	}
	if got := Parse("app:1.2.3", `app:[0-9.]+`); got != "app:1.2.3" { // 無 group → 整段
		t.Fatalf("no-group got %q", got)
	}
	if got := Parse("x", `([0-9`); got != "" { // 非法 regex
		t.Fatalf("bad regex got %q", got)
	}
}

func TestCompare(t *testing.T) {
	check := func(cur, lat, wantS string, wantN int) {
		s, n := Compare(cur, lat)
		if s != wantS || n != wantN {
			t.Fatalf("Compare(%q,%q)=(%q,%d) want (%q,%d)", cur, lat, s, n, wantS, wantN)
		}
	}
	check("2.1.98", "2.1.101", "behind", 3)
	check("2.1.101", "2.1.101", "up_to_date", 0)
	check("1.0.0", "0.9.0", "up_to_date", 0)
	check("", "2.1.101", "unknown", 0)
}
