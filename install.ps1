#Requires -Version 5.1
<#
.SYNOPSIS
  cockpit Windows 安裝腳本（對齊 install.sh 的 serve/agent 流程）。

.DESCRIPTION
  偵測架構 → 查詢最新 release → 下載 windows zip → 解壓 → 安裝 cockpit.exe
  → 視子命令執行 `cockpit setup serve|agent`（含 Windows 服務註冊）。

  binary 安裝位置：系統管理員 → %ProgramFiles%\cockpit；否則 → %LOCALAPPDATA%\cockpit
  設定/資料目錄（-Dir）預設：%ProgramData%\cockpit（Windows 服務以 LocalSystem 跑得可讀）

.NOTES
  安裝系統服務（serve / 不帶 -NoService 的 agent）需「以系統管理員身分」開啟 PowerShell。
  ⚠️ 此腳本尚未經 Windows 實機驗證；service 安裝路徑請對照 README 的注意事項。

.EXAMPLE
  # 下載後執行（要傳參數時用這種）
  irm https://raw.githubusercontent.com/curtis1215/cockpit/main/install.ps1 -OutFile install.ps1
  .\install.ps1 -Subcommand agent -Server http://192.168.1.10:8787 -Token ck_enroll_xxxxxxxx

.EXAMPLE
  # 僅安裝 binary（不帶子命令）
  irm https://raw.githubusercontent.com/curtis1215/cockpit/main/install.ps1 | iex
#>
[CmdletBinding()]
param(
    [ValidateSet('serve', 'agent', '')]
    [string]$Subcommand = '',
    [string]$Server,
    [string]$Token,
    [string]$Dir = "$env:ProgramData\cockpit",
    [switch]$NoService
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'  # 關閉 Invoke-WebRequest 進度條（大幅加速）
try { [Console]::OutputEncoding = [System.Text.Encoding]::UTF8 } catch { }  # 讓繁中訊息在終端正確顯示

$Repo = if ($env:COCKPIT_REPO) { $env:COCKPIT_REPO } else { 'curtis1215/cockpit' }

# ── 偵測 ARCH ────────────────────────────────────────────────────────────────
$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { 'amd64' }
    'ARM64' { 'arm64' }
    default { throw "不支援的 CPU 架構：$env:PROCESSOR_ARCHITECTURE（僅支援 AMD64 / ARM64）" }
}

# ── 是否系統管理員 ───────────────────────────────────────────────────────────
$isAdmin = ([Security.Principal.WindowsPrincipal][Security.Principal.WindowsIdentity]::GetCurrent()
).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)

# ── 取得最新 release tag ──────────────────────────────────────────────────────
$headers = @{ 'User-Agent' = 'cockpit-installer' }
Write-Host "查詢最新版本（repo: $Repo）..."
$rel = Invoke-RestMethod -Uri "https://api.github.com/repos/$Repo/releases/latest" -Headers $headers
$tag = $rel.tag_name
if (-not $tag) { throw "無法取得最新 release tag，請確認 repo 是否存在或網路是否正常。" }
$ver = $tag -replace '^v', ''
Write-Host "最新版本：$tag（windows/$arch）"

# ── 下載並解壓縮 ─────────────────────────────────────────────────────────────
$filename = "cockpit_${ver}_windows_${arch}.zip"
$url = "https://github.com/$Repo/releases/download/$tag/$filename"
$tmp = Join-Path ([System.IO.Path]::GetTempPath()) ("cockpit_" + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $tmp -Force | Out-Null

try {
    $zip = Join-Path $tmp $filename
    Write-Host "下載中：$url"
    Invoke-WebRequest -Uri $url -OutFile $zip -Headers $headers
    Expand-Archive -Path $zip -DestinationPath $tmp -Force

    $binary = Join-Path $tmp 'cockpit.exe'
    if (-not (Test-Path $binary)) { throw "解壓縮後找不到 cockpit.exe，請回報此問題。" }

    # ── 安裝目標路徑 ─────────────────────────────────────────────────────────
    if ($isAdmin) {
        $binDir = Join-Path $env:ProgramFiles 'cockpit'
    }
    else {
        $binDir = Join-Path $env:LOCALAPPDATA 'cockpit'
        Write-Host "注意：未以系統管理員執行，安裝至 $binDir（無法安裝系統服務）。"
    }
    New-Item -ItemType Directory -Path $binDir -Force | Out-Null
    $dest = Join-Path $binDir 'cockpit.exe'
    Copy-Item -Path $binary -Destination $dest -Force

    # ── 加入使用者 PATH（若尚未存在）─────────────────────────────────────────
    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if (-not $userPath) { $userPath = '' }
    if (($userPath -split ';') -notcontains $binDir) {
        $newPath = if ($userPath) { "$userPath;$binDir" } else { $binDir }
        [Environment]::SetEnvironmentVariable('Path', $newPath, 'User')
        Write-Host "已將 $binDir 加入使用者 PATH（重開終端機後對新工作階段生效）。"
    }
    $env:Path = "$env:Path;$binDir"

    Write-Host ''
    Write-Host '✅ cockpit 安裝完成！'
    & $dest version 2>$null
    Write-Host ''

    # ── 處理子命令 ───────────────────────────────────────────────────────────
    switch ($Subcommand) {
        'serve' {
            if (-not $isAdmin -and -not $NoService) {
                throw '安裝控制台服務需以系統管理員身分執行 PowerShell（或加 -NoService 僅產生設定）。'
            }
            Write-Host '🔧 執行：cockpit setup serve …'
            $setupArgs = @('setup', 'serve', '-dir', $Dir)
            if ($NoService) { $setupArgs += '-no-service' }
            & $dest @setupArgs
        }
        'agent' {
            if (-not $Server -or -not $Token) {
                throw '用法：.\install.ps1 -Subcommand agent -Server <server_url> -Token <enroll_token>'
            }
            if (-not $isAdmin -and -not $NoService) {
                throw '安裝 agent 服務需以系統管理員身分執行 PowerShell（或加 -NoService 僅產生設定）。'
            }
            Write-Host '🔧 執行：cockpit setup agent …'
            $setupArgs = @('setup', 'agent', '-server', $Server, '-token', $Token, '-dir', $Dir)
            if ($NoService) { $setupArgs += '-no-service' }
            & $dest @setupArgs
        }
        default {
            Write-Host '下一步：'
            Write-Host '  設定控制台  — .\install.ps1 -Subcommand serve'
            Write-Host '  設定 agent  — .\install.ps1 -Subcommand agent -Server <url> -Token <token>'
            Write-Host ''
            Write-Host '或手動執行（系統管理員 PowerShell）：'
            Write-Host "  cockpit setup agent -server <url> -token <token> -dir $Dir"
            Write-Host '  cockpit upgrade   # 自動更新至最新版本'
        }
    }
}
finally {
    Remove-Item -Path $tmp -Recurse -Force -ErrorAction SilentlyContinue
}
