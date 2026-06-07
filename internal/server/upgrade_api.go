package server

import (
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/curtis1215/cockpit/internal/selfupdate"
)

const latestCacheTTL = time.Hour

func cockpitRepo() string {
	if r := os.Getenv("COCKPIT_REPO"); r != "" {
		return r
	}
	return "curtis1215/cockpit"
}

func defaultLatestFn() func() (string, error) {
	return func() (string, error) {
		hc := &http.Client{Timeout: 20 * time.Second}
		tag, _, err := selfupdate.Latest(hc, "https://api.github.com", cockpitRepo())
		return strings.TrimPrefix(tag, "v"), err
	}
}

func defaultUpgrade(current string) (bool, error) {
	hc := &http.Client{Timeout: 60 * time.Second}
	return selfupdate.Run(hc, "https://api.github.com", cockpitRepo(), current, "")
}

func (s *Server) isDevBuild() bool {
	return s.version == "" || s.version == "0.0.0-dev"
}

func (s *Server) latestCached() (string, error) {
	s.latestMu.Lock()
	defer s.latestMu.Unlock()

	if !s.latestAt.IsZero() && time.Since(s.latestAt) < latestCacheTTL {
		return s.latestCache, nil
	}

	v, err := s.latestFn()
	if err != nil {
		return "", err
	}
	s.latestCache = v
	s.latestAt = time.Now()
	return v, nil
}

func (s *Server) apiVersion(w http.ResponseWriter, r *http.Request) {
	v := s.version
	if v == "" {
		v = "dev"
	}

	resp := map[string]any{
		"version":          v,
		"latest":           "",
		"update_available": false,
	}
	if s.isDevBuild() {
		writeJSON(w, 200, resp)
		return
	}

	latest, err := s.latestCached()
	if err == nil && latest != "" {
		resp["latest"] = latest
		resp["update_available"] = latest != s.version
	}
	writeJSON(w, 200, resp)
}
