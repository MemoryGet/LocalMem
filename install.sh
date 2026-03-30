#!/bin/bash
set -euo pipefail

# IClude 统一安装入口 / Unified installer (Claude Code + Codex CLI)
# Usage: curl -fsSL https://raw.githubusercontent.com/MemoryGet/LocalMem/main/install.sh | bash
# 单平台安装 / Single platform:
#   curl -fsSL .../integrations/claude/install.sh | bash
#   curl -fsSL .../integrations/codex/install.sh | bash

SCRIPT_URL_BASE="https://raw.githubusercontent.com/MemoryGet/LocalMem/main/integrations"

RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
info()  { echo -e "${GREEN}[INFO]${NC} $1"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $1"; }

echo ""
echo "  IClude Memory System — Unified Installer"
echo "  =========================================="
echo ""
echo "  Select which AI coding tools to integrate:"
echo ""
echo -e "    ${CYAN}1${NC}) Claude Code only"
echo -e "    ${CYAN}2${NC}) Codex CLI only"
echo -e "    ${CYAN}3${NC}) Both (recommended)"
echo ""

CHOICE="3"
if [ -t 0 ]; then
    echo -n "  Enter choice [3]: "
    read -r input
    if [ -n "$input" ]; then
        CHOICE="$input"
    fi
fi

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

run_claude() {
    if [ -f "$SCRIPT_DIR/integrations/claude/install.sh" ]; then
        bash "$SCRIPT_DIR/integrations/claude/install.sh" "$@"
    else
        info "Downloading Claude Code installer..."
        curl -fsSL "${SCRIPT_URL_BASE}/claude/install.sh" | bash -s -- "$@"
    fi
}

run_codex() {
    if [ -f "$SCRIPT_DIR/integrations/codex/install.sh" ]; then
        bash "$SCRIPT_DIR/integrations/codex/install.sh" "$@"
    else
        info "Downloading Codex CLI installer..."
        curl -fsSL "${SCRIPT_URL_BASE}/codex/install.sh" | bash -s -- "$@"
    fi
}

case "$CHOICE" in
    1) run_claude "$@" ;;
    2) run_codex "$@" ;;
    3)
        run_claude "$@"
        echo ""
        info "Now configuring Codex CLI..."
        echo ""
        run_codex "$@"
        ;;
    *)
        warn "Invalid choice: $CHOICE, defaulting to both"
        run_claude "$@"
        run_codex "$@"
        ;;
esac
