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

func TestChangelogResult(t *testing.T) {
	tr := &Translator{Run: func(prompt string) (string, error) { return "  中文摘要\n", nil }}
	out, err := tr.ChangelogResult("## 1.0\n- fix")
	if err != nil || out != "中文摘要" {
		t.Fatalf("got %q err=%v", out, err)
	}
	out, err = tr.ChangelogResult("")
	if err != nil || out != "" {
		t.Fatalf("empty raw: %q err=%v", out, err)
	}
	boom := &Translator{Run: func(string) (string, error) { return "", errFake }}
	if _, err := boom.ChangelogResult("notes"); err == nil {
		t.Fatal("error should propagate")
	}
	empty := &Translator{Run: func(string) (string, error) { return "  \n", nil }}
	if _, err := empty.ChangelogResult("notes"); err == nil || !strings.Contains(strings.ToLower(err.Error()), "empty") {
		t.Fatalf("empty content: %v", err)
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
