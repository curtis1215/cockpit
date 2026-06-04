package inventory

import (
	"reflect"
	"testing"
)

const inv = `
machines:
  mac:  { host: 1.2.3.4, ssh_user: curtis, local: true, agent_token: tok-mac }
  box:  { host: 5.6.7.8, ssh_user: root, agent_token: tok-box }
software:
  - name: cc
    kind: npm
    latest_source: "npm:cc"
    changelog: null
    installs:
      - machine: mac
        current_cmd: "cc --version"
        update: { type: command, cmd: "npm i -g cc@latest" }
  - name: multica
    kind: custom
    latest_source: "github:o/multica"
    changelog: "github:o/multica"
    installs:
      - machine: box
        current_cmd: "docker inspect"
        update: { type: agent, runner: codex_exec, cwd: /srv/multica, prompt: "update to {latest_version}" }
`

func TestLoad(t *testing.T) {
	iv, err := LoadText([]byte(inv))
	if err != nil {
		t.Fatal(err)
	}
	if iv.Machines["mac"].AgentToken != "tok-mac" || !iv.Machines["mac"].Local {
		t.Fatalf("mac: %+v", iv.Machines["mac"])
	}
	if iv.Software[0].Installs[0].Update.Type != "command" || iv.Software[1].Installs[0].Update.Runner != "codex_exec" {
		t.Fatalf("software: %+v", iv.Software)
	}
	if MachineForToken(iv, "tok-box") != "box" || MachineForToken(iv, "nope") != "" {
		t.Fatal("token resolve")
	}
}

func TestValidation(t *testing.T) {
	bad := "machines: { mac: { host: x } }\nsoftware: []\n" // 缺 ssh_user
	if _, err := LoadText([]byte(bad)); err == nil {
		t.Fatal("want error for missing ssh_user")
	}
	// P3 起 install.machine 可指向 DB 管理的機器（不必在 inventory.machines）——僅要求非空。
	ok2 := `
machines: { mac: { host: x, ssh_user: c } }
software:
  - name: s
    latest_source: "github:o/s"
    installs:
      - machine: ghost
        current_cmd: "x"
        update: { type: command, cmd: "y" }
`
	if _, err := LoadText([]byte(ok2)); err != nil {
		t.Fatalf("db-managed machine ref should load: %v", err)
	}
	bad2 := `
machines: {}
software:
  - name: s
    latest_source: "x"
    installs:
      - machine: ""
        current_cmd: "x"
        update: { type: command, cmd: "y" }
`
	if _, err := LoadText([]byte(bad2)); err == nil {
		t.Fatal("want error for empty machine")
	}
}

// TestMarshalRoundTrip verifies Marshal → LoadText produces an identical Inventory.
func TestMarshalRoundTrip(t *testing.T) {
	// Fixture covers: command + agent update types, version_regex, agent_token, changelog.
	const fixture = `
machines:
  mac:  { host: 1.2.3.4, ssh_user: curtis, local: true, agent_token: tok-mac }
  box:  { host: 5.6.7.8, ssh_user: root }
software:
  - name: cc
    kind: npm
    latest_source: "npm:cc"
    installs:
      - machine: mac
        current_cmd: "cc --version"
        version_regex: "v(\\d+\\.\\d+)"
        update: { type: command, cmd: "npm i -g cc@latest" }
  - name: multica
    kind: custom
    latest_source: "github:o/multica"
    changelog: "github:o/multica"
    installs:
      - machine: box
        current_cmd: "docker inspect multica --format version"
        update: { type: agent, runner: codex_exec, prompt: "update to latest", cwd: /srv/multica }
`
	orig, err := LoadText([]byte(fixture))
	if err != nil {
		t.Fatalf("load orig: %v", err)
	}
	b, err := Marshal(orig)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	got, err := LoadText(b)
	if err != nil {
		t.Fatalf("load marshaled: %v", err)
	}
	if !reflect.DeepEqual(orig, got) {
		t.Fatalf("round-trip mismatch\norig: %+v\ngot:  %+v", orig, got)
	}
}
