package selfupdate_test

import (
	"archive/tar"
	"archive/zip"
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

// goreleaser 對 windows 出的是 zip（format_overrides），asset 名稱必須一致，
// 否則 Windows agent 自我更新會報 no asset found。
func TestAssetNameWindows(t *testing.T) {
	got := selfupdate.AssetName("windows", "amd64", "0.5.1")
	want := "cockpit_0.5.1_windows_amd64.zip"
	if got != want {
		t.Errorf("AssetName = %q, want %q", got, want)
	}
}

// buildFakeZip creates an in-memory .zip archive containing a single file
// named "cockpit.exe" with the given content.
func buildFakeZip(t *testing.T, content string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create("cockpit.exe")
	if err != nil {
		t.Fatalf("zip create: %v", err)
	}
	if _, err := f.Write([]byte(content)); err != nil {
		t.Fatalf("zip write: %v", err)
	}
	zw.Close()
	return buf.Bytes()
}

// TestRun_UpgradeWindowsZip: Windows 平台的升級必須能下載 zip asset 並解出
// cockpit.exe 替換目標檔。
func TestRun_UpgradeWindowsZip(t *testing.T) {
	const newContent = "NEWEXE"
	assetBytes := buildFakeZip(t, newContent)
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/repos/owner/repo/releases/latest":
			assetName := "cockpit_9.9.9_windows_amd64.zip"
			payload := map[string]interface{}{
				"tag_name": "v9.9.9",
				"assets": []map[string]interface{}{
					{"name": assetName, "browser_download_url": srv.URL + "/assets/" + assetName},
				},
			}
			w.Header().Set("Content-Type", "application/json")
			json.NewEncoder(w).Encode(payload)
		case len(r.URL.Path) > 8 && r.URL.Path[:8] == "/assets/":
			w.Write(assetBytes)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	tmp := t.TempDir()
	target := filepath.Join(tmp, "cockpit.exe")
	if err := os.WriteFile(target, []byte("OLDEXE"), 0755); err != nil {
		t.Fatalf("write target: %v", err)
	}

	replaced, err := selfupdate.RunForPlatform(nil, srv.URL, "owner/repo", "1.0.0", target, "windows", "amd64")
	if err != nil {
		t.Fatalf("RunForPlatform: %v", err)
	}
	if !replaced {
		t.Fatal("expected replaced=true")
	}
	got, err := os.ReadFile(target)
	if err != nil {
		t.Fatalf("read target: %v", err)
	}
	if string(got) != newContent {
		t.Errorf("target content = %q, want %q", string(got), newContent)
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

	if _, err := selfupdate.Run(nil, srv.URL, "owner/repo", "1.0.0", target); err != nil {
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
	if runtime.GOOS != "windows" { // Windows 無 unix 執行位
		if info.Mode()&0111 == 0 {
			t.Errorf("new binary is not executable, mode=%v", info.Mode())
		}
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
	if _, err := selfupdate.Run(nil, srv.URL, "owner/repo", "9.9.9", target); err != nil {
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
