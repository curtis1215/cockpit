package collector

import (
	"fmt"
	"time"

	"github.com/curtis1215/cockpit/internal/inventory"
	"github.com/curtis1215/cockpit/internal/sources"
	"github.com/curtis1215/cockpit/internal/store"
	"github.com/curtis1215/cockpit/internal/version"
)

type FetchFunc func(inventory.Software) (sources.SourceResult, error)
type TranslateFunc func(raw string) string
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
		zh := ""
		if existing, e := s.GetVersion(sw.Name, latest.Version); e == nil {
			zh = existing.ChangelogZh
		}
		if zh == "" && latest.ChangelogRaw != "" {
			zh = translate(latest.ChangelogRaw)
			if zh == "" {
				// 翻譯靜默失敗（codex/claude 逾時或暫時性錯誤）：留下 error event，
				// 否則此版本會無中文且無從察覺。下次 refresh 仍會重試翻譯。
				s.AddEvent("error", sw.Name, "", fmt.Sprintf("translate failed (raw %d bytes)", len(latest.ChangelogRaw)))
			}
		}
		s.AddVersion(sw.Name, latest.Version, "", latest.ChangelogRaw, zh)
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
