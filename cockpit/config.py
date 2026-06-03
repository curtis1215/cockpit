from __future__ import annotations
import os
from dataclasses import dataclass


@dataclass
class Settings:
    db_path: str
    inventory_path: str
    check_hours: int

    @classmethod
    def from_env(cls) -> "Settings":
        return cls(
            db_path=os.environ.get("COCKPIT_DB_PATH", "cockpit.db"),
            inventory_path=os.environ.get("COCKPIT_INVENTORY", "inventory.yaml"),
            check_hours=int(os.environ.get("COCKPIT_CHECK_HOURS", "24")),
        )
