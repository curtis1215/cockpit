package sources

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/curtis1215/cockpit/internal/inventory"
)

func srv(h http.HandlerFunc) (*httptest.Server, *http.Client) {
	s := httptest.NewServer(h)
	return s, s.Client()
}

func TestNpm(t *testing.T) {
	s, hc := srv(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"dist-tags":{"latest":"2.1.101"}}`))
	})
	defer s.Close()
	sw := inventory.Software{Name: "cc", Kind: "npm", LatestSource: "npm:cc"}
	res, err := fetchNpm(sw, "cc", hc, s.URL)
	if err != nil || res.Version != "2.1.101" {
		t.Fatalf("npm: %+v %v", res, err)
	}
}

func TestGithub(t *testing.T) {
	s, hc := srv(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"tag_name":"v0.9.0","body":"## 0.9.0\n- fix"}`))
	})
	defer s.Close()
	sw := inventory.Software{Name: "m", Kind: "github", LatestSource: "github:o/m", Changelog: "github:o/m"}
	res, err := fetchGithub(sw, "o/m", hc, s.URL)
	if err != nil || res.Version != "0.9.0" || res.ChangelogRaw == "" {
		t.Fatalf("github: %+v %v", res, err)
	}
}

func TestGithubReleaseBodyTagFallback(t *testing.T) {
	s, hc := srv(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/o/m/releases/tags/v0.137.0", "/repos/o/m/releases/tags/0.137.0":
			http.Error(w, "not found", 404)
		case "/repos/o/m/releases":
			w.Write([]byte(`[{"tag_name":"rust-v0.138.0-alpha.1","body":"alpha"},{"tag_name":"rust-v0.137.0","body":"stable notes"}]`))
		default:
			http.Error(w, "nope", 404)
		}
	})
	defer s.Close()
	if got := githubReleaseBody("o/m", "0.137.0", hc, s.URL); got != "stable notes" {
		t.Fatalf("fallback: %q", got)
	}
}
