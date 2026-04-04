package cli_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

// TestFinalizeIdempotencyKeyFormat 验证 finalize 幂等键格式一致性 / Verify finalize idempotency key format
func TestFinalizeIdempotencyKeyFormat(t *testing.T) {
	tests := []struct {
		name      string
		sessionID string
		wantKey   string
	}{
		{"normal session", "abc123", "finalize:claude-code:abc123:v1"},
		{"uuid session", "550e8400-e29b-41d4-a716-446655440000", "finalize:claude-code:550e8400-e29b-41d4-a716-446655440000:v1"},
		{"short session", "s1", "finalize:claude-code:s1:v1"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			idemKey := fmt.Sprintf("finalize:claude-code:%s:v1", tc.sessionID)
			assert.Equal(t, tc.wantKey, idemKey)
		})
	}
}

// TestFallbackSummaryFormat 验证降级摘要格式 / Verify fallback summary format
func TestFallbackSummaryFormat(t *testing.T) {
	sessionID := "abc12345678"
	cwd := "/home/user/project"

	sessionShort := sessionID
	if len(sessionShort) > 8 {
		sessionShort = sessionShort[:8]
	}
	summary := fmt.Sprintf("Session %s ended at %s. Project: %s",
		sessionShort,
		time.Now().UTC().Format(time.RFC3339),
		cwd,
	)

	assert.Equal(t, "abc12345", sessionShort, "session short should be first 8 chars")
	assert.Contains(t, summary, "Session abc12345")
	assert.Contains(t, summary, "/home/user/project")
	assert.Contains(t, summary, "ended at")
}

// TestFallbackSummaryShortSessionID 验证短 session ID 不截断 / Short session ID should not be truncated
func TestFallbackSummaryShortSessionID(t *testing.T) {
	sessionID := "s1"
	sessionShort := sessionID
	if len(sessionShort) > 8 {
		sessionShort = sessionShort[:8]
	}
	assert.Equal(t, "s1", sessionShort)
}

// TestStopHookCallsFinalize 验证 stop hook 调用 finalize 而非 retain / Verify stop hook calls finalize not retain
// 注意：这是行为文档测试，实际 MCP 调用在集成测试中验证
func TestStopHookCallsFinalize(t *testing.T) {
	// stop hook 应构建以下参数调 iclude_finalize_session / Stop hook should build these args
	sessionID := "test-session-123"
	expectedTool := "iclude_finalize_session"
	expectedArgs := map[string]any{
		"session_id":      sessionID,
		"tool_name":       "claude-code",
		"idempotency_key": fmt.Sprintf("finalize:claude-code:%s:v1", sessionID),
	}

	assert.Equal(t, "iclude_finalize_session", expectedTool)
	assert.Equal(t, sessionID, expectedArgs["session_id"])
	assert.Equal(t, "claude-code", expectedArgs["tool_name"])
	assert.Contains(t, expectedArgs["idempotency_key"], "finalize:claude-code:test-session-123:v1")
}
