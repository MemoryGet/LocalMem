# One-Click Install Design

> IClude 一键安装脚本，让普通用户在任意机器上 `curl | bash` 即可开箱即用。

## 1. 目标

- Linux/macOS: `curl -fsSL .../install.sh | bash`
- Windows: `irm .../install.ps1 | iex`
- 自动下载预编译二进制、生成配置、配置 Claude Code hooks + MCP stdio
- 用户只需输入 API Key（可选），重启 Claude Code 即生效

## 2. 安装流程

```
用户执行安装脚本
    |
    +-- 1. 检测平台 (linux/darwin/windows) + 架构 (amd64/arm64)
    +-- 2. 从 GitHub Releases 下载预编译二进制 (iclude-mcp + iclude-cli)
    +-- 3. 安装到 ~/.iclude/bin/，加入 PATH
    +-- 4. 交互式生成 ~/.iclude/config.yaml（提示输入 API Key，可跳过）
    +-- 5. 写入 Claude Code 配置:
    |       - ~/.claude/settings.local.json (hooks)
    |       - 项目 .mcp.json 或全局 MCP 配置 (stdio 模式)
    +-- 6. 输出安装成功信息，提示重启 Claude Code
```

## 3. MCP Server stdio 模式

### 现状

MCP Server 仅支持 HTTP+SSE 传输（`GET /sse` + `POST /messages`）。

### 改动

给 `cmd/mcp/main.go` 加 `--stdio` flag：
- 无 flag: 启动 HTTP+SSE 服务器（现有行为）
- `--stdio`: 从 stdin 读 JSON-RPC 请求，stdout 写 JSON-RPC 响应，stderr 写日志
- stdio 模式复用现有的 Registry + ToolHandler，只替换传输层

### Claude Code 配置

```json
{
  "mcpServers": {
    "iclude": {
      "type": "stdio",
      "command": "iclude-mcp",
      "args": ["--stdio", "--config", "~/.iclude/config.yaml"]
    }
  }
}
```

Claude Code 打开时自动拉起 MCP 进程，关闭时自动停止。零运维。

## 4. 安装目录结构

```
~/.iclude/
  bin/
    iclude-mcp      # MCP Server 二进制
    iclude-cli      # CLI hooks 二进制
  config.yaml       # 用户配置
  iclude.db         # SQLite 数据库（运行时自动创建）
```

## 5. 默认 config.yaml 模板

```yaml
storage:
  sqlite:
    enabled: true
    path: "~/.iclude/iclude.db"
  qdrant:
    enabled: false

llm:
  openai:
    api_key: "${OPENAI_API_KEY}"
    model: "gpt-4o-mini"

mcp:
  enabled: true

hooks:
  enabled: true

scheduler:
  enabled: true

heartbeat:
  enabled: true
```

- `api_key`: 安装时交互输入写入明文值；用户跳过则保留 `${OPENAI_API_KEY}` 占位，运行时从环境变量读取
- SQLite 路径使用 `~/.iclude/iclude.db`，需确认 config 加载时展开 `~`

## 6. Claude Code Hooks 配置

安装脚本写入 `~/.claude/settings.local.json`（全局级别，非项目级别）：

```json
{
  "hooks": {
    "SessionStart": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "iclude-cli hook session-start",
            "timeout": 10
          }
        ]
      }
    ],
    "PostToolUse": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "iclude-cli hook capture",
            "timeout": 5
          }
        ]
      }
    ],
    "Stop": [
      {
        "hooks": [
          {
            "type": "command",
            "command": "iclude-cli hook session-stop",
            "timeout": 10
          }
        ]
      }
    ]
  }
}
```

注意：安装脚本需要合并现有 settings，不能覆盖用户已有配置。

## 7. install.sh（Linux/macOS）

### 逻辑

```bash
#!/bin/bash
set -euo pipefail

VERSION="latest"
INSTALL_DIR="$HOME/.iclude"
BIN_DIR="$INSTALL_DIR/bin"
REPO="MemoryGet/LocalMem"

# 1. 检测平台
OS=$(uname -s | tr '[:upper:]' '[:lower:]')   # linux / darwin
ARCH=$(uname -m)                                # x86_64 / arm64 / aarch64
case "$ARCH" in
  x86_64)  ARCH="amd64" ;;
  aarch64) ARCH="arm64" ;;
esac

# 2. 下载二进制
download "iclude-mcp-${OS}-${ARCH}" "$BIN_DIR/iclude-mcp"
download "iclude-cli-${OS}-${ARCH}" "$BIN_DIR/iclude-cli"
chmod +x "$BIN_DIR/iclude-mcp" "$BIN_DIR/iclude-cli"

# 3. 加入 PATH（写入 .bashrc / .zshrc）
add_to_path "$BIN_DIR"

# 4. 生成 config.yaml
prompt_api_key  # 交互输入或跳过
generate_config "$INSTALL_DIR/config.yaml"

# 5. 配置 Claude Code
configure_mcp_json
configure_hooks

# 6. 完成
echo "Installation complete! Restart Claude Code."
```

### Release 资产命名

```
iclude-mcp-linux-amd64
iclude-mcp-linux-arm64
iclude-mcp-darwin-amd64
iclude-mcp-darwin-arm64
iclude-mcp-windows-amd64.exe
iclude-mcp-windows-arm64.exe
iclude-cli-linux-amd64
iclude-cli-linux-arm64
iclude-cli-darwin-amd64
iclude-cli-darwin-arm64
iclude-cli-windows-amd64.exe
iclude-cli-windows-arm64.exe
```

共 12 个二进制。

## 8. install.ps1（Windows）

PowerShell 版本，逻辑等价：
- 下载 `.exe` 二进制到 `$HOME\.iclude\bin\`
- 加入用户 PATH 环境变量
- 生成 config.yaml
- 配置 Claude Code hooks + MCP

## 9. Makefile

```makefile
PLATFORMS := linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64 windows/arm64

release:
	@for platform in $(PLATFORMS); do \
		OS=$${platform%/*}; ARCH=$${platform#*/}; \
		EXT=""; [ "$$OS" = "windows" ] && EXT=".exe"; \
		CGO_ENABLED=0 GOOS=$$OS GOARCH=$$ARCH go build -o dist/iclude-mcp-$$OS-$$ARCH$$EXT ./cmd/mcp/; \
		CGO_ENABLED=0 GOOS=$$OS GOARCH=$$ARCH go build -o dist/iclude-cli-$$OS-$$ARCH$$EXT ./cmd/cli/; \
	done
```

## 10. GitHub Actions

```yaml
on:
  push:
    tags: ['v*']

jobs:
  release:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - uses: actions/setup-go@v5
        with: { go-version: '1.25' }
      - run: make release
      - uses: softprops/action-gh-release@v2
        with:
          files: dist/*
```

Tag 推送时自动交叉编译 + 发布 GitHub Release。

## 11. 卸载

```bash
# Linux/macOS
rm -rf ~/.iclude
# 手动移除 .bashrc/.zshrc 中的 PATH 行
# 手动移除 .claude/settings.local.json 中的 hooks
# 手动移除 .mcp.json 中的 iclude 条目
```

安装脚本可提供 `--uninstall` flag 自动清理。

## 12. 需要实现的文件

| 文件 | 类型 | 说明 |
|------|------|------|
| `cmd/mcp/stdio.go` | 新增 | stdio 传输层实现 |
| `cmd/mcp/main.go` | 修改 | 加 `--stdio` flag 分支 |
| `install.sh` | 新增 | Linux/macOS 安装脚本 |
| `install.ps1` | 新增 | Windows 安装脚本 |
| `Makefile` | 新增 | 交叉编译目标 |
| `.github/workflows/release.yml` | 新增 | CI 自动发布 |

## 13. 设计决策记录

| 决策 | 选择 | 理由 |
|------|------|------|
| 安装目录 | `~/.iclude/` | 不污染系统目录，用户空间自包含 |
| MCP 启动 | stdio 模式 | Claude Code 自管理生命周期，零运维 |
| API Key | 交互输入 + 环境变量 fallback | 兼顾易用和安全 |
| 二进制分发 | GitHub Releases | Go 交叉编译天然支持，零外部依赖 |
| 平台支持 | linux/darwin/windows × amd64/arm64 | 覆盖 99%+ 开发者 |
| 配置合并 | 读取现有 settings 后追加 | 不破坏用户已有配置 |
