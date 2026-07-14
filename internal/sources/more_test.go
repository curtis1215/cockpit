package sources

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/curtis1215/cockpit/internal/inventory"
)

func TestPypi(t *testing.T) {
	s, hc := srv(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"info":{"version":"1.4.2"}}`)) })
	defer s.Close()
	res, err := fetchPypi(inventory.Software{Name: "x", LatestSource: "pypi:p"}, "p", hc, s.URL)
	if err != nil || res.Version != "1.4.2" {
		t.Fatalf("pypi: %+v %v", res, err)
	}
}
func TestBrew(t *testing.T) {
	s, hc := srv(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"versions":{"stable":"3.2.1"}}`)) })
	defer s.Close()
	res, err := fetchBrew(inventory.Software{Name: "x", LatestSource: "brew:w"}, "w", hc, s.URL)
	if err != nil || res.Version != "3.2.1" {
		t.Fatalf("brew: %+v %v", res, err)
	}
}
func TestCustom(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires bash")
	}
	res, err := fetchCustom(inventory.Software{Name: "x", LatestSource: "custom:echo 9.9.9"}, "echo 9.9.9", nil)
	if err != nil || res.Version != "9.9.9" {
		t.Fatalf("custom: %+v %v", res, err)
	}
	if _, err := fetchCustom(inventory.Software{}, "exit 7", nil); err == nil {
		t.Fatal("custom nonzero should error")
	}
}

// brew / custom 來源帶 github changelog 時必須抓 release body（修：先前永遠空白）。
func TestBrewChangelog(t *testing.T) {
	gh, ghc := srv(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name":"v3.2.1","body":"## 3.2.1\n- brew fix"}`))
	})
	defer gh.Close()
	old := githubBase
	githubBase = gh.URL
	defer func() { githubBase = old }()

	s, _ := srv(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"versions":{"stable":"3.2.1"}}`)) })
	defer s.Close()
	sw := inventory.Software{Name: "x", LatestSource: "brew:w", Changelog: "github:o/w"}
	res, err := fetchBrew(sw, "w", ghc, s.URL)
	if err != nil || res.Version != "3.2.1" {
		t.Fatalf("brew: %+v %v", res, err)
	}
	if res.ChangelogRaw == "" {
		t.Fatal("brew with github changelog should fill ChangelogRaw")
	}
}

func TestCustomChangelog(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires bash")
	}
	gh, ghc := srv(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name":"v9.9.9","body":"## 9.9.9\n- custom fix"}`))
	})
	defer gh.Close()
	old := githubBase
	githubBase = gh.URL
	defer func() { githubBase = old }()

	sw := inventory.Software{Name: "x", LatestSource: "custom:echo 9.9.9", Changelog: "github:o/x"}
	res, err := fetchCustom(sw, "echo 9.9.9", ghc)
	if err != nil || res.Version != "9.9.9" {
		t.Fatalf("custom: %+v %v", res, err)
	}
	if res.ChangelogRaw == "" {
		t.Fatal("custom with github changelog should fill ChangelogRaw")
	}
}

func TestURLChangelogTemplate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("requires bash")
	}
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte("# 0.2.101\n- feature"))
	}))
	defer srv.Close()
	sw := inventory.Software{
		Name: "grok", LatestSource: "custom:echo 0.2.101",
		Changelog: "url:" + srv.URL + "/changelogs/{version}.external.md",
	}
	res, err := fetchCustom(sw, "echo 0.2.101", srv.Client())
	if err != nil || res.Version != "0.2.101" {
		t.Fatalf("%+v %v", res, err)
	}
	if res.ChangelogRaw == "" || !strings.Contains(res.ChangelogRaw, "feature") {
		t.Fatalf("raw=%q path=%q", res.ChangelogRaw, gotPath)
	}
	if !strings.Contains(gotPath, "0.2.101") {
		t.Fatalf("path %q missing version", gotPath)
	}
}
