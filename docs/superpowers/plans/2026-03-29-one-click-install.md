# One-Click Install Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Let users install IClude on any machine with a single `curl | bash` (or PowerShell) command — auto-download binaries, generate config, configure Claude Code hooks + MCP stdio.

**Architecture:** MCP Server gains `--stdio` flag for stdin/stdout JSON-RPC transport. Cross-compiled binaries published to GitHub Releases. Install scripts download binaries, generate config, wire Claude Code integration.

**Tech Stack:** Go 1.25+ (cross-compile), bash, PowerShell, GitHub Actions

**Design Spec:** `docs/superpowers/specs/2026-03-29-one-click-install-design.md`

---

## File Structure

### New files

| File | Responsibility |
|------|---------------|
| `internal/mcp/stdio.go` | stdio 传输层：stdin → HandleRequest → stdout 循环 |
| `Makefile` | 交叉编译 12 个平台二进制 |
| `install.sh` | Linux/macOS 安装脚本 |
| `install.ps1` | Windows PowerShell 安装脚本 |
| `.github/workflows/release.yml` | Tag 推送时自动编译发布 |
| `testing/mcp/stdio_test.go` | stdio 传输层测试 |

### Modified files

| File | Change |
|------|--------|
| `cmd/mcp/main.go` | 加 `--stdio` 和 `--config` flag，分支到 stdio 模式 |

---

## Task 1: MCP stdio 传输层

**Files:**
- Create: `internal/mcp/stdio.go`
- Create: `testing/mcp/stdio_test.go`

- [ ] **Step 1: Write failing test**

```go
// testing/mcp/stdio_test.go
package mcp_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"iclude/internal/mcp"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStdioTransport_InitializeAndToolsList(t *testing.T) {
	// 构建 registry with a dummy tool
	reg := mcp.NewRegistry()
	reg.RegisterTool(&mockToolHandler{
		def: mcp.ToolDefinition{
			Name:        "test_tool",
			Description: "A test tool",
			InputSchema: json.RawMessage(`{"type":"object"}`),
		},
	})

	identity := &model.Identity{TeamID: "default", OwnerID: "test"}

	// 构造 stdin：initialize 请求 + tools/list 请求，每行一个 JSON
	requests := []map[string]any{
		{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "test", "version": "1.0"},
		}},
		{"jsonrpc": "2.0", "id": 2, "method": "tools/list"},
	}
	var stdinBuf bytes.Buffer
	for _, req := range requests {
		line, _ := json.Marshal(req)
		stdinBuf.Write(line)
		stdinBuf.WriteByte('\n')
	}

	var stdoutBuf bytes.Buffer

	err := mcp.RunStdio(context.Background(), reg, identity, &stdinBuf, &stdoutBuf)
	assert.NoError(t, err)

	// 解析 stdout 响应
	lines := strings.Split(strings.TrimSpace(stdoutBuf.String()), "\n")
	require.GreaterOrEqual(t, len(lines), 2)

	// 验证 initialize 响应
	var initResp mcp.JSONRPCResponse
	require.NoError(t, json.Unmarshal([]byte(lines[0]), &initResp))
	assert.Nil(t, initResp.Error)

	// 验证 tools/list 响应
	var listResp mcp.JSONRPCResponse
	require.NoError(t, json.Unmarshal([]byte(lines[1]), &listResp))
	assert.Nil(t, listResp.Error)
}

func TestStdioTransport_ToolCall(t *testing.T) {
	reg := mcp.NewRegistry()
	reg.RegisterTool(&mockToolHandler{
		def: mcp.ToolDefinition{
			Name:        "echo",
			Description: "Echo tool",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"msg":{"type":"string"}}}`),
		},
		execFn: func(ctx context.Context, args json.RawMessage) (*mcp.ToolResult, error) {
			return mcp.TextResult("hello"), nil
		},
	})

	identity := &model.Identity{TeamID: "default", OwnerID: "test"}

	req := map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/call",
		"params": map[string]any{"name": "echo", "arguments": map[string]any{"msg": "hi"}},
	}
	line, _ := json.Marshal(req)
	stdin := bytes.NewBuffer(append(line, '\n'))
	var stdout bytes.Buffer

	err := mcp.RunStdio(context.Background(), reg, identity, stdin, &stdout)
	assert.NoError(t, err)

	var resp mcp.JSONRPCResponse
	require.NoError(t, json.Unmarshal([]byte(strings.TrimSpace(stdout.String())), &resp))
	assert.Nil(t, resp.Error)
}
```

Note: `mockToolHandler` already exists in `testing/mcp/` from existing tests. If not, create one:

```go
type mockToolHandler struct {
	def    mcp.ToolDefinition
	execFn func(ctx context.Context, args json.RawMessage) (*mcp.ToolResult, error)
}

func (m *mockToolHandler) Definition() mcp.ToolDefinition { return m.def }
func (m *mockToolHandler) Execute(ctx context.Context, args json.RawMessage) (*mcp.ToolResult, error) {
	if m.execFn != nil {
		return m.execFn(ctx, args)
	}
	return mcp.TextResult("ok"), nil
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./testing/mcp/ -run TestStdioTransport -v`
Expected: FAIL (RunStdio not defined)

- [ ] **Step 3: Implement stdio transport**

```go
// internal/mcp/stdio.go
package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"iclude/internal/logger"
	"iclude/internal/model"

	"go.uber.org/zap"
)

// RunStdio 启动 stdio 传输层（阻塞直到 stdin EOF 或 ctx 取消）
// Runs stdio transport: reads JSON-RPC from reader, writes responses to writer.
// Blocks until reader EOF or ctx cancellation.
func RunStdio(ctx context.Context, registry *Registry, identity *model.Identity, reader io.Reader, writer io.Writer) error {
	session := NewSession("stdio", registry, identity)
	defer session.Close()

	var mu sync.Mutex // 保护 writer 的并发写入 / Protect concurrent writes to writer
	writeLine := func(data []byte) {
		mu.Lock()
		defer mu.Unlock()
		writer.Write(data)
		writer.Write([]byte("\n"))
	}

	// 异步转发 session.Out() 到 writer（处理通知等异步消息）
	// Forward session.Out() to writer asynchronously
	done := make(chan struct{})
	go func() {
		defer close(done)
		for msg := range session.Out() {
			writeLine(msg)
		}
	}()

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024) // 1MB 行缓冲 / 1MB line buffer
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req JSONRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			errResp := &JSONRPCResponse{
				JSONRPC: "2.0",
				Error:   &JSONRPCError{Code: -32700, Message: "parse error: " + err.Error()},
			}
			out, _ := json.Marshal(errResp)
			writeLine(out)
			continue
		}

		// 通知（无 ID）不需要响应 / Notifications (no ID) don't get responses
		if req.ID == nil || string(req.ID) == "null" {
			logger.Debug("stdio: received notification", zap.String("method", req.Method))
			continue
		}

		resp := session.HandleRequest(ctx, &req)
		if resp != nil {
			out, err := json.Marshal(resp)
			if err != nil {
				logger.Error("stdio: failed to marshal response", zap.Error(err))
				continue
			}
			writeLine(out)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("stdio scanner: %w", err)
	}

	session.Close()
	<-done
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./testing/mcp/ -run TestStdioTransport -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/stdio.go testing/mcp/stdio_test.go
git commit -m "feat(mcp): add stdio transport for Claude Code MCP integration"
```

---

## Task 2: MCP Server --stdio flag

**Files:**
- Modify: `cmd/mcp/main.go`

- [ ] **Step 1: Add flag parsing and stdio branch**

In `cmd/mcp/main.go`, replace `func main()` with flag-aware version:

```go
func main() {
	stdioMode := flag.Bool("stdio", false, "Run in stdio mode (JSON-RPC over stdin/stdout)")
	configPath := flag.String("config", "", "Path to config file (overrides default search)")
	flag.Parse()

	logger.InitLogger()
	defer logger.GetLogger().Sync()

	if *configPath != "" {
		os.Setenv("ICLUDE_CONFIG_PATH", *configPath)
	}

	if err := config.LoadConfig(); err != nil {
		logger.Fatal("failed to load config", zap.Error(err))
	}
	cfg := config.GetConfig()

	// stdio 模式不检查 mcp.enabled（由 Claude Code 直接拉起）
	if !*stdioMode && !cfg.MCP.Enabled {
		logger.Info("mcp server disabled, set mcp.enabled=true to enable")
		os.Exit(0)
	}

	deps, cleanup, err := bootstrap.Init(context.Background(), cfg)
	if err != nil {
		logger.Fatal("failed to initialize", zap.Error(err))
	}
	defer cleanup()

	// ... adapter + registry setup (existing code, unchanged) ...

	if *stdioMode {
		runStdioMode(cfg, reg)
		return
	}

	// ... existing HTTP+SSE server code ...
}

func runStdioMode(cfg config.Config, reg *mcp.Registry) {
	identity := &model.Identity{
		TeamID:  cfg.MCP.DefaultTeamID,
		OwnerID: cfg.MCP.DefaultOwnerID,
	}
	// stdio 模式日志输出到 stderr / In stdio mode, logs go to stderr
	logger.Info("mcp server starting in stdio mode")
	if err := mcp.RunStdio(context.Background(), reg, identity, os.Stdin, os.Stdout); err != nil {
		logger.Error("stdio transport error", zap.Error(err))
	}
}
```

Add `"flag"` to imports.

- [ ] **Step 2: Handle --config flag in config loading**

In `internal/config/config.go`, check `ICLUDE_CONFIG_PATH` env var before default search paths:

```go
// 在 LoadConfig 中，Viper 搜索路径之前加:
if envPath := os.Getenv("ICLUDE_CONFIG_PATH"); envPath != "" {
	viper.SetConfigFile(envPath)
}
```

- [ ] **Step 3: Verify build and both modes**

Run: `go build -o /tmp/iclude-mcp ./cmd/mcp/`
Expected: BUILD SUCCESS

Run: `echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}' | /tmp/iclude-mcp --stdio 2>/dev/null | head -1`
Expected: JSON response with `"result"` containing server capabilities

- [ ] **Step 4: Commit**

```bash
git add cmd/mcp/main.go internal/config/config.go
git commit -m "feat(mcp): add --stdio and --config flags to MCP server"
```

---

## Task 3: Makefile 交叉编译

**Files:**
- Create: `Makefile`

- [ ] **Step 1: Create Makefile**

```makefile
# IClude 构建目标 / IClude build targets
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -s -w -X main.version=$(VERSION)
DIST := dist

PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

.PHONY: build release clean

# 本地构建 / Local build
build:
	go build -ldflags "$(LDFLAGS)" -o $(DIST)/iclude-mcp ./cmd/mcp/
	go build -ldflags "$(LDFLAGS)" -o $(DIST)/iclude-cli ./cmd/cli/

# 交叉编译所有平台 / Cross-compile for all platforms
release: clean
	@mkdir -p $(DIST)
	@for platform in $(PLATFORMS); do \
		OS=$${platform%/*}; \
		ARCH=$${platform#*/}; \
		EXT=""; \
		[ "$$OS" = "windows" ] && EXT=".exe"; \
		echo "Building iclude-mcp-$$OS-$$ARCH$$EXT ..."; \
		CGO_ENABLED=0 GOOS=$$OS GOARCH=$$ARCH go build -ldflags "$(LDFLAGS)" \
			-o $(DIST)/iclude-mcp-$$OS-$$ARCH$$EXT ./cmd/mcp/; \
		echo "Building iclude-cli-$$OS-$$ARCH$$EXT ..."; \
		CGO_ENABLED=0 GOOS=$$OS GOARCH=$$ARCH go build -ldflags "$(LDFLAGS)" \
			-o $(DIST)/iclude-cli-$$OS-$$ARCH$$EXT ./cmd/cli/; \
	done
	@echo "Release build complete: $(DIST)/"

clean:
	rm -rf $(DIST)
```

- [ ] **Step 2: Test local build**

Run: `make build`
Expected: `dist/iclude-mcp` and `dist/iclude-cli` created

- [ ] **Step 3: Test cross-compile (one platform)**

Run: `CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -o /tmp/iclude-mcp-linux-arm64 ./cmd/mcp/`
Expected: BUILD SUCCESS (binary for linux/arm64)

Note: CGO_ENABLED=0 is critical — IClude uses modernc.org/sqlite (pure Go SQLite), not cgo.

- [ ] **Step 4: Commit**

```bash
git add Makefile
git commit -m "build: add Makefile with cross-compile release target"
```

---

## Task 4: install.sh（Linux/macOS）

**Files:**
- Create: `install.sh`

- [ ] **Step 1: Create install script**

```bash
#!/bin/bash
set -euo pipefail

# IClude 一键安装脚本 / IClude one-click installer for Linux/macOS
# Usage: curl -fsSL https://raw.githubusercontent.com/MemoryGet/LocalMem/main/install.sh | bash

REPO="MemoryGet/LocalMem"
INSTALL_DIR="$HOME/.iclude"
BIN_DIR="$INSTALL_DIR/bin"

# ── 颜色输出 / Color output ──
RED='\033[0;31m'; GREEN='\033[0;32m'; YELLOW='\033[1;33m'; NC='\033[0m'
info()  { echo -e "${GREEN}[INFO]${NC} $1"; }
warn()  { echo -e "${YELLOW}[WARN]${NC} $1"; }
error() { echo -e "${RED}[ERROR]${NC} $1" >&2; exit 1; }

# ── 检测平台 / Detect platform ──
detect_platform() {
    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    ARCH=$(uname -m)
    case "$ARCH" in
        x86_64)  ARCH="amd64" ;;
        aarch64) ARCH="arm64" ;;
        arm64)   ARCH="arm64" ;;
        *)       error "Unsupported architecture: $ARCH" ;;
    esac
    case "$OS" in
        linux|darwin) ;;
        *)  error "Unsupported OS: $OS. Use install.ps1 for Windows." ;;
    esac
    info "Platform: ${OS}/${ARCH}"
}

# ── 获取最新版本 / Get latest release version ──
get_latest_version() {
    VERSION=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null \
        | grep '"tag_name"' | head -1 | sed -E 's/.*"([^"]+)".*/\1/')
    if [ -z "$VERSION" ]; then
        VERSION="main"
        warn "Could not detect latest release, using main branch builds"
    fi
    info "Version: ${VERSION}"
}

# ── 下载二进制 / Download binaries ──
download_binaries() {
    mkdir -p "$BIN_DIR"
    local base_url="https://github.com/${REPO}/releases/download/${VERSION}"

    for bin in iclude-mcp iclude-cli; do
        local url="${base_url}/${bin}-${OS}-${ARCH}"
        local dest="${BIN_DIR}/${bin}"
        info "Downloading ${bin}..."
        if ! curl -fsSL -o "$dest" "$url"; then
            error "Failed to download ${bin} from ${url}"
        fi
        chmod +x "$dest"
    done
    info "Binaries installed to ${BIN_DIR}/"
}

# ── 加入 PATH / Add to PATH ──
add_to_path() {
    local path_line="export PATH=\"${BIN_DIR}:\$PATH\""
    local added=false

    for rc in "$HOME/.bashrc" "$HOME/.zshrc" "$HOME/.profile"; do
        if [ -f "$rc" ]; then
            if ! grep -q ".iclude/bin" "$rc" 2>/dev/null; then
                echo "" >> "$rc"
                echo "# IClude memory system" >> "$rc"
                echo "$path_line" >> "$rc"
                added=true
            fi
        fi
    done

    # 当前 shell 立即生效 / Apply to current shell
    export PATH="${BIN_DIR}:$PATH"

    if [ "$added" = true ]; then
        info "Added ${BIN_DIR} to PATH (restart shell or run: source ~/.bashrc)"
    else
        info "PATH already configured"
    fi
}

# ── 生成配置 / Generate config ──
generate_config() {
    local config_file="$INSTALL_DIR/config.yaml"

    if [ -f "$config_file" ]; then
        warn "Config already exists at ${config_file}, skipping"
        return
    fi

    # 交互式询问 API Key / Prompt for API Key
    local api_key=""
    if [ -t 0 ]; then
        echo ""
        echo -n "Enter your OpenAI API Key (or press Enter to skip): "
        read -r api_key
    fi

    if [ -z "$api_key" ]; then
        api_key='${OPENAI_API_KEY}'
        warn "No API key provided. Set OPENAI_API_KEY environment variable before use."
    fi

    cat > "$config_file" << YAML
# IClude config — auto-generated by install.sh
storage:
  sqlite:
    enabled: true
    path: "${INSTALL_DIR}/iclude.db"
  qdrant:
    enabled: false

llm:
  openai:
    api_key: "${api_key}"
    model: "gpt-4o-mini"

mcp:
  enabled: true
  default_team_id: "default"
  default_owner_id: "local-user"

hooks:
  enabled: true

scheduler:
  enabled: true
  cleanup_interval: 1h
  access_flush_interval: 30s

heartbeat:
  enabled: true
  interval: 6h
YAML

    info "Config written to ${config_file}"
}

# ── 配置 Claude Code / Configure Claude Code ──
configure_claude() {
    local claude_dir="$HOME/.claude"
    mkdir -p "$claude_dir"

    # 写入全局 MCP 配置（stdio 模式）/ Write global MCP config (stdio mode)
    local mcp_file="$claude_dir/.mcp.json"
    if [ -f "$mcp_file" ] && grep -q "iclude" "$mcp_file" 2>/dev/null; then
        info "MCP config already contains iclude, skipping"
    else
        # 如果文件存在则合并，否则创建 / Merge if exists, create if not
        if [ -f "$mcp_file" ]; then
            # 简单策略：备份旧文件，创建新文件 / Simple: backup and recreate
            cp "$mcp_file" "${mcp_file}.bak"
            warn "Backed up existing .mcp.json to .mcp.json.bak"
        fi
        cat > "$mcp_file" << JSON
{
  "mcpServers": {
    "iclude": {
      "type": "stdio",
      "command": "${BIN_DIR}/iclude-mcp",
      "args": ["--stdio", "--config", "${INSTALL_DIR}/config.yaml"]
    }
  }
}
JSON
        info "MCP stdio config written to ${mcp_file}"
    fi

    # 写入全局 hooks 配置 / Write global hooks config
    local settings_file="$claude_dir/settings.json"
    if [ -f "$settings_file" ] && grep -q "iclude-cli" "$settings_file" 2>/dev/null; then
        info "Hooks already configured, skipping"
    else
        info "Note: Add hooks to your Claude Code settings manually if needed."
        info "See: https://github.com/${REPO}#hooks-configuration"
    fi
}

# ── 主流程 / Main ──
main() {
    echo ""
    echo "  IClude Memory System Installer"
    echo "  ================================"
    echo ""

    detect_platform
    get_latest_version
    download_binaries
    add_to_path
    generate_config
    configure_claude

    echo ""
    info "Installation complete!"
    echo ""
    echo "  Restart Claude Code to activate IClude memory system."
    echo "  Config: ${INSTALL_DIR}/config.yaml"
    echo "  Data:   ${INSTALL_DIR}/iclude.db"
    echo ""
}

main "$@"
```

- [ ] **Step 2: Make executable and test syntax**

Run: `chmod +x install.sh && bash -n install.sh`
Expected: No syntax errors

- [ ] **Step 3: Commit**

```bash
git add install.sh
git commit -m "feat: add one-click install script for Linux/macOS"
```

---

## Task 5: install.ps1（Windows）

**Files:**
- Create: `install.ps1`

- [ ] **Step 1: Create PowerShell install script**

```powershell
# IClude 一键安装脚本 (Windows) / IClude one-click installer for Windows
# Usage: irm https://raw.githubusercontent.com/MemoryGet/LocalMem/main/install.ps1 | iex

$ErrorActionPreference = "Stop"
$REPO = "MemoryGet/LocalMem"
$INSTALL_DIR = "$env:USERPROFILE\.iclude"
$BIN_DIR = "$INSTALL_DIR\bin"

function Info($msg)  { Write-Host "[INFO] $msg" -ForegroundColor Green }
function Warn($msg)  { Write-Host "[WARN] $msg" -ForegroundColor Yellow }
function Error($msg) { Write-Host "[ERROR] $msg" -ForegroundColor Red; exit 1 }

# Detect architecture
$ARCH = if ([Environment]::Is64BitOperatingSystem) {
    if ($env:PROCESSOR_ARCHITECTURE -eq "ARM64") { "arm64" } else { "amd64" }
} else { Error "32-bit systems not supported" }

Info "Platform: windows/$ARCH"

# Get latest version
try {
    $release = Invoke-RestMethod "https://api.github.com/repos/$REPO/releases/latest"
    $VERSION = $release.tag_name
} catch {
    $VERSION = "main"
    Warn "Could not detect latest release"
}
Info "Version: $VERSION"

# Download binaries
New-Item -ItemType Directory -Path $BIN_DIR -Force | Out-Null
$baseUrl = "https://github.com/$REPO/releases/download/$VERSION"

foreach ($bin in @("iclude-mcp", "iclude-cli")) {
    $url = "$baseUrl/$bin-windows-$ARCH.exe"
    $dest = "$BIN_DIR\$bin.exe"
    Info "Downloading $bin..."
    try {
        Invoke-WebRequest -Uri $url -OutFile $dest -UseBasicParsing
    } catch {
        Error "Failed to download $bin from $url"
    }
}
Info "Binaries installed to $BIN_DIR\"

# Add to PATH
$currentPath = [Environment]::GetEnvironmentVariable("PATH", "User")
if ($currentPath -notlike "*$BIN_DIR*") {
    [Environment]::SetEnvironmentVariable("PATH", "$BIN_DIR;$currentPath", "User")
    $env:PATH = "$BIN_DIR;$env:PATH"
    Info "Added $BIN_DIR to user PATH"
} else {
    Info "PATH already configured"
}

# Generate config
$configFile = "$INSTALL_DIR\config.yaml"
if (!(Test-Path $configFile)) {
    $apiKey = Read-Host "Enter your OpenAI API Key (or press Enter to skip)"
    if ([string]::IsNullOrEmpty($apiKey)) {
        $apiKey = '${OPENAI_API_KEY}'
        Warn "No API key provided. Set OPENAI_API_KEY environment variable before use."
    }

    $dbPath = "$INSTALL_DIR\iclude.db" -replace '\\', '/'
    $configContent = @"
storage:
  sqlite:
    enabled: true
    path: "$dbPath"
  qdrant:
    enabled: false
llm:
  openai:
    api_key: "$apiKey"
    model: "gpt-4o-mini"
mcp:
  enabled: true
  default_team_id: "default"
  default_owner_id: "local-user"
hooks:
  enabled: true
scheduler:
  enabled: true
  cleanup_interval: 1h
  access_flush_interval: 30s
heartbeat:
  enabled: true
  interval: 6h
"@
    Set-Content -Path $configFile -Value $configContent -Encoding UTF8
    Info "Config written to $configFile"
} else {
    Warn "Config already exists, skipping"
}

# Configure Claude Code MCP
$claudeDir = "$env:USERPROFILE\.claude"
New-Item -ItemType Directory -Path $claudeDir -Force | Out-Null
$mcpFile = "$claudeDir\.mcp.json"

$mcpBin = "$BIN_DIR\iclude-mcp.exe" -replace '\\', '/'
$configPathForMcp = "$INSTALL_DIR\config.yaml" -replace '\\', '/'
$mcpContent = @"
{
  "mcpServers": {
    "iclude": {
      "type": "stdio",
      "command": "$mcpBin",
      "args": ["--stdio", "--config", "$configPathForMcp"]
    }
  }
}
"@

if ((Test-Path $mcpFile) -and (Get-Content $mcpFile -Raw) -like "*iclude*") {
    Info "MCP config already contains iclude, skipping"
} else {
    if (Test-Path $mcpFile) {
        Copy-Item $mcpFile "$mcpFile.bak"
        Warn "Backed up existing .mcp.json"
    }
    Set-Content -Path $mcpFile -Value $mcpContent -Encoding UTF8
    Info "MCP stdio config written to $mcpFile"
}

Write-Host ""
Info "Installation complete!"
Write-Host ""
Write-Host "  Restart Claude Code to activate IClude memory system."
Write-Host "  Config: $configFile"
Write-Host "  Data:   $INSTALL_DIR\iclude.db"
Write-Host ""
```

- [ ] **Step 2: Commit**

```bash
git add install.ps1
git commit -m "feat: add one-click install script for Windows"
```

---

## Task 6: GitHub Actions Release 工作流

**Files:**
- Create: `.github/workflows/release.yml`

- [ ] **Step 1: Create workflow file**

```yaml
name: Release

on:
  push:
    tags: ['v*']

permissions:
  contents: write

jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - uses: actions/setup-go@v5
        with:
          go-version: '1.25'

      - name: Run tests
        run: go test ./testing/... -count=1

      - name: Build release binaries
        run: make release

      - name: Create GitHub Release
        uses: softprops/action-gh-release@v2
        with:
          files: dist/*
          generate_release_notes: true
```

- [ ] **Step 2: Create .github directory and commit**

```bash
mkdir -p .github/workflows
git add .github/workflows/release.yml
git commit -m "ci: add GitHub Actions release workflow for cross-compiled binaries"
```

---

## Task 7: 端到端验证

- [ ] **Step 1: Build both binaries**

Run: `make build`
Expected: `dist/iclude-mcp` and `dist/iclude-cli` created

- [ ] **Step 2: Test stdio mode**

Run:
```bash
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}' | dist/iclude-mcp --stdio --config config.yaml 2>/dev/null
```
Expected: Two JSON-RPC response lines on stdout

- [ ] **Step 3: Test install script syntax**

Run: `bash -n install.sh`
Expected: No errors

- [ ] **Step 4: Run all tests**

Run: `go test ./testing/... -count=1`
Expected: ALL PASS

- [ ] **Step 5: Commit any fixes**

```bash
git add -A
git commit -m "test: end-to-end verification for one-click install"
```
