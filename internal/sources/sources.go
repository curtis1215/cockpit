package sources

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/curtis1215/cockpit/internal/inventory"
)

type SourceResult struct {
	Version      string
	ChangelogRaw string
}

const (
	npmBase    = "https://registry.npmjs.org"
	githubBase = "https://api.github.com"
	pypiBase   = "https://pypi.org"
	brewBase   = "https://formulae.brew.sh"
)

func split(source string) (provider, locator string) {
	i := strings.IndexByte(source, ':')
	if i < 0 {
		return source, ""
	}
	return source[:i], source[i+1:]
}

func FetchLatest(sw inventory.Software, hc *http.Client) (SourceResult, error) {
	if hc == nil {
		hc = &http.Client{Timeout: 20 * time.Second}
	}
	provider, locator := split(sw.LatestSource)
	switch provider {
	case "npm":
		return fetchNpm(sw, locator, hc, npmBase)
	case "github":
		return fetchGithub(sw, locator, hc, githubBase)
	case "pypi":
		return fetchPypi(sw, locator, hc, pypiBase)
	case "brew":
		return fetchBrew(sw, locator, hc, brewBase)
	case "claude-plugin":
		return fetchGithub(sw, locator, hc, githubBase)
	case "custom":
		return fetchCustom(sw, locator)
	default:
		return SourceResult{}, fmt.Errorf("unknown provider: %s", provider)
	}
}

func getJSON(hc *http.Client, url string, hdr map[string]string, out any) error {
	req, _ := http.NewRequest("GET", url, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return fmt.Errorf("http %d: %s", resp.StatusCode, b)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func ghHeaders() map[string]string {
	if t := os.Getenv("COCKPIT_GITHUB_TOKEN"); t != "" {
		return map[string]string{"Authorization": "Bearer " + t}
	}
	return nil
}

func fetchNpm(sw inventory.Software, locator string, hc *http.Client, base string) (SourceResult, error) {
	var out struct {
		DistTags struct {
			Latest string `json:"latest"`
		} `json:"dist-tags"`
	}
	if err := getJSON(hc, base+"/"+locator, nil, &out); err != nil {
		return SourceResult{}, err
	}
	res := SourceResult{Version: out.DistTags.Latest}
	if strings.HasPrefix(sw.Changelog, "github:") {
		res.ChangelogRaw = githubReleaseBody(strings.TrimPrefix(sw.Changelog, "github:"), res.Version, hc, githubBase)
	}
	return res, nil
}

func fetchGithub(sw inventory.Software, locator string, hc *http.Client, base string) (SourceResult, error) {
	var out struct {
		TagName string `json:"tag_name"`
		Body    string `json:"body"`
	}
	if err := getJSON(hc, base+"/repos/"+locator+"/releases/latest", ghHeaders(), &out); err != nil {
		return SourceResult{}, err
	}
	return SourceResult{Version: strings.TrimPrefix(out.TagName, "v"), ChangelogRaw: out.Body}, nil
}

func githubReleaseBody(repo, version string, hc *http.Client, base string) string {
	for _, tag := range []string{"v" + version, version} {
		var out struct {
			Body string `json:"body"`
		}
		if err := getJSON(hc, base+"/repos/"+repo+"/releases/tags/"+tag, ghHeaders(), &out); err == nil {
			return out.Body
		}
	}
	return ""
}

// ---- TEMPORARY stubs: replaced by Task 5 (more.go) ----
func fetchPypi(sw inventory.Software, l string, hc *http.Client, base string) (SourceResult, error) {
	return SourceResult{}, fmt.Errorf("pypi not yet")
}
func fetchBrew(sw inventory.Software, l string, hc *http.Client, base string) (SourceResult, error) {
	return SourceResult{}, fmt.Errorf("brew not yet")
}
func fetchCustom(sw inventory.Software, l string) (SourceResult, error) {
	return SourceResult{}, fmt.Errorf("custom not yet")
}
