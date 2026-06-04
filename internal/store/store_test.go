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

func TestReEnrollSameLabelRotatesToken(t *testing.T) {
	s := open(t)
	id1, tok1, err := s.RegisterSystem("samehost", "linux", "amd64")
	if err != nil {
		t.Fatal(err)
	}
	// Re-enroll under same label (agent lost its token) → reuse row, rotate token.
	id2, tok2, err := s.RegisterSystem("samehost", "linux", "amd64")
	if err != nil {
		t.Fatalf("re-enroll: %v", err)
	}
	if id2 != id1 {
		t.Fatalf("id should be reused: %q vs %q", id1, id2)
	}
	if tok2 == tok1 {
		t.Fatalf("token should rotate, got same %q", tok2)
	}
	// Old token invalidated.
	if _, err := s.SystemByAgentToken(tok1); err != ErrNotFound {
		t.Fatalf("old token want ErrNotFound, got %v", err)
	}
	// New token resolves to the same system.
	sys, err := s.SystemByAgentToken(tok2)
	if err != nil || sys.ID != id1 {
		t.Fatalf("new token resolve: %+v err=%v", sys, err)
	}
	// Still exactly one system row.
	list, err := s.ListSystems()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("want 1 system, got %d: %+v", len(list), list)
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
