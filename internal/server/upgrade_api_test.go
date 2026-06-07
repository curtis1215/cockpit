package server

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
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

func TestServerUpgradeSuccess(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.SetVersion("0.2.1")

	exited := make(chan struct{})
	srv.upgradeFn = func() (bool, error) {
		return true, nil
	}
	srv.exitFn = func() {
		close(exited)
	}

	rec := doJSON(t, srv, "POST", "/api/server/upgrade", "")
	if rec.Code != 202 {
		t.Fatalf("upgrade: %d %s", rec.Code, rec.Body.String())
	}

	select {
	case <-exited:
	case <-time.After(3 * time.Second):
		t.Fatal("exitFn not called within 3s")
	}
}

func TestServerUpgradeUpToDate(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.SetVersion("0.2.1")
	srv.upgradeFn = func() (bool, error) {
		return false, nil
	}
	srv.exitFn = func() {
		t.Fatal("must not exit when already up to date")
	}

	rec := doJSON(t, srv, "POST", "/api/server/upgrade", "")
	if rec.Code != 200 {
		t.Fatalf("upgrade: %d %s", rec.Code, rec.Body.String())
	}

	rec2 := doJSON(t, srv, "POST", "/api/server/upgrade", "")
	if rec2.Code != 200 {
		t.Fatalf("second release should be allowed after up_to_date, got %d %s", rec2.Code, rec2.Body.String())
	}
}

func TestServerUpgradeError(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.SetVersion("0.2.1")
	srv.upgradeFn = func() (bool, error) {
		return false, errBoom
	}

	rec := doJSON(t, srv, "POST", "/api/server/upgrade", "")
	if rec.Code != 500 {
		t.Fatalf("error: %d %s", rec.Code, rec.Body.String())
	}

	rec2 := doJSON(t, srv, "POST", "/api/server/upgrade", "")
	if rec2.Code != 500 {
		t.Fatalf("lock should be released after error, got %d %s", rec2.Code, rec2.Body.String())
	}
}

func TestServerUpgradeBinaryNotWritable(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.SetVersion("0.2.1")
	called := false
	srv.writableCheckFn = func() error {
		return errBoom
	}
	srv.upgradeFn = func() (bool, error) {
		called = true
		return true, nil
	}

	rec := doJSON(t, srv, "POST", "/api/server/upgrade", "")
	if rec.Code != 500 {
		t.Fatalf("writable check: %d %s", rec.Code, rec.Body.String())
	}
	if called {
		t.Fatal("upgradeFn must not be called when binary is not writable")
	}
	if !strings.Contains(rec.Body.String(), "binary not writable") {
		t.Fatalf("missing actionable error: %s", rec.Body.String())
	}
}

func TestServerUpgradeConflict(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.SetVersion("0.2.1")
	started := make(chan struct{})
	release := make(chan struct{})
	srv.upgradeFn = func() (bool, error) {
		close(started)
		<-release
		return false, nil
	}

	done := make(chan *httptestResult, 1)
	go func() {
		rec := doJSON(t, srv, "POST", "/api/server/upgrade", "")
		done <- &httptestResult{code: rec.Code, body: rec.Body.String()}
	}()

	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("upgradeFn not started")
	}

	rec2 := doJSON(t, srv, "POST", "/api/server/upgrade", "")
	if rec2.Code != 409 {
		t.Fatalf("conflict: %d %s", rec2.Code, rec2.Body.String())
	}

	close(release)
	select {
	case res := <-done:
		if res.code != 200 {
			t.Fatalf("first upgrade: %d %s", res.code, res.body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first upgrade did not finish")
	}
}

type httptestResult struct {
	code int
	body string
}

func TestVersionDevBuildSkipsLatest(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.SetVersion("0.0.0-dev")
	called := false
	srv.latestFn = func() (string, error) { called = true; return "9.9.9", nil }
	rec := doJSON(t, srv, "GET", "/api/version", "")
	var resp map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if called || resp["update_available"] != false {
		t.Fatalf("dev build must not query github: called=%v resp=%v", called, resp)
	}
}

func TestServerUpgradeDevBuild(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.SetVersion("0.0.0-dev")
	if rec := doJSON(t, srv, "POST", "/api/server/upgrade", ""); rec.Code != 400 {
		t.Fatalf("dev build: %d, want 400", rec.Code)
	}
}

func TestServerUpgradeMethodNotAllowed(t *testing.T) {
	srv, _ := newTestServer(t)
	if rec := doJSON(t, srv, "GET", "/api/server/upgrade", ""); rec.Code != 405 {
		t.Fatalf("GET: %d, want 405", rec.Code)
	}
}
