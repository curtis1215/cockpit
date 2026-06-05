package translate

import (
	"runtime"
	"strings"
	"testing"
)

func TestTranslate(t *testing.T) {
	tr := &Translator{Run: func(prompt string) (string, error) { return "中文摘要", nil }}
	if out := tr.Changelog("## 1.0\n- fix {bug}"); out != "中文摘要" {
		t.Fatalf("got %q", out)
	}
	if tr.Changelog("") != "" || tr.Changelog("   ") != "" {
		t.Fatal("empty → empty")
	}
	boom := &Translator{Run: func(string) (string, error) { return "", errFake }}
	if boom.Changelog("notes") != "" {
		t.Fatal("error → empty")
	}
}

var errFake = errBoom{}

type errBoom struct{}

func (errBoom) Error() string { return "boom" }

func TestNewWithCmdStdin(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires bash")
	}
	tr := NewWithCmd("cat") // bash -lc cat：原樣回吐 stdin
	out := tr.Changelog("hello-raw")
	if out == "" || !strings.Contains(out, "hello-raw") || !strings.Contains(out, "技術翻譯") {
		t.Fatalf("stdin path: %q", out)
	}
	boom := NewWithCmd("exit 3")
	if boom.Changelog("x") != "" {
		t.Fatal("cmd failure → empty")
	}
}
