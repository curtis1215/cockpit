from cockpit.inventory import load_inventory_text, machine_for_token, InventoryError

INV = """
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
"""


def test_load_text_and_tokens():
    inv = load_inventory_text(INV)
    assert inv.machines["mac"].agent_token == "tok-mac"
    assert machine_for_token(inv, "tok-box") == "box"
    assert machine_for_token(inv, "nope") is None


def test_bad_text_raises():
    import pytest
    with pytest.raises(InventoryError):
        load_inventory_text("not: [a, mapping")
