// Package mcp_test MCP create_session 工具单元测试 / MCP create_session tool unit tests
package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"iclude/internal/mcp/tools"
	"iclude/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockContextCreator 测试用上下文创建存根 / Context creator stub for testing
type mockContextCreator struct {
	created *model.Context
	err     error
	// 记录最后一次调用的请求 / Records the last received request
	lastReq *model.CreateContextRequest
}

func (m *mockContextCreator) Create(_ context.Context, req *model.CreateContextRequest) (*model.Context, error) {
	m.lastReq = req
	if m.err != nil {
		return nil, m.err
	}
	if m.created != nil {
		return m.created, nil
	}
	// 默认返回：回显请求字段 / Default: echo request fields
	return &model.Context{
		ID:    "ctx-test-001",
		Name:  req.Name,
		ContextType: req.ContextType,
		Scope: req.Scope,
	}, nil
}

// TestCreateSessionTool_Definition 验证工具名称和 schema / Verify tool name and schema
func TestCreateSessionTool_Definition(t *testing.T) {
	tool := tools.NewCreateSessionTool(&mockContextCreator{})
	def := tool.Definition()
	assert.Equal(t, "iclude_create_session", def.Name)
	assert.NotEmpty(t, def.Description)

	var schema map[string]any
	require.NoError(t, json.Unmarshal(def.InputSchema, &schema))
	required, ok := schema["required"].([]any)
	require.True(t, ok)
	assert.Contains(t, required, "session_id")
}

// TestCreateSessionTool_Execute 表驱动测试 / Table-driven tests for Execute
func TestCreateSessionTool_Execute(t *testing.T) {
	tests := []struct {
		name          string
		args          map[string]any
		mockCreated   *model.Context
		mockErr       error
		wantIsError   bool
		wantErrSubstr string
		checkResult   func(t *testing.T, mock *mockContextCreator, result map[string]any)
	}{
		{
			name: "basic session creation",
			args: map[string]any{
				"session_id":  "session-abc-123",
				"project_dir": "/home/user/myproject",
			},
			checkResult: func(t *testing.T, mock *mockContextCreator, result map[string]any) {
				// 验证返回的 context_id 存在 / Verify context_id is present
				assert.Equal(t, "ctx-test-001", result["context_id"])
				assert.Equal(t, "session-abc-123", result["session_id"])
				// 验证 context_type=session / Verify context_type=session
				assert.Equal(t, "session", result["context_type"])
				// 验证请求中 metadata 包含 session_id / Verify metadata contains session_id
				require.NotNil(t, mock.lastReq)
				assert.Equal(t, "session", mock.lastReq.ContextType)
				assert.Equal(t, "session-abc-123", mock.lastReq.Metadata["session_id"])
				assert.Equal(t, "/home/user/myproject", mock.lastReq.Metadata["project_dir"])
				assert.NotEmpty(t, mock.lastReq.Metadata["started_at"])
			},
		},
		{
			name:          "missing session_id returns error",
			args:          map[string]any{},
			wantIsError:   true,
			wantErrSubstr: "session_id is required",
		},
		{
			name: "with scope passed through",
			args: map[string]any{
				"session_id": "session-scoped-456",
				"scope":      "work/project-x",
			},
			checkResult: func(t *testing.T, mock *mockContextCreator, result map[string]any) {
				// 验证 scope 传递 / Verify scope is passed through
				require.NotNil(t, mock.lastReq)
				assert.Equal(t, "work/project-x", mock.lastReq.Scope)
				assert.Equal(t, "work/project-x", result["scope"])
			},
		},
		{
			name: "creator error propagates",
			args: map[string]any{
				"session_id": "session-err-789",
			},
			mockErr:       errors.New("database unavailable"),
			wantIsError:   true,
			wantErrSubstr: "failed to create session context",
		},
		{
			name: "no project_dir omits it from metadata",
			args: map[string]any{
				"session_id": "session-no-dir",
			},
			checkResult: func(t *testing.T, mock *mockContextCreator, _ map[string]any) {
				require.NotNil(t, mock.lastReq)
				_, hasDir := mock.lastReq.Metadata["project_dir"]
				assert.False(t, hasDir, "project_dir should not be in metadata when not provided")
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock := &mockContextCreator{
				created: tc.mockCreated,
				err:     tc.mockErr,
			}
			tool := tools.NewCreateSessionTool(mock)

			rawArgs, err := json.Marshal(tc.args)
			require.NoError(t, err)

			result, err := tool.Execute(context.Background(), rawArgs)
			require.NoError(t, err)
			require.NotNil(t, result)

			if tc.wantIsError {
				assert.True(t, result.IsError)
				require.NotEmpty(t, result.Content)
				assert.Contains(t, result.Content[0].Text, tc.wantErrSubstr)
				return
			}

			assert.False(t, result.IsError)
			require.NotEmpty(t, result.Content)

			if tc.checkResult != nil {
				var parsed map[string]any
				require.NoError(t, json.Unmarshal([]byte(result.Content[0].Text), &parsed))
				tc.checkResult(t, mock, parsed)
			}
		})
	}
}
