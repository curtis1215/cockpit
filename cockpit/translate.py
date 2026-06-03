from __future__ import annotations
import subprocess

_PROMPT = (
    "你是技術翻譯。把以下軟體 changelog 整理成繁體中文重點摘要，"
    "用條列列出重要變更（新功能/修正/安全/破壞性變更），精簡不逐字翻。\n\n"
    "---\n{raw}\n---"
)


def translate_changelog(raw: str | None, timeout: int = 120) -> str | None:
    if not raw or not raw.strip():
        return None
    prompt = _PROMPT.format(raw=raw)
    try:
        res = subprocess.run(["claude", "-p", prompt], capture_output=True,
                             text=True, timeout=timeout)
    except Exception:
        return None
    if res.returncode != 0:
        return None
    out = (res.stdout or "").strip()
    return out or None
