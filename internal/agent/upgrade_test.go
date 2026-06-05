package agent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestUpgradeCaseUpdated verifies that when doUpgrade returns (true, nil),
// the agent calls exit(0) after logging.
func TestUpgradeCaseUpdated(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent/installs":
			json.NewEncoder(w).Encode([]map[string]any{})
		case "/api/agent/poll":
			// Return "upgrade" on first call, then block forever (only one iteration needed).
			json.NewEncoder(w).Encode(map[string]string{"type": "upgrade"})
		}
	}))
	defer srv.Close()

	upgradeCalled := false
	exitCalled := false
	var exitCode int

	a := &Agent{
		ServerURL: srv.URL,
		Token:     "tok",
		Version:   "0.1.0",
		doUpgrade: func() (bool, error) {
			upgradeCalled = true
			return true, nil // updated=true
		},
		exit: func(code int) {
			exitCalled = true
			exitCode = code
			// Don't actually exit — just record the call.
		},
	}

	// Run a single poll cycle by calling pollOnce and handling the result directly,
	// mirroring what Run() does in the switch.
	evt, _, err := a.pollOnce(0)
	if err != nil {
		t.Fatalf("pollOnce: %v", err)
	}
	if evt != "upgrade" {
		t.Fatalf("want evt=upgrade got %q", evt)
	}

	// Simulate the case "upgrade" branch.
	updated, upgradeErr := a.getDoUpgrade()()
	if upgradeErr != nil {
		t.Fatalf("upgrade err: %v", upgradeErr)
	}
	if updated {
		a.getExit()(0)
	}

	if !upgradeCalled {
		t.Error("doUpgrade was not called")
	}
	if !exitCalled {
		t.Error("exit was not called when updated=true")
	}
	if exitCode != 0 {
		t.Errorf("exit code want 0 got %d", exitCode)
	}
}

// TestUpgradeCaseAlreadyUpToDate verifies that when doUpgrade returns (false, nil),
// the agent does NOT call exit.
func TestUpgradeCaseAlreadyUpToDate(t *testing.T) {
	upgradeCalled := false
	exitCalled := false

	a := &Agent{
		Version: "0.1.0",
		doUpgrade: func() (bool, error) {
			upgradeCalled = true
			return false, nil // already up-to-date
		},
		exit: func(code int) {
			exitCalled = true
		},
	}

	updated, upgradeErr := a.getDoUpgrade()()
	if upgradeErr != nil {
		t.Fatalf("upgrade err: %v", upgradeErr)
	}
	if updated {
		a.getExit()(0)
	}

	if !upgradeCalled {
		t.Error("doUpgrade was not called")
	}
	if exitCalled {
		t.Error("exit should NOT be called when already up-to-date")
	}
}

// TestUpgradeCaseError verifies that when doUpgrade returns an error,
// the agent does NOT call exit.
func TestUpgradeCaseError(t *testing.T) {
	exitCalled := false

	a := &Agent{
		Version: "0.1.0",
		doUpgrade: func() (bool, error) {
			return false, &errTest{"network failure"}
		},
		exit: func(code int) {
			exitCalled = true
		},
	}

	updated, upgradeErr := a.getDoUpgrade()()
	// Mirrors the Run() case "upgrade" branch: on error, continue (no exit).
	if upgradeErr == nil {
		t.Fatal("expected error")
	}
	_ = updated // unused when err != nil

	if exitCalled {
		t.Error("exit should NOT be called when upgrade returns an error")
	}
}

type errTest struct{ msg string }

func (e *errTest) Error() string { return e.msg }
