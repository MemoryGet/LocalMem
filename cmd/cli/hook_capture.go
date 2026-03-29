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

// captureInput Claude Code PostToolUse hook stdin JSON
type captureInput struct {
	SessionID    string          `json:"session_id"`
	CWD          string          `json:"cwd"`
	ToolName     string          `json:"tool_name"`
	ToolInput    json.RawMessage `json:"tool_input"`
	ToolResponse json.RawMessage `json:"tool_response"`
	ToolUseID    string          `json:"tool_use_id"`
}

// runCapture PostToolUse hook 捕获工具调用 / Capture tool calls via PostToolUse hook
func runCapture() error {
	// 1. 读 stdin JSON / Read stdin JSON
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read stdin: %w", err)
	}
	var hookInput captureInput
	if err := json.Unmarshal(input, &hookInput); err != nil {
		return fmt.Errorf("parse stdin: %w", err)
	}

	// 2. 读配置 / Load config
	if err := config.LoadConfig(); err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	cfg := config.GetConfig()

	// 3. 黑名单过滤 / Skip list filtering
	if hooks.ShouldSkipTool(hookInput.ToolName, cfg.Hooks.SkipTools) {
		return nil
	}

	// 4. 格式化 content（FormatObservation 内部截断）/ Format content (FormatObservation truncates internally)
	content := hooks.FormatObservation(hookInput.ToolName, string(hookInput.ToolInput), string(hookInput.ToolResponse), cfg.Hooks.MaxInputChars, cfg.Hooks.MaxOutputChars)

	// 5. 构造 metadata / Build metadata
	metadata := map[string]string{
		"tool_name":   hookInput.ToolName,
		"tool_use_id": hookInput.ToolUseID,
		"session_id":  hookInput.SessionID,
	}
	if inputTrunc := hooks.Truncate(string(hookInput.ToolInput), cfg.Hooks.MaxInputChars); len(inputTrunc) > 0 {
		metadata["tool_input"] = inputTrunc
	}
	if outputTrunc := hooks.Truncate(string(hookInput.ToolResponse), cfg.Hooks.MaxOutputChars); len(outputTrunc) > 0 {
		metadata["tool_output"] = outputTrunc
	}

	// 6. 连接 MCP 并 retain / Connect MCP and retain
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	mcpURL := cfg.Hooks.MCPURL
	if mcpURL == "" {
		mcpURL = fmt.Sprintf("http://localhost:%d", cfg.MCP.Port)
	}
	c := client.New(mcpURL, cfg.MCP.APIToken)
	if err := c.Connect(ctx); err != nil {
		// MCP 不可达时静默失败 / Silent failure when MCP unreachable
		return nil
	}

	// PostToolUse 钩子必须静默失败，非零退出码会影响 Claude Code
	// PostToolUse hooks must be silent — non-zero exit disrupts Claude Code.
	if err := c.CallTool(ctx, "iclude_retain", map[string]any{
		"content":      content,
		"kind":         "observation",
		"source_type":  "hook",
		"message_role": "tool",
		"metadata":     metadata,
	}); err != nil {
		fmt.Fprintf(os.Stderr, "iclude: capture retain failed: %v\n", err)
	}
	return nil
}
