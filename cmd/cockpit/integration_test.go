package main

import (
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/curtis1215/cockpit/internal/agent"
	"github.com/curtis1215/cockpit/internal/server"
	"github.com/curtis1215/cockpit/internal/store"
)

func TestEndToEndEnrollHeartbeat(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ts := httptest.NewServer(server.New(st, "s3cret").Handler())
	defer ts.Close()

	a := &agent.Agent{ServerURL: ts.URL, Secret: "s3cret", Version: "9.9.9",
		SaveToken: func(string) error { return nil }}
	if err := a.RunOnce(); err != nil {
		t.Fatal(err)
	}
	list, err := st.ListSystems()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].Status != "online" || list[0].AgentVersion != "9.9.9" {
		t.Fatalf("system not online with version: %+v", list)
	}
}
