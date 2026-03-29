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
	SessionID            string `json:"session_id"`
	CWD                  string `json:"cwd"`
	StopHookActive       bool   `json:"stop_hook_active"`
	LastAssistantMessage string `json:"last_assistant_message"`
}

// runSessionStop Stop hook 生成会话摘要 / Generate session summary via Stop hook
func runSessionStop() error {
	// 1. 读 stdin JSON / Read stdin JSON
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	var hookInput sessionStopInput
	if err := json.Unmarshal(input, &hookInput); err != nil {
		return fmt.Errorf("parse stdin: %w", err)
	}

	// 2. 防死循环 / Prevent infinite loop
	if hookInput.StopHookActive {
		return nil
	}

	// 3. 读配置 / Load config
	if err := config.LoadConfig(); err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	cfg := config.GetConfig()

	// 4. 连接 MCP / Connect to MCP
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	mcpURL := cfg.Hooks.MCPURL
	if mcpURL == "" {
		mcpURL = fmt.Sprintf("http://localhost:%d", cfg.MCP.Port)
	}
	c := client.New(mcpURL, cfg.MCP.APIToken)
	if err := c.Connect(ctx); err != nil {
		// MCP 不可达时静默退出 / Silent exit when MCP unreachable
		return nil
	}

	// 5. 生成会话摘要 / Generate session summary
	sessionShort := hookInput.SessionID
	if len(sessionShort) > 8 {
		sessionShort = sessionShort[:8]
	}
	summary := fmt.Sprintf("Session %s ended at %s. Project: %s",
		sessionShort,
		time.Now().UTC().Format(time.RFC3339),
		hookInput.CWD,
	)
	if hookInput.LastAssistantMessage != "" {
		summary += "\nLast action: " + hooks.Truncate(hookInput.LastAssistantMessage, 300)
	}

	// 6. 存储会话摘要（Stop hook 必须静默失败）/ Store session summary (Stop hook must be silent)
	defer c.Close()
	if err := c.CallTool(ctx, "iclude_retain", map[string]any{
		"content":      summary,
		"kind":         "session_summary",
		"source_type":  "hook",
		"message_role": "system",
		"metadata": map[string]string{
			"session_id": hookInput.SessionID,
		},
	}); err != nil {
		fmt.Fprintf(os.Stderr, "iclude: session stop retain failed: %v\n", err)
	}
	return nil
}
