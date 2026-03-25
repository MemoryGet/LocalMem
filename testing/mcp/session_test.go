// Package mcp_test MCP 会话单元测试 / MCP session unit tests
package mcp_test

import (
	"context"
	"encoding/json"
	"testing"

	"iclude/internal/mcp"
	"iclude/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSession_Dispatch_initialize 验证 initialize 响应包含 protocolVersion / Verify protocolVersion in response
func TestSession_Dispatch_initialize(t *testing.T) {
	reg := mcp.NewRegistry()
	id := &model.Identity{TeamID: "team1", OwnerID: "user1"}
	sess := mcp.NewSession("s1", reg, id)
	defer sess.Close()

	raw := mustMarshal(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  mcp.MethodInitialize,
	})
	sess.Dispatch(context.Background(), raw)

	data := <-sess.Out()
	var resp mcp.JSONRPCResponse
	require.NoError(t, json.Unmarshal(data, &resp))
	assert.Nil(t, resp.Error)
	require.NotNil(t, resp.Result)

	// 验证 protocolVersion / Verify protocol version field
	resultMap, ok := resp.Result.(map[string]any)
	require.True(t, ok)
	assert.Equal(t, mcp.MCPProtocolVersion, resultMap["protocolVersion"])
}

// TestSession_Dispatch_ping 验证 ping 返回空结果 / Verify ping returns empty result
func TestSession_Dispatch_ping(t *testing.T) {
	reg := mcp.NewRegistry()
	sess := mcp.NewSession("s2", reg, &model.Identity{})
	defer sess.Close()

	raw := mustMarshal(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  mcp.MethodPing,
	})
	sess.Dispatch(context.Background(), raw)

	data := <-sess.Out()
	var resp mcp.JSONRPCResponse
	require.NoError(t, json.Unmarshal(data, &resp))
	assert.Nil(t, resp.Error)
	assert.NotNil(t, resp.Result)
}

// TestSession_Dispatch_toolsCall_success 注册 mock 工具后调用，验证结果 / Register mock tool, call it, verify result
func TestSession_Dispatch_toolsCall_success(t *testing.T) {
	reg := mcp.NewRegistry()
	reg.RegisterTool(&stubTool{name: "my_tool", desc: "test tool"})
	sess := mcp.NewSession("s3", reg, &model.Identity{TeamID: "t", OwnerID: "u"})
	defer sess.Close()

	raw := mustMarshal(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  mcp.MethodToolsCall,
		"params": map[string]any{
			"name":      "my_tool",
			"arguments": map[string]any{},
		},
	})
	sess.Dispatch(context.Background(), raw)

	data := <-sess.Out()
	var resp mcp.JSONRPCResponse
	require.NoError(t, json.Unmarshal(data, &resp))
	assert.Nil(t, resp.Error, "expected no error for known tool")
	assert.NotNil(t, resp.Result)
}

// TestSession_Dispatch_toolsCall_unknown 调用未知工具，验证错误码 -32601 / Call unknown tool, verify error code -32601
func TestSession_Dispatch_toolsCall_unknown(t *testing.T) {
	reg := mcp.NewRegistry()
	sess := mcp.NewSession("s4", reg, &model.Identity{})
	defer sess.Close()

	raw := mustMarshal(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      4,
		"method":  mcp.MethodToolsCall,
		"params": map[string]any{
			"name":      "nonexistent_tool",
			"arguments": map[string]any{},
		},
	})
	sess.Dispatch(context.Background(), raw)

	data := <-sess.Out()
	var resp mcp.JSONRPCResponse
	require.NoError(t, json.Unmarshal(data, &resp))
	require.NotNil(t, resp.Error)
	assert.Equal(t, -32601, resp.Error.Code)
}

// TestSession_Dispatch_methodNotFound 调用未知方法，验证错误码 -32601 / Call unknown method, verify error code -32601
func TestSession_Dispatch_methodNotFound(t *testing.T) {
	reg := mcp.NewRegistry()
	sess := mcp.NewSession("s5", reg, &model.Identity{})
	defer sess.Close()

	raw := mustMarshal(t, map[string]any{
		"jsonrpc": "2.0",
		"id":      5,
		"method":  "unknown/method",
	})
	sess.Dispatch(context.Background(), raw)

	data := <-sess.Out()
	var resp mcp.JSONRPCResponse
	require.NoError(t, json.Unmarshal(data, &resp))
	require.NotNil(t, resp.Error)
	assert.Equal(t, -32601, resp.Error.Code)
}

// TestWithIdentity_roundtrip WithIdentity + IdentityFromContext 往返验证 / WithIdentity + IdentityFromContext round-trip
func TestWithIdentity_roundtrip(t *testing.T) {
	id := &model.Identity{TeamID: "team-x", OwnerID: "owner-y"}
	ctx := mcp.WithIdentity(context.Background(), id)
	got := mcp.IdentityFromContext(ctx)
	require.NotNil(t, got)
	assert.Equal(t, "team-x", got.TeamID)
	assert.Equal(t, "owner-y", got.OwnerID)
}

// TestWithIdentity_nilOnMissingContext 空 context 返回 nil / Empty context returns nil
func TestWithIdentity_nilOnMissingContext(t *testing.T) {
	got := mcp.IdentityFromContext(context.Background())
	assert.Nil(t, got)
}

// TestSession_Close_idempotent 多次调用 Close 不 panic / Multiple Close calls must not panic
func TestSession_Close_idempotent(t *testing.T) {
	reg := mcp.NewRegistry()
	sess := mcp.NewSession("s6", reg, &model.Identity{})

	assert.NotPanics(t, func() {
		sess.Close()
		sess.Close() // second call must not panic
	})
}

// --- plan-compatible HandleRequest tests (kept for plan compliance) ---

// TestSession_HandleRequest_initialize plan 兼容 initialize 测试 / Plan-compatible initialize test
func TestSession_HandleRequest_initialize(t *testing.T) {
	reg := mcp.NewRegistry()
	id := &model.Identity{TeamID: "t", OwnerID: "u"}
	sess := mcp.NewSession("s7", reg, id)
	defer sess.Close()

	req := &mcp.JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  mcp.MethodInitialize,
	}
	resp := sess.HandleRequest(context.Background(), req)
	require.NotNil(t, resp)
	assert.Nil(t, resp.Error)
	assert.NotNil(t, resp.Result)
}

// TestSession_HandleRequest_toolsList plan 兼容 tools/list 测试 / Plan-compatible tools/list test
func TestSession_HandleRequest_toolsList(t *testing.T) {
	reg := mcp.NewRegistry()
	reg.RegisterTool(&stubTool{name: "test_tool"})
	sess := mcp.NewSession("s8", reg, &model.Identity{})
	defer sess.Close()

	req := &mcp.JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`2`),
		Method:  mcp.MethodToolsList,
	}
	resp := sess.HandleRequest(context.Background(), req)
	assert.Nil(t, resp.Error)
}

// TestSession_HandleRequest_unknownMethod plan 兼容未知方法测试 / Plan-compatible unknown method test
func TestSession_HandleRequest_unknownMethod(t *testing.T) {
	reg := mcp.NewRegistry()
	sess := mcp.NewSession("s9", reg, &model.Identity{})
	defer sess.Close()

	req := &mcp.JSONRPCRequest{JSONRPC: "2.0", ID: json.RawMessage(`3`), Method: "unknown/method"}
	resp := sess.HandleRequest(context.Background(), req)
	require.NotNil(t, resp.Error)
	assert.Equal(t, -32601, resp.Error.Code)
}

// mustMarshal JSON 序列化辅助 / JSON marshal helper
func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return data
}
