import pytest
from cockpit.inventory import load_inventory, InventoryError

VALID = """
machines:
  mac: { host: 1.2.3.4, ssh_user: curtis, local: true }
  box: { host: 5.6.7.8, ssh_user: root }
software:
  - name: claude-code
    kind: npm
    latest_source: "npm:@anthropic-ai/claude-code"
    changelog: "github:anthropics/claude-code"
    installs:
      - machine: mac
        current_cmd: "claude --version"
        update: { type: command, cmd: "npm i -g @anthropic-ai/claude-code@latest" }
  - name: multica
    kind: custom
    latest_source: "github:o/multica"
    changelog: "github:o/multica"
    installs:
      - machine: box
        current_cmd: "docker inspect multica --format '{{.Config.Labels.version}}'"
        update:
          type: agent
          runner: codex_exec
          cwd: /srv/multica
          prompt: "update multica to {latest_version}"
"""


def test_load_valid(tmp_path):
    p = tmp_path / "inv.yaml"
    p.write_text(VALID)
    inv = load_inventory(p)
    assert set(inv.machines) == {"mac", "box"}
    assert inv.software[0].installs[0].update.type == "command"
    assert inv.software[1].installs[0].update.runner == "codex_exec"


def test_unknown_machine_ref(tmp_path):
    bad = VALID.replace("machine: box", "machine: ghost")
    p = tmp_path / "inv.yaml"
    p.write_text(bad)
    with pytest.raises(InventoryError, match="ghost"):
        load_inventory(p)


def test_agent_requires_runner_and_prompt(tmp_path):
    bad = """
machines: { mac: { host: 1.2.3.4, ssh_user: curtis } }
software:
  - name: x
    kind: custom
    latest_source: "github:o/x"
    changelog: null
    installs:
      - machine: mac
        current_cmd: "x --version"
        update: { type: agent, runner: codex_exec }
"""
    p = tmp_path / "inv.yaml"
    p.write_text(bad)
    with pytest.raises(InventoryError, match="prompt"):
        load_inventory(p)
