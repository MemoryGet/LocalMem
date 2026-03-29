# Claude Code Integration Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Integrate IClude into Claude Code via hooks — auto-capture tool calls, auto-inject session context, generate session summaries.

**Architecture:** Go CLI binary (`cmd/cli/`) communicates with MCP Server over HTTP. Three hooks (SessionStart/PostToolUse/Stop) call CLI subcommands. MCP tools extended to support session creation and richer retain fields.

**Tech Stack:** Go 1.25+, stdlib net/http client, existing MCP Server (SSE transport), existing config.yaml/Viper stack.

**Design Spec:** `docs/superpowers/specs/2026-03-29-claude-code-integration-design.md`

---

## File Structure

### New files

| File | Responsibility |
|------|---------------|
| `internal/config/hooks.go` | HooksConfig struct definition |
| `internal/mcp/tools/create_session.go` | `iclude_create_session` MCP tool |
| `internal/mcp/client/client.go` | MCP HTTP client (CLI -> MCP Server) |
| `cmd/cli/main.go` | CLI entry point, subcommand routing |
| `cmd/cli/hook_session_start.go` | `hook session-start` subcommand |
| `cmd/cli/hook_capture.go` | `hook capture` subcommand |
| `cmd/cli/hook_session_stop.go` | `hook session-stop` subcommand |
| `testing/mcp/create_session_test.go` | create_session tool tests |
| `testing/cli/hook_capture_test.go` | capture filtering/formatting tests |

### Modified files

| File | Change |
|------|--------|
| `internal/mcp/tools/retain.go` | Add `context_id`, `source_type`, `message_role` to retainArgs |
| `internal/config/config.go` | Add `Hooks HooksConfig` field to Config struct |
| `cmd/mcp/main.go` | Register `iclude_create_session` tool |
| `config.yaml` | Add `hooks` config section |

---

## Task 1: HooksConfig 配置结构

**Files:**
- Create: `internal/config/hooks.go`
- Modify: `internal/config/config.go`
- Modify: `config.yaml`

- [ ] **Step 1: Create HooksConfig struct**

```go
// internal/config/hooks.go
package config

// HooksConfig Claude Code hooks 配置 / Claude Code hooks configuration
type HooksConfig struct {
	Enabled       bool     `mapstructure:"enabled"`
	MCPURL        string   `mapstructure:"mcp_url"`
	SkipTools     []string `mapstructure:"skip_tools"`
	MaxInputChars int      `mapstructure:"max_input_chars"`
	MaxOutputChars int     `mapstructure:"max_output_chars"`
	InjectLimit   int      `mapstructure:"inject_limit"`
	SummaryLimit  int      `mapstructure:"summary_limit"`
}
```

- [ ] **Step 2: Add to Config struct**

In `internal/config/config.go`, add field to Config:
```go
Hooks HooksConfig `mapstructure:"hooks"`
```

Add defaults in `setDefaults()`:
```go
viper.SetDefault("hooks.enabled", false)
viper.SetDefault("hooks.mcp_url", "http://localhost:8081")
viper.SetDefault("hooks.skip_tools", []string{"Glob", "Grep", "ToolSearch", "TaskCreate", "TaskUpdate", "TaskList", "TaskGet", "TodoWrite"})
viper.SetDefault("hooks.max_input_chars", 1000)
viper.SetDefault("hooks.max_output_chars", 500)
viper.SetDefault("hooks.inject_limit", 20)
viper.SetDefault("hooks.summary_limit", 50)
```

- [ ] **Step 3: Add hooks section to config.yaml**

```yaml
hooks:
  enabled: false
  mcp_url: "http://localhost:8081"
  skip_tools: [Glob, Grep, ToolSearch, TaskCreate, TaskUpdate, TaskList, TaskGet, TodoWrite]
  max_input_chars: 1000
  max_output_chars: 500
  inject_limit: 20
  summary_limit: 50
```

- [ ] **Step 4: Verify config loads**

Run: `go build ./cmd/server/`
Expected: BUILD SUCCESS

- [ ] **Step 5: Commit**

```bash
git add internal/config/hooks.go internal/config/config.go config.yaml
git commit -m "feat(config): add HooksConfig for Claude Code integration"
```

---

## Task 2: iclude_create_session MCP 工具

**Files:**
- Create: `internal/mcp/tools/create_session.go`
- Create: `testing/mcp/create_session_test.go`
- Modify: `cmd/mcp/main.go`

- [ ] **Step 1: Write failing test**

```go
// testing/mcp/create_session_test.go
package mcp_test

import (
	"context"
	"encoding/json"
	"testing"

	"iclude/internal/mcp/tools"
	"iclude/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockContextCreator struct {
	created *model.Context
}

func (m *mockContextCreator) Create(ctx context.Context, req *model.CreateContextRequest) (*model.Context, error) {
	m.created = &model.Context{
		ID:       "ctx-123",
		Name:     req.Name,
		Kind:     req.Kind,
		Scope:    req.Scope,
		Metadata: req.Metadata,
	}
	return m.created, nil
}

func TestCreateSessionTool_Execute(t *testing.T) {
	tests := []struct {
		name      string
		args      string
		wantErr   bool
		wantKind  string
	}{
		{
			name:     "basic session creation",
			args:     `{"session_id":"abc123","project_dir":"/root/LocalMem"}`,
			wantKind: "session",
		},
		{
			name:    "missing session_id",
			args:    `{}`,
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockContextCreator{}
			tool := tools.NewCreateSessionTool(mock)
			result, err := tool.Execute(context.Background(), json.RawMessage(tc.args))
			require.NoError(t, err)
			if tc.wantErr {
				assert.True(t, result.IsError)
				return
			}
			assert.False(t, result.IsError)
			assert.NotNil(t, mock.created)
			assert.Equal(t, tc.wantKind, mock.created.Kind)
			assert.Equal(t, "abc123", mock.created.Metadata["session_id"])
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./testing/mcp/ -run TestCreateSessionTool -v`
Expected: FAIL (type not defined)

- [ ] **Step 3: Implement create_session tool**

```go
// internal/mcp/tools/create_session.go
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"iclude/internal/mcp"
	"iclude/internal/model"
)

// ContextCreator 上下文创建接口 / Interface for creating contexts
type ContextCreator interface {
	Create(ctx context.Context, req *model.CreateContextRequest) (*model.Context, error)
}

// CreateSessionTool iclude_create_session 工具 / iclude_create_session tool handler
type CreateSessionTool struct{ ctxMgr ContextCreator }

// NewCreateSessionTool 创建会话工具 / Create session tool
func NewCreateSessionTool(ctxMgr ContextCreator) *CreateSessionTool {
	return &CreateSessionTool{ctxMgr: ctxMgr}
}

type createSessionArgs struct {
	SessionID  string `json:"session_id"`
	ProjectDir string `json:"project_dir,omitempty"`
	Scope      string `json:"scope,omitempty"`
}

// Definition 返回工具元数据定义 / Return tool metadata definition
func (t *CreateSessionTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name:        "iclude_create_session",
		Description: "Create a session context for Claude Code hook integration. Call at session start to get a context_id for subsequent retain calls.",
		InputSchema: json.RawMessage(`{
			"type":"object",
			"properties":{
				"session_id":{"type":"string","description":"Claude Code session ID"},
				"project_dir":{"type":"string","description":"Project working directory"},
				"scope":{"type":"string","description":"Namespace scope"}
			},
			"required":["session_id"]
		}`),
	}
}

// Execute 创建会话 Context 并返回 context_id / Create session Context and return context_id
func (t *CreateSessionTool) Execute(ctx context.Context, arguments json.RawMessage) (*mcp.ToolResult, error) {
	var args createSessionArgs
	if err := json.Unmarshal(arguments, &args); err != nil {
		return mcp.ErrorResult("invalid arguments: " + err.Error()), nil
	}
	if args.SessionID == "" {
		return mcp.ErrorResult("session_id is required"), nil
	}

	now := time.Now().UTC()
	req := &model.CreateContextRequest{
		Name:  fmt.Sprintf("claude-code-%s", args.SessionID[:min(len(args.SessionID), 8)]),
		Kind:  "session",
		Scope: args.Scope,
		Metadata: map[string]any{
			"session_id":  args.SessionID,
			"project_dir": args.ProjectDir,
			"started_at":  now.Format(time.RFC3339),
		},
	}
	created, err := t.ctxMgr.Create(ctx, req)
	if err != nil {
		return mcp.ErrorResult("failed to create session: " + err.Error()), nil
	}
	out, _ := json.Marshal(map[string]any{"context_id": created.ID, "session_id": args.SessionID})
	return mcp.TextResult(string(out)), nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./testing/mcp/ -run TestCreateSessionTool -v`
Expected: PASS

- [ ] **Step 5: Register tool in MCP server**

In `cmd/mcp/main.go`, add after existing tool registrations:

```go
if deps.ContextManager != nil {
	reg.RegisterTool(tools.NewCreateSessionTool(deps.ContextManager))
}
```

- [ ] **Step 6: Verify build**

Run: `go build ./cmd/mcp/`
Expected: BUILD SUCCESS

- [ ] **Step 7: Commit**

```bash
git add internal/mcp/tools/create_session.go testing/mcp/create_session_test.go cmd/mcp/main.go
git commit -m "feat(mcp): add iclude_create_session tool for hook integration"
```

---

## Task 3: 扩展 iclude_retain 工具参数

**Files:**
- Modify: `internal/mcp/tools/retain.go`

- [ ] **Step 1: Extend retainArgs struct**

In `internal/mcp/tools/retain.go`, add fields to `retainArgs`:

```go
type retainArgs struct {
	Content     string            `json:"content"`
	Scope       string            `json:"scope,omitempty"`
	Kind        string            `json:"kind,omitempty"`
	Tags        []string          `json:"tags,omitempty"`
	Metadata    map[string]string `json:"metadata,omitempty"`
	ContextID   string            `json:"context_id,omitempty"`
	SourceType  string            `json:"source_type,omitempty"`
	MessageRole string            `json:"message_role,omitempty"`
}
```

- [ ] **Step 2: Update Execute to pass new fields**

In Execute method, update the `mem` construction:

```go
mem := &model.Memory{
	Content:     args.Content,
	Scope:       args.Scope,
	Kind:        args.Kind,
	ContextID:   args.ContextID,
	SourceType:  args.SourceType,
	MessageRole: args.MessageRole,
}
```

- [ ] **Step 3: Update InputSchema**

Update the Definition() InputSchema to document new fields:

```go
InputSchema: json.RawMessage(`{
	"type":"object",
	"properties":{
		"content":{"type":"string","description":"The memory content to save"},
		"scope":{"type":"string","description":"Namespace scope for organization"},
		"kind":{"type":"string","description":"Memory kind (fact, decision, observation, session_summary, etc.)"},
		"tags":{"type":"array","items":{"type":"string"},"description":"Optional tags"},
		"metadata":{"type":"object","description":"Optional key-value metadata"},
		"context_id":{"type":"string","description":"Context ID to associate with (e.g. session context)"},
		"source_type":{"type":"string","description":"Source type (manual, hook, conversation, api)"},
		"message_role":{"type":"string","description":"Message role (user, assistant, tool, system)"}
	},
	"required":["content"]
}`),
```

- [ ] **Step 4: Verify build and existing tests pass**

Run: `go build ./cmd/mcp/ && go test ./testing/mcp/ -v`
Expected: BUILD SUCCESS, all existing tests PASS

- [ ] **Step 5: Commit**

```bash
git add internal/mcp/tools/retain.go
git commit -m "feat(mcp): extend iclude_retain with context_id, source_type, message_role"
```

---

## Task 4: MCP HTTP Client

**Files:**
- Create: `internal/mcp/client/client.go`

- [ ] **Step 1: Create MCP client**

```go
// Package client MCP HTTP 客户端（CLI 用）/ MCP HTTP client for CLI hooks
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"
)

// Client MCP JSON-RPC 客户端 / MCP JSON-RPC client
type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
	sessionID  string
	reqID      atomic.Int64
}

// New 创建 MCP 客户端 / Create MCP client
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Connect 建立 SSE 会话并获取 session ID / Establish SSE session
func (c *Client) Connect(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/sse", nil)
	if err != nil {
		return fmt.Errorf("create SSE request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("SSE connect: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("SSE connect: status %d", resp.StatusCode)
	}

	// 读取首条 SSE 事件获取 session query param / Read first SSE event for session param
	buf := make([]byte, 4096)
	n, err := resp.Body.Read(buf)
	if err != nil && err != io.EOF {
		return fmt.Errorf("SSE read: %w", err)
	}

	// 解析 "data: /messages?session=xxx\n\n" 格式 / Parse SSE data field
	data := string(buf[:n])
	var sessionParam string
	for _, line := range bytes.Split([]byte(data), []byte("\n")) {
		s := string(line)
		if len(s) > 6 && s[:6] == "data: " {
			sessionParam = s[6:]
			break
		}
	}
	if sessionParam == "" {
		return fmt.Errorf("SSE: no session endpoint in response")
	}
	c.sessionID = sessionParam
	return nil
}

// jsonRPCRequest JSON-RPC 2.0 请求 / JSON-RPC 2.0 request
type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int64  `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

// CallTool 调用 MCP 工具 / Call an MCP tool
func (c *Client) CallTool(ctx context.Context, toolName string, arguments any) (json.RawMessage, error) {
	id := c.reqID.Add(1)
	rpcReq := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  "tools/call",
		Params: map[string]any{
			"name":      toolName,
			"arguments": arguments,
		},
	}

	body, err := json.Marshal(rpcReq)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	url := c.baseURL + c.sessionID
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("call tool: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("call tool: status %d: %s", resp.StatusCode, string(respBody))
	}

	// MCP SSE 模型：POST 返回 202，响应通过 SSE 流异步返回
	// 对于 CLI hooks 场景，我们只需确认请求被接受
	return nil, nil
}

// CallToolSync 同步调用工具（fire-and-forget 模式）/ Synchronous tool call (fire-and-forget)
// 适用于 hooks 场景：不需要等待工具结果
func (c *Client) CallToolSync(ctx context.Context, toolName string, arguments any) error {
	_, err := c.CallTool(ctx, toolName, arguments)
	return err
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./internal/mcp/client/`
Expected: BUILD SUCCESS

- [ ] **Step 3: Commit**

```bash
git add internal/mcp/client/client.go
git commit -m "feat(mcp): add MCP HTTP client for CLI hooks"
```

---

## Task 5: CLI 入口 + session-start 子命令

**Files:**
- Create: `cmd/cli/main.go`
- Create: `cmd/cli/hook_session_start.go`

- [ ] **Step 1: Create CLI entry point**

```go
// Package main IClude CLI 入口 / IClude CLI entry point
package main

import (
	"fmt"
	"os"
)

func main() {
	if len(os.Args) < 3 || os.Args[1] != "hook" {
		fmt.Fprintln(os.Stderr, "usage: iclude-cli hook <session-start|capture|session-stop>")
		os.Exit(1)
	}

	var err error
	switch os.Args[2] {
	case "session-start":
		err = runSessionStart()
	case "capture":
		err = runCapture()
	case "session-stop":
		err = runSessionStop()
	default:
		fmt.Fprintf(os.Stderr, "unknown hook subcommand: %s\n", os.Args[2])
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "hook error: %v\n", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 2: Implement session-start subcommand**

```go
// cmd/cli/hook_session_start.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"iclude/internal/config"
	"iclude/internal/mcp/client"
)

// sessionStartInput Claude Code SessionStart hook stdin JSON
type sessionStartInput struct {
	SessionID      string `json:"session_id"`
	CWD            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	TranscriptPath string `json:"transcript_path"`
}

func runSessionStart() error {
	// 1. 读 stdin JSON
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	var hookInput sessionStartInput
	if err := json.Unmarshal(input, &hookInput); err != nil {
		return fmt.Errorf("parse stdin: %w", err)
	}
	if hookInput.SessionID == "" {
		return fmt.Errorf("session_id is empty")
	}

	// 2. 读配置
	if err := config.LoadConfig(); err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	cfg := config.GetConfig()

	// 3. 连接 MCP
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	mcpURL := cfg.Hooks.MCPURL
	if mcpURL == "" {
		mcpURL = fmt.Sprintf("http://localhost:%d", cfg.MCP.Port)
	}
	c := client.New(mcpURL, cfg.MCP.APIToken)
	if err := c.Connect(ctx); err != nil {
		return fmt.Errorf("mcp connect: %w", err)
	}

	// 4. 创建会话 Context
	if err := c.CallToolSync(ctx, "iclude_create_session", map[string]any{
		"session_id":  hookInput.SessionID,
		"project_dir": hookInput.CWD,
	}); err != nil {
		return fmt.Errorf("create session: %w", err)
	}

	// 5. 获取最近记忆 abstract
	injectLimit := cfg.Hooks.InjectLimit
	if injectLimit <= 0 {
		injectLimit = 20
	}
	if err := c.CallToolSync(ctx, "iclude_scan", map[string]any{
		"query": "*",
		"limit": injectLimit,
	}); err != nil {
		// scan 失败不阻断，输出空上下文
		fmt.Fprintf(os.Stderr, "scan failed: %v\n", err)
	}

	// 6. stdout 输出上下文注入文本
	// 注意：由于 MCP SSE 异步模型，CLI 可能无法同步拿到 scan 结果
	// 降级策略：输出会话创建确认 + 使用提示
	fmt.Printf("# IClude Session Context (session: %s)\n", hookInput.SessionID[:min(len(hookInput.SessionID), 8)])
	fmt.Printf("Session started at %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Printf("Project: %s\n", hookInput.CWD)
	fmt.Println("---")
	fmt.Println("IClude memory system active. Use iclude_scan to search memories, iclude_fetch for full content.")

	return nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
```

- [ ] **Step 3: Add placeholder stubs for other subcommands**

```go
// cmd/cli/hook_capture.go
package main

func runCapture() error {
	// Task 6 实现
	return nil
}
```

```go
// cmd/cli/hook_session_stop.go
package main

func runSessionStop() error {
	// Task 7 实现
	return nil
}
```

- [ ] **Step 4: Verify build**

Run: `go build -o iclude-cli ./cmd/cli/`
Expected: BUILD SUCCESS, binary `iclude-cli` created

- [ ] **Step 5: Commit**

```bash
git add cmd/cli/main.go cmd/cli/hook_session_start.go cmd/cli/hook_capture.go cmd/cli/hook_session_stop.go
git commit -m "feat(cli): add iclude-cli with session-start hook subcommand"
```

---

## Task 6: capture 子命令

**Files:**
- Modify: `cmd/cli/hook_capture.go`
- Create: `testing/cli/hook_capture_test.go`

- [ ] **Step 1: Write failing test for capture formatting and filtering**

```go
// testing/cli/hook_capture_test.go
package cli_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// formatObservation 格式化工具调用为可读文本
// 直接测试格式化逻辑（从 hook_capture.go 导出）
func TestFormatObservation(t *testing.T) {
	tests := []struct {
		name       string
		toolName   string
		toolInput  string
		toolOutput string
		wantPrefix string
	}{
		{
			name:       "Write tool",
			toolName:   "Write",
			toolInput:  `{"file_path":"/root/LocalMem/cmd/cli/main.go","content":"package main..."}`,
			toolOutput: `{"success":true}`,
			wantPrefix: "[Write]",
		},
		{
			name:       "Bash tool",
			toolName:   "Bash",
			toolInput:  `{"command":"go test ./..."}`,
			toolOutput: `ok iclude/testing/store 0.5s`,
			wantPrefix: "[Bash]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := FormatObservation(tc.toolName, tc.toolInput, tc.toolOutput, 1000, 500)
			assert.Contains(t, result, tc.wantPrefix)
			assert.NotEmpty(t, result)
		})
	}
}

func TestShouldSkipTool(t *testing.T) {
	skipList := []string{"Glob", "Grep", "ToolSearch"}

	assert.True(t, ShouldSkipTool("Glob", skipList))
	assert.True(t, ShouldSkipTool("Grep", skipList))
	assert.False(t, ShouldSkipTool("Write", skipList))
	assert.False(t, ShouldSkipTool("Edit", skipList))
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./testing/cli/ -v`
Expected: FAIL (package/functions not defined)

- [ ] **Step 3: Implement capture logic**

```go
// cmd/cli/hook_capture.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"iclude/internal/config"
	"iclude/internal/mcp/client"
)

// captureInput Claude Code PostToolUse hook stdin JSON
type captureInput struct {
	SessionID    string          `json:"session_id"`
	CWD          string          `json:"cwd"`
	ToolName     string          `json:"tool_name"`
	ToolInput    json.RawMessage `json:"tool_input"`
	ToolResponse json.RawMessage `json:"tool_response"`
	ToolUseID    string          `json:"tool_use_id"`
}

func runCapture() error {
	// 1. 读 stdin JSON
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	var hookInput captureInput
	if err := json.Unmarshal(input, &hookInput); err != nil {
		return fmt.Errorf("parse stdin: %w", err)
	}

	// 2. 读配置
	if err := config.LoadConfig(); err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	cfg := config.GetConfig()

	// 3. 黑名单过滤
	if ShouldSkipTool(hookInput.ToolName, cfg.Hooks.SkipTools) {
		return nil
	}

	// 4. 格式化 content
	inputStr := truncate(string(hookInput.ToolInput), cfg.Hooks.MaxInputChars)
	outputStr := truncate(string(hookInput.ToolResponse), cfg.Hooks.MaxOutputChars)
	content := FormatObservation(hookInput.ToolName, inputStr, outputStr, cfg.Hooks.MaxInputChars, cfg.Hooks.MaxOutputChars)

	// 5. 构造 metadata
	metadata := map[string]string{
		"tool_name":   hookInput.ToolName,
		"tool_use_id": hookInput.ToolUseID,
		"session_id":  hookInput.SessionID,
	}
	if len(inputStr) > 0 {
		metadata["tool_input"] = inputStr
	}
	if len(outputStr) > 0 {
		metadata["tool_output"] = outputStr
	}

	// 6. 连接 MCP 并 retain
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	mcpURL := cfg.Hooks.MCPURL
	if mcpURL == "" {
		mcpURL = fmt.Sprintf("http://localhost:%d", cfg.MCP.Port)
	}
	c := client.New(mcpURL, cfg.MCP.APIToken)
	if err := c.Connect(ctx); err != nil {
		return fmt.Errorf("mcp connect: %w", err)
	}

	return c.CallToolSync(ctx, "iclude_retain", map[string]any{
		"content":      content,
		"kind":         "observation",
		"source_type":  "hook",
		"message_role": "tool",
		"metadata":     metadata,
	})
}

// ShouldSkipTool 检查工具是否在黑名单中 / Check if tool is in skip list
func ShouldSkipTool(toolName string, skipTools []string) bool {
	for _, skip := range skipTools {
		if strings.EqualFold(toolName, skip) {
			return true
		}
	}
	return false
}

// FormatObservation 格式化工具调用为可读文本 / Format tool call as readable text
func FormatObservation(toolName, toolInput, toolOutput string, maxInput, maxOutput int) string {
	input := truncate(toolInput, maxInput)
	output := truncate(toolOutput, maxOutput)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[%s] ", toolName))

	// 针对常见工具提取关键信息 / Extract key info for common tools
	switch toolName {
	case "Write", "Edit", "Read":
		var parsed struct {
			FilePath string `json:"file_path"`
		}
		if json.Unmarshal([]byte(toolInput), &parsed) == nil && parsed.FilePath != "" {
			sb.WriteString(parsed.FilePath)
		} else {
			sb.WriteString(input)
		}
	case "Bash":
		var parsed struct {
			Command string `json:"command"`
		}
		if json.Unmarshal([]byte(toolInput), &parsed) == nil && parsed.Command != "" {
			sb.WriteString(fmt.Sprintf("$ %s", truncate(parsed.Command, 200)))
			if output != "" {
				sb.WriteString(fmt.Sprintf(" -> %s", truncate(output, 200)))
			}
		} else {
			sb.WriteString(input)
		}
	default:
		sb.WriteString(input)
	}

	return sb.String()
}

func truncate(s string, maxChars int) string {
	if maxChars <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxChars {
		return s
	}
	return string(runes[:maxChars]) + "..."
}
```

- [ ] **Step 4: Update test to import from correct package**

Since `FormatObservation` and `ShouldSkipTool` are in `package main` (cmd/cli), tests cannot import them directly. Move the pure functions to an internal package:

Create `internal/hooks/format.go`:
```go
// Package hooks Claude Code hook 工具函数 / Claude Code hook utilities
package hooks

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ShouldSkipTool 检查工具是否在黑名单中 / Check if tool is in skip list
func ShouldSkipTool(toolName string, skipTools []string) bool {
	for _, skip := range skipTools {
		if strings.EqualFold(toolName, skip) {
			return true
		}
	}
	return false
}

// FormatObservation 格式化工具调用为可读文本 / Format tool call as readable text
func FormatObservation(toolName, toolInput, toolOutput string, maxInput, maxOutput int) string {
	input := Truncate(toolInput, maxInput)
	output := Truncate(toolOutput, maxOutput)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[%s] ", toolName))

	switch toolName {
	case "Write", "Edit", "Read":
		var parsed struct {
			FilePath string `json:"file_path"`
		}
		if json.Unmarshal([]byte(toolInput), &parsed) == nil && parsed.FilePath != "" {
			sb.WriteString(parsed.FilePath)
		} else {
			sb.WriteString(input)
		}
	case "Bash":
		var parsed struct {
			Command string `json:"command"`
		}
		if json.Unmarshal([]byte(toolInput), &parsed) == nil && parsed.Command != "" {
			sb.WriteString(fmt.Sprintf("$ %s", Truncate(parsed.Command, 200)))
			if output != "" {
				sb.WriteString(fmt.Sprintf(" -> %s", Truncate(output, 200)))
			}
		} else {
			sb.WriteString(input)
		}
	default:
		sb.WriteString(input)
	}

	return sb.String()
}

// Truncate 截断字符串到指定 rune 数 / Truncate string to max rune count
func Truncate(s string, maxChars int) string {
	if maxChars <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxChars {
		return s
	}
	return string(runes[:maxChars]) + "..."
}
```

Update test to import `iclude/internal/hooks`:
```go
// testing/cli/hook_capture_test.go
package cli_test

import (
	"testing"

	"iclude/internal/hooks"

	"github.com/stretchr/testify/assert"
)

func TestFormatObservation(t *testing.T) {
	tests := []struct {
		name       string
		toolName   string
		toolInput  string
		toolOutput string
		wantPrefix string
	}{
		{
			name:       "Write tool",
			toolName:   "Write",
			toolInput:  `{"file_path":"/root/LocalMem/cmd/cli/main.go","content":"package main..."}`,
			toolOutput: `{"success":true}`,
			wantPrefix: "[Write]",
		},
		{
			name:       "Bash tool",
			toolName:   "Bash",
			toolInput:  `{"command":"go test ./..."}`,
			toolOutput: `ok iclude/testing/store 0.5s`,
			wantPrefix: "[Bash]",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := hooks.FormatObservation(tc.toolName, tc.toolInput, tc.toolOutput, 1000, 500)
			assert.Contains(t, result, tc.wantPrefix)
			assert.NotEmpty(t, result)
		})
	}
}

func TestShouldSkipTool(t *testing.T) {
	skipList := []string{"Glob", "Grep", "ToolSearch"}

	assert.True(t, hooks.ShouldSkipTool("Glob", skipList))
	assert.True(t, hooks.ShouldSkipTool("Grep", skipList))
	assert.False(t, hooks.ShouldSkipTool("Write", skipList))
	assert.False(t, hooks.ShouldSkipTool("Edit", skipList))
}
```

Update `cmd/cli/hook_capture.go` to use `hooks.FormatObservation`, `hooks.ShouldSkipTool`, `hooks.Truncate` instead of local functions.

- [ ] **Step 5: Run tests**

Run: `go test ./testing/cli/ -v`
Expected: PASS

- [ ] **Step 6: Verify build**

Run: `go build -o iclude-cli ./cmd/cli/`
Expected: BUILD SUCCESS

- [ ] **Step 7: Commit**

```bash
git add internal/hooks/format.go cmd/cli/hook_capture.go testing/cli/hook_capture_test.go
git commit -m "feat(cli): add capture subcommand with filtering and formatting"
```

---

## Task 7: session-stop 子命令

**Files:**
- Modify: `cmd/cli/hook_session_stop.go`

- [ ] **Step 1: Implement session-stop**

```go
// cmd/cli/hook_session_stop.go
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"iclude/internal/config"
	"iclude/internal/hooks"
	"iclude/internal/mcp/client"
)

// sessionStopInput Claude Code Stop hook stdin JSON
type sessionStopInput struct {
	SessionID           string `json:"session_id"`
	CWD                 string `json:"cwd"`
	StopHookActive      bool   `json:"stop_hook_active"`
	LastAssistantMessage string `json:"last_assistant_message"`
}

func runSessionStop() error {
	// 1. 读 stdin JSON
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	var hookInput sessionStopInput
	if err := json.Unmarshal(input, &hookInput); err != nil {
		return fmt.Errorf("parse stdin: %w", err)
	}

	// 2. 防死循环
	if hookInput.StopHookActive {
		return nil
	}

	// 3. 读配置
	if err := config.LoadConfig(); err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	cfg := config.GetConfig()

	// 4. 连接 MCP
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	mcpURL := cfg.Hooks.MCPURL
	if mcpURL == "" {
		mcpURL = fmt.Sprintf("http://localhost:%d", cfg.MCP.Port)
	}
	c := client.New(mcpURL, cfg.MCP.APIToken)
	if err := c.Connect(ctx); err != nil {
		return fmt.Errorf("mcp connect: %w", err)
	}

	// 5. 生成会话摘要
	summary := fmt.Sprintf("Session %s ended at %s. Project: %s",
		hookInput.SessionID[:min(len(hookInput.SessionID), 8)],
		time.Now().UTC().Format(time.RFC3339),
		hookInput.CWD,
	)
	if hookInput.LastAssistantMessage != "" {
		summary += "\nLast action: " + hooks.Truncate(hookInput.LastAssistantMessage, 300)
	}

	// 6. 存储会话摘要
	return c.CallToolSync(ctx, "iclude_retain", map[string]any{
		"content":      summary,
		"kind":         "session_summary",
		"source_type":  "hook",
		"message_role": "system",
		"metadata": map[string]string{
			"session_id": hookInput.SessionID,
		},
	})
}
```

- [ ] **Step 2: Verify build**

Run: `go build -o iclude-cli ./cmd/cli/`
Expected: BUILD SUCCESS

- [ ] **Step 3: Commit**

```bash
git add cmd/cli/hook_session_stop.go
git commit -m "feat(cli): add session-stop subcommand with session summary"
```

---

## Task 8: Hooks 配置写入 settings.local.json

**Files:**
- Modify: `.claude/settings.local.json`

- [ ] **Step 1: Add hooks configuration**

In `.claude/settings.local.json`, add `hooks` section (merge with existing content):

```json
{
  "hooks": {
    "SessionStart": [{
      "command": "iclude-cli hook session-start",
      "timeout": 10000
    }],
    "PostToolUse": [{
      "command": "iclude-cli hook capture",
      "timeout": 5000
    }],
    "Stop": [{
      "command": "iclude-cli hook session-stop",
      "timeout": 10000
    }]
  }
}
```

Note: `iclude-cli` must be in PATH or use absolute path.

- [ ] **Step 2: Build and install CLI binary**

Run: `go build -o /usr/local/bin/iclude-cli ./cmd/cli/`
Expected: BUILD SUCCESS, binary installed

- [ ] **Step 3: Commit**

```bash
git add .claude/settings.local.json
git commit -m "feat(hooks): configure Claude Code hooks for IClude integration"
```

---

## Task 9: 端到端验证

- [ ] **Step 1: Start MCP server**

Run: `go run ./cmd/mcp/ &`
Expected: "mcp server starting" log

- [ ] **Step 2: Test session-start hook**

Run: `echo '{"session_id":"test-123","cwd":"/root/LocalMem","hook_event_name":"SessionStart"}' | iclude-cli hook session-start`
Expected: stdout 输出 IClude Session Context 文本

- [ ] **Step 3: Test capture hook**

Run: `echo '{"session_id":"test-123","tool_name":"Write","tool_input":{"file_path":"/tmp/test.go"},"tool_response":{"success":true},"tool_use_id":"toolu_01"}' | iclude-cli hook capture`
Expected: 静默退出，exit code 0

- [ ] **Step 4: Test capture skip**

Run: `echo '{"session_id":"test-123","tool_name":"Glob","tool_input":{},"tool_response":{},"tool_use_id":"toolu_02"}' | iclude-cli hook capture`
Expected: 静默退出（Glob 在黑名单中）

- [ ] **Step 5: Test session-stop hook**

Run: `echo '{"session_id":"test-123","stop_hook_active":false,"last_assistant_message":"Done.","cwd":"/root/LocalMem"}' | iclude-cli hook session-stop`
Expected: 静默退出，exit code 0

- [ ] **Step 6: Test stop_hook_active guard**

Run: `echo '{"session_id":"test-123","stop_hook_active":true,"cwd":"/root/LocalMem"}' | iclude-cli hook session-stop`
Expected: 立即退出，不调 MCP

- [ ] **Step 7: Run all tests**

Run: `go test ./testing/... -v -count=1`
Expected: ALL PASS

- [ ] **Step 8: Final commit**

```bash
git add -A
git commit -m "test: end-to-end verification for Claude Code hook integration"
```
