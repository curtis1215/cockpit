package sources

import (
	"context"
	"net/http"
	"os/exec"
	"strings"
	"time"

	"github.com/curtis1215/cockpit/internal/inventory"
	"github.com/curtis1215/cockpit/internal/version"
)

func fetchPypi(sw inventory.Software, locator string, hc *http.Client, base string) (SourceResult, error) {
	var out struct {
		Info struct {
			Version string `json:"version"`
		} `json:"info"`
	}
	if err := getJSON(hc, base+"/pypi/"+locator+"/json", nil, &out); err != nil {
		return SourceResult{}, err
	}
	res := SourceResult{Version: out.Info.Version}
	if strings.HasPrefix(sw.Changelog, "github:") {
		res.ChangelogRaw = githubReleaseBody(strings.TrimPrefix(sw.Changelog, "github:"), res.Version, hc, githubBase)
	}
	return res, nil
}

func fetchBrew(sw inventory.Software, locator string, hc *http.Client, base string) (SourceResult, error) {
	var out struct {
		Versions struct {
			Stable string `json:"stable"`
		} `json:"versions"`
	}
	if err := getJSON(hc, base+"/api/formula/"+locator+".json", nil, &out); err != nil {
		return SourceResult{}, err
	}
	res := SourceResult{Version: out.Versions.Stable}
	if strings.HasPrefix(sw.Changelog, "github:") {
		res.ChangelogRaw = githubReleaseBody(strings.TrimPrefix(sw.Changelog, "github:"), res.Version, hc, githubBase)
	}
	return res, nil
}

func fetchCustom(sw inventory.Software, locator string, hc *http.Client) (SourceResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, "bash", "-lc", locator)
	b, err := c.CombinedOutput()
	if err != nil {
		return SourceResult{}, err
	}
	out := strings.TrimSpace(string(b))
	v := version.Parse(out, "")
	if v == "" {
		v = out
	}
	res := SourceResult{Version: v}
	if strings.HasPrefix(sw.Changelog, "github:") {
		res.ChangelogRaw = githubReleaseBody(strings.TrimPrefix(sw.Changelog, "github:"), res.Version, hc, githubBase)
	}
	return res, nil
}
