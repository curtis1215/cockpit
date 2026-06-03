from __future__ import annotations
from pathlib import Path
import yaml

from cockpit.models import Machine, Update, Install, Software, Inventory


class InventoryError(ValueError):
    pass


def _parse_update(raw: dict, ctx: str) -> Update:
    if not isinstance(raw, dict) or "type" not in raw:
        raise InventoryError(f"{ctx}: update 缺少 type")
    t = raw["type"]
    if t == "command":
        if not raw.get("cmd"):
            raise InventoryError(f"{ctx}: command 型 update 需要 cmd")
        return Update(type="command", cmd=raw["cmd"])
    if t == "agent":
        if not raw.get("runner"):
            raise InventoryError(f"{ctx}: agent 型 update 需要 runner")
        if not raw.get("prompt"):
            raise InventoryError(f"{ctx}: agent 型 update 需要 prompt")
        return Update(
            type="agent", runner=raw["runner"], prompt=raw["prompt"],
            machine=raw.get("machine"), cwd=raw.get("cwd"), invoke=raw.get("invoke"),
        )
    raise InventoryError(f"{ctx}: 未知 update.type={t!r}")


def load_inventory_text(text: str) -> Inventory:
    try:
        data = yaml.safe_load(text)
    except yaml.YAMLError as e:
        raise InventoryError(f"YAML 解析失敗: {e}")
    if not isinstance(data, dict):
        raise InventoryError("inventory 根節點必須是 mapping")

    machines: dict[str, Machine] = {}
    for name, m in (data.get("machines") or {}).items():
        if not isinstance(m, dict):
            raise InventoryError(f"machine {name}: 定義必須是 mapping")
        if "host" not in m or "ssh_user" not in m:
            raise InventoryError(f"machine {name}: 需要 host 與 ssh_user")
        machines[name] = Machine(name=name, host=m["host"], ssh_user=m["ssh_user"],
                                 local=bool(m.get("local", False)),
                                 agent_token=m.get("agent_token"))

    software: list[Software] = []
    for sw in (data.get("software") or []):
        name = sw.get("name")
        if not name:
            raise InventoryError("software 條目缺少 name")
        if not sw.get("latest_source"):
            raise InventoryError(f"software {name}: 需要 latest_source")
        installs: list[Install] = []
        for i, inst in enumerate(sw.get("installs") or []):
            ctx = f"software {name} install[{i}]"
            mach = inst.get("machine")
            if mach not in machines:
                raise InventoryError(f"{ctx}: 參照未知 machine {mach!r}")
            if not inst.get("current_cmd"):
                raise InventoryError(f"{ctx}: 需要 current_cmd")
            update = _parse_update(inst.get("update"), ctx)
            installs.append(Install(machine=mach, current_cmd=inst["current_cmd"],
                                    update=update, version_regex=inst.get("version_regex")))
        software.append(Software(name=name, kind=sw.get("kind", "custom"),
                                 latest_source=sw["latest_source"],
                                 changelog=sw.get("changelog"), installs=installs))
    return Inventory(machines=machines, software=software)


def load_inventory(path: str | Path) -> Inventory:
    return load_inventory_text(Path(path).read_text())


def machine_for_token(inv: Inventory, token: str) -> str | None:
    if not token:
        return None
    for name, m in inv.machines.items():
        if m.agent_token and m.agent_token == token:
            return name
    return None
