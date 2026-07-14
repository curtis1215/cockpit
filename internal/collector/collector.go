package collector

import (
	"fmt"
	"strings"
	"time"

	"github.com/curtis1215/cockpit/internal/inventory"
	"github.com/curtis1215/cockpit/internal/sources"
	"github.com/curtis1215/cockpit/internal/store"
	"github.com/curtis1215/cockpit/internal/version"
)

type FetchFunc func(inventory.Software) (sources.SourceResult, error)
type TranslateFunc func(raw string) (string, error)
type Report struct {
	Software       string `json:"software"`
	CurrentVersion string `json:"current_version"`
}

func DefaultFetch(sw inventory.Software) (sources.SourceResult, error) {
	return sources.FetchLatest(sw, nil)
}

func RefreshUpstream(s *store.Store, inv inventory.Inventory, fetch FetchFunc, translate TranslateFunc) {
	for _, sw := range inv.Software {
		latest, err := fetch(sw)
		if err != nil {
			s.AddEvent("error", sw.Name, "", "fetch failed: "+err.Error())
			continue
		}
		existing, _ := s.GetVersion(sw.Name, latest.Version)
		zh := existing.ChangelogZh
		if zh != "" {
			s.AddVersion(sw.Name, latest.Version, "", latest.ChangelogRaw, zh)
			s.SetTranslateStatus(sw.Name, latest.Version, "ready", "")
			continue
		}
		s.AddVersion(sw.Name, latest.Version, "", latest.ChangelogRaw, "")
		if strings.TrimSpace(latest.ChangelogRaw) == "" {
			s.SetTranslateStatus(sw.Name, latest.Version, "none", "")
			continue
		}
		s.SetTranslateStatus(sw.Name, latest.Version, "translating", "")
		out, terr := translate(latest.ChangelogRaw)
		if terr != nil || strings.TrimSpace(out) == "" {
			msg := "empty translation"
			if terr != nil {
				msg = terr.Error()
			}
			// 只改 status，不動 zh——避免與並行 retry 競態時清掉剛寫好的中文。
			s.SetTranslateStatus(sw.Name, latest.Version, "failed", msg)
			s.AddEvent("error", sw.Name, "", fmt.Sprintf("translate failed (raw %d bytes): %s", len(latest.ChangelogRaw), msg))
			continue
		}
		s.UpdateTranslateResult(sw.Name, latest.Version, out, "ready", "")
	}
}

func ApplyVersionReport(s *store.Store, machine string, reports []Report) int {
	now := time.Now().UTC().Format(time.RFC3339)
	applied := 0
	for _, r := range reports {
		if r.Software == "" {
			continue
		}
		latest, _ := s.LatestVersion(r.Software)
		status, _ := version.Compare(r.CurrentVersion, latest.VersionStr)
		s.UpsertInstall(r.Software, machine, r.CurrentVersion, status, now)
		s.AddEvent("check", r.Software, machine, fmt.Sprintf("current=%s latest=%s status=%s", r.CurrentVersion, latest.VersionStr, status))
		applied++
	}
	return applied
}
