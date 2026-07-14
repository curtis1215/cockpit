package server

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/curtis1215/cockpit/internal/inventory"
	"github.com/curtis1215/cockpit/internal/store"
)

func vtInv() inventory.Inventory {
	return inventory.Inventory{
		Machines: map[string]inventory.Machine{"mac": {Name: "mac", AgentToken: "tok-mac"}},
		Software: []inventory.Software{{Name: "cc", Kind: "npm", LatestSource: "npm:cc",
			Installs: []inventory.Install{{Machine: "mac", CurrentCmd: "cc --version", Update: inventory.Update{Type: "command", Cmd: "x"}}}}},
	}
}
func vtServer(t *testing.T) (*Server, *store.Store) {
	st, _ := store.Open(filepath.Join(t.TempDir(), "c.db"))
	t.Cleanup(func() { st.Close() })
	st.AddVersion("cc", "2.1.101", "2026-04-10", "raw", "中文")
	st.UpsertInstall("cc", "mac", "2.1.98", "behind", "t")
	return NewWithInventory(st, "s3cret", vtInv()), st
}

func TestInstallsEnriched(t *testing.T) {
	srv, _ := vtServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/installs", nil))
	b := rec.Body.String()
	if !strings.Contains(b, `"id":"cc::mac"`) || !strings.Contains(b, `"behind_count":3`) ||
		!strings.Contains(b, `"update_kind":"command"`) || !strings.Contains(b, `"status":"behind"`) {
		t.Fatalf("installs: %s", b)
	}
}
func TestChangelog(t *testing.T) {
	srv, _ := vtServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/changelog/cc/2.1.101", nil))
	if !strings.Contains(rec.Body.String(), `"changelog_zh":"中文"`) {
		t.Fatalf("changelog: %s", rec.Body.String())
	}
}

func TestChangelogIncludesTranslateStatus(t *testing.T) {
	srv, st := vtServer(t)
	// ready from AddVersion with zh
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/changelog/cc/2.1.101", nil))
	var got map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["translate_status"] != "ready" {
		t.Fatalf("want ready got %v body=%s", got["translate_status"], rec.Body)
	}
	// failed row
	st.AddVersion("cc", "9.9.9", "", "raw body", "")
	st.SetTranslateStatus("cc", "9.9.9", "failed", "timeout")
	rec = httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/changelog/cc/9.9.9", nil))
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got["translate_status"] != "failed" || got["translate_error"] != "timeout" {
		t.Fatalf("failed fields: %+v", got)
	}
	if _, ok := got["translate_updated_at"]; !ok {
		t.Fatal("missing translate_updated_at")
	}
}

func TestChangelogRetryNoRaw400(t *testing.T) {
	srv, st := vtServer(t)
	st.AddVersion("cc", "0.0.1", "", "", "")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/changelog/cc/0.0.1/retry", nil))
	if rec.Code != 400 {
		t.Fatalf("want 400 got %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), "no changelog raw") {
		t.Fatalf("body: %s", rec.Body)
	}
}

func TestChangelogRetrySuccess(t *testing.T) {
	srv, st := vtServer(t)
	st.AddVersion("cc", "3.0.0", "", "## raw notes", "")
	st.SetTranslateStatus("cc", "3.0.0", "failed", "prev")
	done := make(chan struct{})
	srv.TranslateFn = func(raw string) (string, error) {
		defer close(done)
		if raw != "## raw notes" {
			return "", errors.New("bad raw")
		}
		return "中文結果", nil
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/changelog/cc/3.0.0/retry", nil))
	if rec.Code != 200 {
		t.Fatalf("retry code %d %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"translate_status":"translating"`) {
		t.Fatalf("body: %s", rec.Body)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("translate did not finish")
	}
	// wait for store update
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		v, _ := st.GetVersion("cc", "3.0.0")
		if v.TranslateStatus == "ready" && v.ChangelogZh == "中文結果" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	v, _ := st.GetVersion("cc", "3.0.0")
	t.Fatalf("want ready/中文結果 got status=%q zh=%q err=%q", v.TranslateStatus, v.ChangelogZh, v.TranslateError)
}

func TestChangelogRetryConflict409(t *testing.T) {
	srv, st := vtServer(t)
	st.AddVersion("cc", "4.0.0", "", "raw", "old")
	block := make(chan struct{})
	started := make(chan struct{})
	var once sync.Once
	srv.TranslateFn = func(raw string) (string, error) {
		once.Do(func() { close(started) })
		<-block
		return "zh", nil
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/changelog/cc/4.0.0/retry", nil))
	if rec.Code != 200 {
		t.Fatalf("first retry %d %s", rec.Code, rec.Body)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("translate not started")
	}
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, httptest.NewRequest("POST", "/api/changelog/cc/4.0.0/retry", nil))
	if rec2.Code != 409 {
		t.Fatalf("want 409 got %d %s", rec2.Code, rec2.Body)
	}
	close(block)
	// allow goroutine to finish
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		v, _ := st.GetVersion("cc", "4.0.0")
		if v.TranslateStatus == "ready" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestChangelogRetryNotFound(t *testing.T) {
	srv, _ := vtServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/changelog/nope/1.0/retry", nil))
	if rec.Code != 404 {
		t.Fatalf("want 404 got %d", rec.Code)
	}
}

func TestChangelogRetryFailureKeepsZh(t *testing.T) {
	srv, st := vtServer(t)
	st.AddVersion("cc", "5.0.0", "", "## raw", "既有中文")
	st.SetTranslateStatus("cc", "5.0.0", "ready", "")
	done := make(chan struct{})
	srv.TranslateFn = func(raw string) (string, error) {
		defer close(done)
		return "", errors.New("lm down")
	}
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/changelog/cc/5.0.0/retry", nil))
	if rec.Code != 200 {
		t.Fatalf("retry code %d %s", rec.Code, rec.Body)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("translate did not finish")
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		v, _ := st.GetVersion("cc", "5.0.0")
		if v.TranslateStatus == "failed" && v.TranslateError == "lm down" {
			if v.ChangelogZh != "既有中文" {
				t.Fatalf("zh wiped on failure: %q", v.ChangelogZh)
			}
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	v, _ := st.GetVersion("cc", "5.0.0")
	t.Fatalf("want failed/lm down keeping zh got status=%q zh=%q err=%q", v.TranslateStatus, v.ChangelogZh, v.TranslateError)
}

func TestInstallsNoTranslateStatus(t *testing.T) {
	srv, _ := vtServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/installs", nil))
	if strings.Contains(rec.Body.String(), "translate_status") {
		t.Fatalf("installs must not include translate_status: %s", rec.Body)
	}
}
func TestTriggerAndConflict(t *testing.T) {
	srv, _ := vtServer(t)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("POST", "/api/installs/cc/mac/update", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"job_id"`) {
		t.Fatalf("trigger: %d %s", rec.Code, rec.Body.String())
	}
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, httptest.NewRequest("POST", "/api/installs/cc/mac/update", nil))
	if rec2.Code != 409 {
		t.Fatalf("conflict want 409 got %d", rec2.Code)
	}
}
func TestJobGetAndAbort(t *testing.T) {
	srv, st := vtServer(t)
	jid, _ := st.CreateJobUnique("cc", "mac", "command", "")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/jobs/"+strconv.FormatInt(jid, 10), nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"status":"queued"`) {
		t.Fatalf("get job: %d %s", rec.Code, rec.Body.String())
	}
	rec2 := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec2, httptest.NewRequest("POST", "/api/jobs/"+strconv.FormatInt(jid, 10)+"/abort", nil))
	if rec2.Code != 200 || !strings.Contains(rec2.Body.String(), `"aborted"`) {
		t.Fatalf("abort: %d %s", rec2.Code, rec2.Body.String())
	}
}
func TestSSEEndsOnDone(t *testing.T) {
	srv, st := vtServer(t)
	jid, _ := st.CreateJobUnique("cc", "mac", "command", "")
	st.AppendJobLog(jid, "line A")
	st.FinishJob(jid, "success", 0, "2.1.101")
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/jobs/"+strconv.FormatInt(jid, 10)+"/log/stream", nil))
	b := rec.Body.String()
	if !strings.Contains(b, "line A") || !strings.Contains(b, "event: done") {
		t.Fatalf("sse: %s", b)
	}
}
