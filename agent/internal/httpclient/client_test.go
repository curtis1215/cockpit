package httpclient

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGetJSONSendsAuth(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		json.NewEncoder(w).Encode(map[string]string{"hello": "world"})
	}))
	defer srv.Close()
	c := New(srv.URL, "tok", 5*time.Second)
	var out map[string]string
	status, err := c.GetJSON("/x", &out)
	if err != nil || status != 200 || out["hello"] != "world" {
		t.Fatalf("status=%d err=%v out=%v", status, err, out)
	}
}

func TestGet204(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(204)
	}))
	defer srv.Close()
	c := New(srv.URL, "tok", 5*time.Second)
	var out map[string]string
	status, err := c.GetJSON("/x", &out)
	if err != nil || status != 204 {
		t.Fatalf("status=%d err=%v", status, err)
	}
}

func TestPostJSON(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(204)
	}))
	defer srv.Close()
	c := New(srv.URL, "tok", 5*time.Second)
	status, err := c.PostJSON("/y", map[string]string{"a": "b"}, nil)
	if err != nil || status != 204 || got["a"] != "b" {
		t.Fatalf("status=%d err=%v got=%v", status, err, got)
	}
}
