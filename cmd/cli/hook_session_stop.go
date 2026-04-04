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

// sessionStopInput Claude Code Stop hook stdin JSON
type sessionStopInput struct {
	SessionID            string `json:"session_id"`
	CWD                  string `json:"cwd"`
	StopHookActive       bool   `json:"stop_hook_active"`
	LastAssistantMessage string `json:"last_assistant_message"`
}

// runSessionStop Stop hook 调用 finalize_session 完成会话终结 / Call finalize_session to finalize the session
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
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
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
	defer c.Close()

	// 5. 构建幂等键 / Build idempotency key
	idemKey := fmt.Sprintf("finalize:claude-code:%s:v1", hookInput.SessionID)

	// 6. 调用 finalize_session / Call finalize_session
	err = c.CallTool(ctx, "iclude_finalize_session", map[string]any{
		"session_id":      hookInput.SessionID,
		"tool_name":       "claude-code",
		"idempotency_key": idemKey,
	})
	if err != nil {
		// finalize 失败时降级为 retain summary / Fallback to retain summary on finalize failure
		fmt.Fprintf(os.Stderr, "iclude: finalize_session failed, falling back to retain: %v\n", err)
		return fallbackRetainSummary(ctx, c, hookInput)
	}

	return nil
}

// fallbackRetainSummary finalize 失败时降级为旧的 retain 行为 / Fallback to legacy retain behavior when finalize fails
func fallbackRetainSummary(ctx context.Context, c *client.Client, hookInput sessionStopInput) error {
	sessionShort := hookInput.SessionID
	if len(sessionShort) > 8 {
		sessionShort = sessionShort[:8]
	}
	summary := fmt.Sprintf("Session %s ended at %s. Project: %s",
		sessionShort,
		time.Now().UTC().Format(time.RFC3339),
		hookInput.CWD,
	)

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
