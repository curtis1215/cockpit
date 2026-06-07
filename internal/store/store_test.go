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

func TestSystemGroupColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "g.db")
	st, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	id, _, err := st.CreateSystemPending("gbox", "")
	if err != nil {
		t.Fatal(err)
	}
	// 預設未分組
	sys, err := st.SystemByID(id)
	if err != nil {
		t.Fatal(err)
	}
	if sys.Grp != "" {
		t.Fatalf("default grp = %q, want empty", sys.Grp)
	}
	// 設定群組（中文 OK）
	if err := st.SetSystemGroup(id, "工作"); err != nil {
		t.Fatal(err)
	}
	sys, _ = st.SystemByID(id)
	if sys.Grp != "工作" {
		t.Fatalf("grp = %q, want 工作", sys.Grp)
	}
	// 清空（解除分組）
	if err := st.SetSystemGroup(id, ""); err != nil {
		t.Fatal(err)
	}
	sys, _ = st.SystemByID(id)
	if sys.Grp != "" {
		t.Fatalf("grp = %q, want empty after clear", sys.Grp)
	}
	// 不存在的 id → ErrNotFound
	if err := st.SetSystemGroup("sys_nope", "x"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}
	// migration 冪等：關掉重開同一個 db 不應報錯
	st.Close()
	st2, err := Open(path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer st2.Close()
	if sys, err = st2.SystemByID(id); err != nil || sys.Grp != "" {
		t.Fatalf("after reopen: err=%v grp=%q", err, sys.Grp)
	}
}
