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
	"iclude/pkg/identity"
)

// sessionStartInput Claude Code SessionStart hook stdin JSON
type sessionStartInput struct {
	SessionID      string `json:"session_id"`
	CWD            string `json:"cwd"`
	HookEventName  string `json:"hook_event_name"`
	TranscriptPath string `json:"transcript_path"`
}

// scanResult iclude_scan 工具返回结果结构 / Result structure returned by iclude_scan tool
type scanResult struct {
	Memories []struct {
		ID       string `json:"id"`
		Excerpt string `json:"excerpt"`
		Tags     []struct {
			Name string `json:"name"`
		} `json:"tags"`
		Scope string `json:"scope"`
	} `json:"memories"`
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
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
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
	defer c.Close()

	// 4. 同步创建会话 Context，获取 context_id / Create session Context synchronously to get context_id
	sessionShort := hookInput.SessionID
	if len(sessionShort) > 8 {
		sessionShort = sessionShort[:8]
	}

	var contextID string
	createResult, err := c.CallToolSync(ctx, "iclude_create_session", map[string]any{
		"session_id":  hookInput.SessionID,
		"project_dir": hookInput.CWD,
		"project_id":  identity.ResolveProjectID(hookInput.CWD),
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "iclude: create session failed: %v\n", err)
	} else {
		// 尝试从响应中提取 context_id / Try to extract context_id from response
		var sessionResp struct {
			ContextID string `json:"context_id"`
		}
		if json.Unmarshal(createResult, &sessionResp) == nil && sessionResp.ContextID != "" {
			contextID = sessionResp.ContextID
		}
	}

	// 5. 同步调用 iclude_scan 获取相关记忆摘要 / Call iclude_scan synchronously to get relevant memory abstracts
	scanArgs := map[string]any{
		"project_dir": hookInput.CWD,
		"limit":       10,
	}
	if contextID != "" {
		scanArgs["context_id"] = contextID
	}

	scanRaw, err := c.CallToolSync(ctx, "iclude_scan", scanArgs)
	if err != nil {
		fmt.Fprintf(os.Stderr, "iclude: scan failed: %v\n", err)
	}

	// 6. 输出会话头和记忆摘要 / Output session header and memory abstracts
	fmt.Printf("# IClude Session Context (session: %s)\n", sessionShort)
	fmt.Printf("Session started at %s\n", time.Now().UTC().Format(time.RFC3339))
	fmt.Printf("Project: %s\n", hookInput.CWD)
	if contextID != "" {
		fmt.Printf("Context ID: %s\n", contextID)
	}
	fmt.Println("---")

	if scanRaw != nil {
		var result scanResult
		if err := json.Unmarshal(scanRaw, &result); err == nil && len(result.Memories) > 0 {
			fmt.Printf("## Recent memories (%d)\n", len(result.Memories))
			for i, m := range result.Memories {
				excerpt := m.Excerpt
				if excerpt == "" {
					excerpt = "(no excerpt)"
				}
				fmt.Printf("%d. %s", i+1, excerpt)
				if m.Scope != "" {
					fmt.Printf(" [scope: %s]", m.Scope)
				}
				if len(m.Tags) > 0 {
					tags := make([]string, 0, len(m.Tags))
					for _, t := range m.Tags {
						tags = append(tags, t.Name)
					}
					fmt.Printf(" [tags: %s]", joinTags(tags))
				}
				fmt.Println()
			}
			fmt.Println("---")
		}
	}

	fmt.Println("IClude memory system active. Use iclude_scan to search memories, iclude_fetch for full content.")
	return nil
}

// joinTags 连接标签名称 / Join tag names with comma separator
func joinTags(tags []string) string {
	result := ""
	for i, t := range tags {
		if i > 0 {
			result += ", "
		}
		result += t
	}
	return result
}
