package selfupdate

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

// Latest fetches the latest release info from the GitHub releases API.
// base is the API base URL (e.g. "https://api.github.com").
// repo is "owner/repo" (e.g. "curtis1215/cockpit").
// Returns the tag name and a map of asset name -> browser_download_url.
func Latest(hc *http.Client, base, repo string) (tag string, assets map[string]string, err error) {
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	url := fmt.Sprintf("%s/repos/%s/releases/latest", base, repo)
	resp, err := hc.Get(url)
	if err != nil {
		return "", nil, fmt.Errorf("fetch latest release: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", nil, fmt.Errorf("fetch latest release: HTTP %d", resp.StatusCode)
	}

	var payload struct {
		TagName string `json:"tag_name"`
		Assets  []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		return "", nil, fmt.Errorf("decode release JSON: %w", err)
	}

	assetMap := make(map[string]string, len(payload.Assets))
	for _, a := range payload.Assets {
		assetMap[a.Name] = a.BrowserDownloadURL
	}
	return payload.TagName, assetMap, nil
}

// AssetName returns the expected asset filename for the given platform.
// version should NOT have a leading "v".
func AssetName(goos, goarch, version string) string {
	return fmt.Sprintf("cockpit_%s_%s_%s.tar.gz", version, goos, goarch)
}

// Run checks for updates and, if a newer version is available, downloads and
// replaces the running binary atomically.
//
//   - hc: HTTP client (nil => default 60s timeout)
//   - base: GitHub API base URL (e.g. "https://api.github.com")
//   - repo: "owner/repo"
//   - currentVersion: the running binary's version (without leading "v")
//   - targetPath: path to replace (empty => os.Executable())
//
// Returns (true, nil) when the binary was replaced, (false, nil) when already
// up-to-date, and (false, err) on any failure.
func Run(hc *http.Client, base, repo, currentVersion, targetPath string) (bool, error) {
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}

	tag, assets, err := Latest(hc, base, repo)
	if err != nil {
		return false, err
	}

	// Strip leading "v" from the tag for comparison.
	tagVer := strings.TrimPrefix(tag, "v")
	if tagVer == currentVersion {
		fmt.Println("cockpit 已是最新版本:", currentVersion)
		return false, nil
	}

	assetName := AssetName(runtime.GOOS, runtime.GOARCH, tagVer)
	downloadURL, ok := assets[assetName]
	if !ok {
		return false, fmt.Errorf("no asset %q found in release %s", assetName, tag)
	}

	if targetPath == "" {
		exe, err := os.Executable()
		if err != nil {
			return false, fmt.Errorf("resolve executable path: %w", err)
		}
		targetPath = exe
	}

	// Download asset.
	resp, err := hc.Get(downloadURL)
	if err != nil {
		return false, fmt.Errorf("download asset: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("download asset: HTTP %d", resp.StatusCode)
	}

	// Extract "cockpit" binary from tar.gz into a temp file alongside targetPath.
	newPath := targetPath + ".new"
	if err := extractBinary(resp.Body, newPath); err != nil {
		os.Remove(newPath)
		return false, fmt.Errorf("extract binary: %w", err)
	}
	if err := os.Chmod(newPath, 0755); err != nil {
		os.Remove(newPath)
		return false, fmt.Errorf("chmod new binary: %w", err)
	}

	// Atomic replacement: target -> target.old, new -> target, remove old.
	oldPath := targetPath + ".old"
	os.Remove(oldPath) // best-effort remove stale .old
	if err := os.Rename(targetPath, oldPath); err != nil {
		os.Remove(newPath)
		return false, fmt.Errorf("backup current binary: %w", err)
	}
	if err := os.Rename(newPath, targetPath); err != nil {
		// Try to restore.
		os.Rename(oldPath, targetPath)
		os.Remove(newPath)
		return false, fmt.Errorf("install new binary: %w", err)
	}
	os.Remove(oldPath) // best-effort

	fmt.Printf("cockpit 已更新至 %s\n", tagVer)
	return true, nil
}

// extractBinary reads a gzip+tar stream and writes the first entry named
// "cockpit" (or ending in "/cockpit") to destPath.
func extractBinary(r io.Reader, destPath string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip reader: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar next: %w", err)
		}
		// Accept bare "cockpit" or path ending with "/cockpit".
		name := hdr.Name
		if name != "cockpit" && !strings.HasSuffix(name, "/cockpit") {
			continue
		}
		f, err := os.Create(destPath)
		if err != nil {
			return fmt.Errorf("create dest file: %w", err)
		}
		if _, err := io.Copy(f, tr); err != nil {
			f.Close()
			return fmt.Errorf("write dest file: %w", err)
		}
		return f.Close()
	}
	return fmt.Errorf("binary 'cockpit' not found in tar archive")
}
