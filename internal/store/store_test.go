package store

import (
	"path/filepath"
	"testing"
)

func open(t *testing.T) *Store {
	s, err := Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestRegisterAndLookup(t *testing.T) {
	s := open(t)
	id, token, err := s.RegisterSystem("Mac mini", "darwin", "arm64")
	if err != nil {
		t.Fatal(err)
	}
	if id == "" || token == "" {
		t.Fatalf("empty id/token")
	}
	sys, err := s.SystemByAgentToken(token)
	if err != nil {
		t.Fatal(err)
	}
	if sys.ID != id || sys.Status != "online" || sys.OS != "darwin" {
		t.Fatalf("bad system: %+v", sys)
	}
	if _, err := s.SystemByAgentToken("nope"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
}

func TestHeartbeatAndList(t *testing.T) {
	s := open(t)
	_, token, _ := s.RegisterSystem("box", "linux", "amd64")
	if err := s.Heartbeat(token, "0.1.0"); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListSystems()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].AgentVersion != "0.1.0" || list[0].LastSeen == "" {
		t.Fatalf("bad list: %+v", list)
	}
}
