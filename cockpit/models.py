from __future__ import annotations
from dataclasses import dataclass, field


@dataclass
class Machine:
    name: str
    host: str
    ssh_user: str
    local: bool = False


@dataclass
class Update:
    type: str                      # "command" | "agent"
    cmd: str | None = None         # command 型
    runner: str | None = None      # agent 型: codex_exec | claude_p | custom
    prompt: str | None = None
    machine: str | None = None     # agent 跑在哪台（預設同 install.machine）
    cwd: str | None = None
    invoke: str | None = None      # custom runner 的指令模板


@dataclass
class Install:
    machine: str
    current_cmd: str
    update: Update
    version_regex: str | None = None


@dataclass
class Software:
    name: str
    kind: str
    latest_source: str
    changelog: str | None
    installs: list[Install] = field(default_factory=list)


@dataclass
class Inventory:
    machines: dict[str, Machine]
    software: list[Software]
