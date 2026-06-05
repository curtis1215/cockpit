#!/bin/sh
set -e

# cockpit one-line installer
# Usage: curl -fsSL https://raw.githubusercontent.com/curtis1215/cockpit/main/install.sh | sh
# Override repo with:  COCKPIT_REPO=owner/repo sh install.sh

REPO="${COCKPIT_REPO:-curtis1215/cockpit}"

# ── 偵測 OS ──────────────────────────────────────────────────────────────────
_os=$(uname -s)
case "$_os" in
  Darwin) OS="darwin" ;;
  Linux)  OS="linux"  ;;
  *)
    echo "不支援的作業系統：$_os" >&2
    exit 1
    ;;
esac

# ── 偵測 ARCH ─────────────────────────────────────────────────────────────────
_arch=$(uname -m)
case "$_arch" in
  x86_64)          ARCH="amd64" ;;
  arm64|aarch64)   ARCH="arm64" ;;
  *)
    echo "不支援的 CPU 架構：$_arch" >&2
    exit 1
    ;;
esac

# ── 取得最新 release tag ──────────────────────────────────────────────────────
echo "查詢最新版本（repo: ${REPO}）..."
TAG=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
  | grep '"tag_name"' \
  | cut -d'"' -f4)

if [ -z "$TAG" ]; then
  echo "無法取得最新 release tag，請確認 repo 是否存在或網路是否正常。" >&2
  exit 1
fi

# Strip leading "v" to get bare version number
VER="${TAG#v}"

echo "最新版本：${TAG}（${OS}/${ARCH}）"

# ── 組合下載 URL ───────────────────────────────────────────────────────────────
FILENAME="cockpit_${VER}_${OS}_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${TAG}/${FILENAME}"

# ── 下載並解壓縮 ───────────────────────────────────────────────────────────────
TMP_DIR=$(mktemp -d)
trap 'rm -rf "$TMP_DIR"' EXIT

echo "下載中：${URL}"
curl -fsSL -o "${TMP_DIR}/${FILENAME}" "${URL}"
tar -xzf "${TMP_DIR}/${FILENAME}" -C "${TMP_DIR}"

# ── 安裝目標路徑 ───────────────────────────────────────────────────────────────
BIN_DIR="/usr/local/bin"

# 若 /usr/local/bin 不可寫且沒有 sudo，改用 ~/.local/bin
if [ ! -w "$BIN_DIR" ] && ! sudo -n true 2>/dev/null; then
  BIN_DIR="${HOME}/.local/bin"
  mkdir -p "$BIN_DIR"
  echo "注意：/usr/local/bin 不可寫，安裝至 ${BIN_DIR}"
  echo "請確認 ${BIN_DIR} 已在您的 PATH 中。"
fi

# ── 複製 binary ───────────────────────────────────────────────────────────────
BINARY="${TMP_DIR}/cockpit"
if [ ! -f "$BINARY" ]; then
  echo "解壓縮後找不到 cockpit binary，請回報此問題。" >&2
  exit 1
fi

if [ -w "$BIN_DIR" ]; then
  install -m755 "$BINARY" "${BIN_DIR}/cockpit"
else
  sudo install -m755 "$BINARY" "${BIN_DIR}/cockpit"
fi

# ── 完成 ──────────────────────────────────────────────────────────────────────
echo ""
echo "✅ cockpit 安裝完成！"
"${BIN_DIR}/cockpit" version 2>/dev/null || true
echo ""

# ── 處理傳入的子命令參數 ────────────────────────────────────────────────────────
# 支援：curl ... | sh -s -- serve
#        curl ... | sh -s -- agent <server_url> <enroll_token>
SUBCMD="${1:-}"

_run_as_root() {
  if [ "$(id -u)" = "0" ]; then
    "$@"
  elif command -v sudo >/dev/null 2>&1; then
    sudo "$@"
  else
    echo "⚠️  需要 root 權限，但找不到 sudo。請以 root 身份手動執行：" >&2
    echo "  $*" >&2
    return 1
  fi
}

case "$SUBCMD" in
  serve)
    echo "🔧 執行：cockpit setup serve …"
    _run_as_root "${BIN_DIR}/cockpit" setup serve
    ;;
  agent)
    AGENT_SERVER="${2:-}"
    AGENT_TOKEN="${3:-}"
    if [ -z "$AGENT_SERVER" ] || [ -z "$AGENT_TOKEN" ]; then
      echo "❌ 用法：curl ... | sh -s -- agent <server_url> <enroll_token>" >&2
      exit 1
    fi
    echo "🔧 執行：cockpit setup agent …"
    _run_as_root "${BIN_DIR}/cockpit" setup agent -server "$AGENT_SERVER" -token "$AGENT_TOKEN"
    ;;
  "")
    echo "下一步："
    echo "  一鍵設定控制台  — curl -fsSL .../install.sh | sh -s -- serve"
    echo "  一鍵設定 agent  — curl -fsSL .../install.sh | sh -s -- agent <server_url> <token>"
    echo ""
    echo "或手動執行："
    echo "  sudo cockpit setup serve"
    echo "  sudo cockpit setup agent -server <url> -token <token>"
    echo "  upgrade — 自動更新至最新版本：cockpit upgrade"
    ;;
  *)
    echo "❌ 未知的子命令：$SUBCMD（支援 serve / agent）" >&2
    exit 1
    ;;
esac
