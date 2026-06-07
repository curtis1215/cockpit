package server

import (
	"encoding/json"
	"errors"
	"testing"
)

var errBoom = errors.New("boom")

func TestVersionWithLatest(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.SetVersion("0.2.1")

	calls := 0
	srv.latestFn = func() (string, error) {
		calls++
		return "0.3.0", nil
	}

	rec := doJSON(t, srv, "GET", "/api/version", "")
	if rec.Code != 200 {
		t.Fatalf("version: %d %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp["version"] != "0.2.1" {
		t.Fatalf("version: %v", resp)
	}
	if resp["latest"] != "0.3.0" {
		t.Fatalf("latest: %v", resp)
	}
	if resp["update_available"] != true {
		t.Fatalf("update_available: %v", resp)
	}
	if calls != 1 {
		t.Fatalf("latestFn calls: got %d want 1", calls)
	}
}

func TestVersionLatestEqualsCurrent(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.SetVersion("0.3.0")
	srv.latestFn = func() (string, error) {
		return "0.3.0", nil
	}

	rec := doJSON(t, srv, "GET", "/api/version", "")
	if rec.Code != 200 {
		t.Fatalf("version: %d %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp["update_available"] != false {
		t.Fatalf("update_available: %v", resp)
	}
}

func TestVersionLatestFetchFails(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.SetVersion("0.2.1")
	srv.latestFn = func() (string, error) {
		return "", errBoom
	}

	rec := doJSON(t, srv, "GET", "/api/version", "")
	if rec.Code != 200 {
		t.Fatalf("version: %d %s", rec.Code, rec.Body.String())
	}

	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp["version"] != "0.2.1" || resp["latest"] != "" || resp["update_available"] != false {
		t.Fatalf("unexpected response: %v", resp)
	}
}
