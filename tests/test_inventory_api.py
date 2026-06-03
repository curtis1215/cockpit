from fastapi.testclient import TestClient
from cockpit import db
from cockpit.web.app import create_app
from cockpit.models import Machine, Update, Install, Software, Inventory

VALID2 = """
machines:
  mac: { host: 1.2.3.4, ssh_user: curtis, local: true, agent_token: tok-mac }
  box: { host: 5.6.7.8, ssh_user: root, agent_token: tok-box }
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


def _inv():
    mac = Machine(name="mac", host="x", ssh_user="curtis", local=True, agent_token="tok-mac")
    sw = Software(name="cc", kind="npm", latest_source="npm:cc", changelog=None,
                  installs=[Install(machine="mac", current_cmd="cc --version",
                                    update=Update(type="command", cmd="npm i -g cc@latest"))])
    return Inventory(machines={"mac": mac}, software=[sw])


def _app(tmp_path, with_path=True):
    c = db.connect(tmp_path / "c.db"); db.init_db(c)
    invfile = tmp_path / "inv.yaml"
    invfile.write_text("machines:\n  mac: { host: x, ssh_user: curtis, local: true }\n"
                       "software: []\n")
    path = str(invfile) if with_path else None
    return create_app(c, _inv(), inventory_path=path), c, invfile


def test_get_inventory(tmp_path):
    app, _, invfile = _app(tmp_path)
    r = TestClient(app).get("/api/inventory")
    assert r.status_code == 200
    assert "machines:" in r.text


def test_put_inventory_valid_hot_reloads(tmp_path):
    app, c, invfile = _app(tmp_path)
    cl = TestClient(app)
    r = cl.put("/api/inventory", content=VALID2)
    assert r.status_code == 200
    body = r.json()
    assert set(body["machines"]) == {"mac", "box"}
    # hot-reload: /api/machines (browser BFF) now reflects the new machine set
    assert set(cl.get("/api/machines").json()) == {"mac", "box"}
    # file persisted
    assert "box" in invfile.read_text()


def test_put_inventory_invalid_400(tmp_path):
    app, _, _ = _app(tmp_path)
    r = TestClient(app).put("/api/inventory", content="not: [valid yaml")
    assert r.status_code == 400


def test_put_inventory_no_path_409(tmp_path):
    app, _, _ = _app(tmp_path, with_path=False)
    r = TestClient(app).put("/api/inventory", content=VALID2)
    assert r.status_code == 409
