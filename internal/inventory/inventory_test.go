package inventory

import "testing"

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
	bad2 := `
machines: { mac: { host: x, ssh_user: c } }
software:
  - name: s
    latest_source: "github:o/s"
    installs:
      - machine: ghost
        current_cmd: "x"
        update: { type: command, cmd: "y" }
`
	if _, err := LoadText([]byte(bad2)); err == nil {
		t.Fatal("want error for unknown machine ref")
	}
}
