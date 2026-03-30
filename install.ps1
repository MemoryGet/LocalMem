# IClude 统一安装入口 (Windows) / Unified installer (Claude Code + Codex CLI)
# Usage: irm https://raw.githubusercontent.com/MemoryGet/LocalMem/main/install.ps1 | iex
# 单平台安装 / Single platform:
#   irm .../integrations/claude/install.ps1 | iex
#   irm .../integrations/codex/install.ps1 | iex

$ErrorActionPreference = "Stop"
$SCRIPT_URL_BASE = "https://raw.githubusercontent.com/MemoryGet/LocalMem/main/integrations"

Write-Host ""
Write-Host "  IClude Memory System - Unified Installer"
Write-Host "  =========================================="
Write-Host ""
Write-Host "  Select which AI coding tools to integrate:"
Write-Host ""
Write-Host "    1) Claude Code only"
Write-Host "    2) Codex CLI only"
Write-Host "    3) Both (recommended)"
Write-Host ""

$choice = Read-Host "  Enter choice [3]"
if ([string]::IsNullOrEmpty($choice)) { $choice = "3" }

$scriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path

function Run-Claude {
    $localScript = Join-Path $scriptDir "integrations\claude\install.ps1"
    if (Test-Path $localScript) {
        & $localScript
    } else {
        Write-Host "[INFO] Downloading Claude Code installer..." -ForegroundColor Green
        $script = Invoke-RestMethod "$SCRIPT_URL_BASE/claude/install.ps1"
        Invoke-Expression $script
    }
}

function Run-Codex {
    $localScript = Join-Path $scriptDir "integrations\codex\install.ps1"
    if (Test-Path $localScript) {
        & $localScript
    } else {
        Write-Host "[INFO] Downloading Codex CLI installer..." -ForegroundColor Green
        $script = Invoke-RestMethod "$SCRIPT_URL_BASE/codex/install.ps1"
        Invoke-Expression $script
    }
}

switch ($choice) {
    "1" { Run-Claude }
    "2" { Run-Codex }
    "3" {
        Run-Claude
        Write-Host ""
        Write-Host "[INFO] Now configuring Codex CLI..." -ForegroundColor Green
        Write-Host ""
        Run-Codex
    }
    default {
        Write-Host "[WARN] Invalid choice: $choice, defaulting to both" -ForegroundColor Yellow
        Run-Claude
        Run-Codex
    }
}
