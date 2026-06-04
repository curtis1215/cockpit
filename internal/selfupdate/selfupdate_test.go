package selfupdate_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/curtis1215/cockpit/internal/selfupdate"
)

// buildFakeTarGz creates an in-memory .tar.gz archive containing a single
// file named "cockpit" with the given content.
func buildFakeTarGz(t *testing.T, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gw)

	data := []byte(content)
	hdr := &tar.Header{
		Name: "cockpit",
		Mode: 0755,
		Size: int64(len(data)),
	}
	if err := tw.WriteHeader(hdr); err != nil {
		t.Fatalf("tar write header: %v", err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatalf("tar write body: %v", err)
	}
	tw.Close()
	gw.Close()
	return buf.Bytes()
}

// setupFakeServer starts a test HTTP server that returns a fake GitHub
// releases/latest response pointing the asset URL back at the same server.
func setupFakeServer(t *testing.T, tag, assetContent string) *httptest.Server {
	t.Helper()
	assetBytes := buildFakeTarGz(t, assetContent)

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/owner/repo/releases/latest":
			tagVer := tag
			// strip "v" for asset name
			if len(tagVer) > 0 && tagVer[0] == 'v' {
				tagVer = tagVer[1:]
			}
			assetName := fmt.Sprintf("cockpit_%s_%s_%s.tar.gz", tagVer, runtime.GOOS, runtime.GOARCH)
			assetURL := srv.URL + "/assets/" + assetName
			payload := map[string]interface{}{
				"tag_name": tag,
				"assets": []map[string]interface{}{
					{
						"name":                 assetName,
						"browser_download_url": assetURL,
					},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(payload)

		case len(r.URL.Path) > 8 && r.URL.Path[:8] == "/assets/":
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write(assetBytes)

		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAssetName(t *testing.T) {
	got := selfupdate.AssetName("linux", "amd64", "0.1.0")
	want := "cockpit_0.1.0_linux_amd64.tar.gz"
	if got != want {
		t.Errorf("AssetName = %q, want %q", got, want)
	}
}

func TestLatest(t *testing.T) {
	srv := setupFakeServer(t, "v9.9.9", "unused")
	tag, assets, err := selfupdate.Latest(nil, srv.URL, "owner/repo")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if tag != "v9.9.9" {
		t.Errorf("tag = %q, want v9.9.9", tag)
	}
	want := fmt.Sprintf("cockpit_9.9.9_%s_%s.tar.gz", runtime.GOOS, runtime.GOARCH)
	if _, ok := assets[want]; !ok {
		t.Errorf("expected asset %q in map %v", want, assets)
	}
}

func TestRun_Upgrade(t *testing.T) {
	const newContent = "NEWBIN"
	srv := setupFakeServer(t, "v9.9.9", newContent)

	// Create a temp target file representing the "old" binary.
	tmp := t.TempDir()
	target := filepath.Join(tmp, "cockpit")
	if err := os.WriteFile(target, []byte("OLDBIN"), 0755); err != nil {
		t.Fatalf("write target: %v", err)
	}

	if err := selfupdate.Run(nil, srv.URL, "owner/repo", "1.0.0", target); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target after upgrade: %v", err)
	}
	if string(got) != newContent {
		t.Errorf("target content = %q, want %q", string(got), newContent)
	}

	// Check executable bit.
	info, err := os.Stat(target)
	if err != nil {
		t.Fatalf("stat target: %v", err)
	}
	if info.Mode()&0111 == 0 {
		t.Errorf("new binary is not executable, mode=%v", info.Mode())
	}

	// .old file should be removed.
	if _, err := os.Stat(target + ".old"); !os.IsNotExist(err) {
		t.Errorf("expected .old file to be removed")
	}
}

func TestRun_AlreadyLatest(t *testing.T) {
	srv := setupFakeServer(t, "v9.9.9", "NEWBIN")

	tmp := t.TempDir()
	target := filepath.Join(tmp, "cockpit")
	original := []byte("ORIGINAL")
	if err := os.WriteFile(target, original, 0755); err != nil {
		t.Fatalf("write target: %v", err)
	}

	// currentVersion matches tag (without "v").
	if err := selfupdate.Run(nil, srv.URL, "owner/repo", "9.9.9", target); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != string(original) {
		t.Errorf("target was modified; got %q, want %q", string(got), string(original))
	}
}
