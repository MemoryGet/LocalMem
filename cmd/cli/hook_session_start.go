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

// runSessionStart 处理 session-start 钩子 / Handle session-start hook
func runSessionStart() error {
	// 1. 读 stdin JSON / Read stdin JSON
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

	// 2. 读配置 / Load config
	if err := config.LoadConfig(); err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	cfg := config.GetConfig()

	// 3. 连接 MCP / Connect to MCP
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	mcpURL := cfg.Hooks.MCPURL
	if mcpURL == "" {
		mcpURL = fmt.Sprintf("http://localhost:%d", cfg.MCP.Port)
	}
	c := client.New(mcpURL, cfg.MCP.APIToken)
	if err := c.Connect(ctx); err != nil {
		// MCP 不可达时降级输出 / Graceful degradation when MCP unreachable
		fmt.Fprintf(os.Stderr, "iclude: mcp connect failed: %v\n", err)
		fmt.Printf("# IClude Session (offline mode)\nMCP server unreachable. Memory features disabled.\n")
		return nil
	}

	// 4. 创建会话 Context / Create session Context
	if err := c.CallTool(ctx, "iclude_create_session", map[string]any{
		"session_id":  hookInput.SessionID,
		"project_dir": hookInput.CWD,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "iclude: create session failed: %v\n", err)
	}

	// 5. 获取最近记忆（scan 是 fire-and-forget，无法同步获取结果）
	// 降级策略：输出会话确认 + 使用提示 / Degraded: output session confirmation + usage hints
	sessionShort := hookInput.SessionID
	if len(sessionShort) > 8 {
		sessionShort = sessionShort[:8]
	}
	fmt.Printf("# IClude Session Context (session: %s)\n", sessionShort)
	fmt.Printf("Session started at %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Printf("Project: %s\n", hookInput.CWD)
	fmt.Println("---")
	fmt.Println("IClude memory system active. Use iclude_scan to search memories, iclude_fetch for full content.")

	return nil
}
