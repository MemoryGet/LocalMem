// Package mcp_test MCP 资源处理器测试 / MCP resource handler tests
package mcp_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"iclude/internal/mcp"
	"iclude/internal/mcp/resources"
	"iclude/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockTimelineQuerier 时间线查询存根 / Timeline querier stub for resource tests
type mockTimelineQuerier struct {
	memories []*model.Memory
	err      error
	lastReq  *model.TimelineRequest
}

func (m *mockTimelineQuerier) Timeline(_ context.Context, req *model.TimelineRequest) ([]*model.Memory, error) {
	m.lastReq = req
	if m.err != nil {
		return nil, m.err
	}
	return m.memories, nil
}

// --- RecentResource tests ---

func TestRecentResource_Definition(t *testing.T) {
	r := resources.NewRecentResource(&mockTimelineQuerier{})
	def := r.Definition()
	assert.Equal(t, "iclude://context/recent", def.URI)
	assert.Equal(t, "application/json", def.MimeType)
	assert.NotEmpty(t, def.Name)
}

func TestRecentResource_Match(t *testing.T) {
	r := resources.NewRecentResource(&mockTimelineQuerier{})
	assert.True(t, r.Match("iclude://context/recent"))
	assert.False(t, r.Match("iclude://context/session/abc"))
	assert.False(t, r.Match("iclude://other"))
}

func TestRecentResource_Read_success(t *testing.T) {
	mems := []*model.Memory{
		{ID: "m1", Content: "fact one"},
		{ID: "m2", Content: "fact two"},
	}
	querier := &mockTimelineQuerier{memories: mems}
	r := resources.NewRecentResource(querier)

	out, err := r.Read(context.Background(), "iclude://context/recent")
	require.NoError(t, err)

	var got []*model.Memory
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 2)
	assert.Equal(t, "m1", got[0].ID)
	assert.Equal(t, "m2", got[1].ID)
}

func TestRecentResource_Read_withIdentity(t *testing.T) {
	querier := &mockTimelineQuerier{memories: []*model.Memory{}}
	r := resources.NewRecentResource(querier)

	id := &model.Identity{TeamID: "team-42", OwnerID: "user-7"}
	ctx := mcp.WithIdentity(context.Background(), id)

	_, err := r.Read(ctx, "iclude://context/recent")
	require.NoError(t, err)

	require.NotNil(t, querier.lastReq)
	assert.Equal(t, "team-42", querier.lastReq.TeamID)
	assert.Equal(t, "user-7", querier.lastReq.OwnerID)
}

func TestRecentResource_Read_propagatesError(t *testing.T) {
	querier := &mockTimelineQuerier{err: errors.New("db error")}
	r := resources.NewRecentResource(querier)

	_, err := r.Read(context.Background(), "iclude://context/recent")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "db error")
}

func TestRecentResource_Read_customLimit(t *testing.T) {
	querier := &mockTimelineQuerier{memories: []*model.Memory{}}
	r := resources.NewRecentResource(querier, 5)

	_, err := r.Read(context.Background(), "iclude://context/recent")
	require.NoError(t, err)

	require.NotNil(t, querier.lastReq)
	assert.Equal(t, 5, querier.lastReq.Limit)
}

// --- SessionContextResource tests ---

func TestSessionContextResource_Definition(t *testing.T) {
	r := resources.NewSessionContextResource(&mockTimelineQuerier{})
	def := r.Definition()
	assert.Contains(t, def.URI, "iclude://context/session/")
	assert.Equal(t, "application/json", def.MimeType)
	assert.NotEmpty(t, def.Name)
}

func TestSessionContextResource_Match(t *testing.T) {
	r := resources.NewSessionContextResource(&mockTimelineQuerier{})
	assert.True(t, r.Match("iclude://context/session/abc123"))
	assert.True(t, r.Match("iclude://context/session/some-session-id"))
	assert.False(t, r.Match("iclude://context/recent"))
	assert.False(t, r.Match("iclude://context/session/"))
	assert.False(t, r.Match("iclude://other"))
}

func TestSessionContextResource_Read_success(t *testing.T) {
	mems := []*model.Memory{
		{ID: "m10", Content: "session memory"},
	}
	querier := &mockTimelineQuerier{memories: mems}
	r := resources.NewSessionContextResource(querier)

	out, err := r.Read(context.Background(), "iclude://context/session/sess-abc")
	require.NoError(t, err)

	var got []*model.Memory
	require.NoError(t, json.Unmarshal([]byte(out), &got))
	require.Len(t, got, 1)
	assert.Equal(t, "m10", got[0].ID)
}

func TestSessionContextResource_Read_setsScope(t *testing.T) {
	querier := &mockTimelineQuerier{memories: []*model.Memory{}}
	r := resources.NewSessionContextResource(querier)

	_, err := r.Read(context.Background(), "iclude://context/session/my-scope")
	require.NoError(t, err)

	require.NotNil(t, querier.lastReq)
	assert.Equal(t, "my-scope", querier.lastReq.Scope)
}

func TestSessionContextResource_Read_invalidURI(t *testing.T) {
	r := resources.NewSessionContextResource(&mockTimelineQuerier{})

	_, err := r.Read(context.Background(), "iclude://context/session/")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid session context URI")
}

func TestSessionContextResource_Read_withIdentity(t *testing.T) {
	querier := &mockTimelineQuerier{memories: []*model.Memory{}}
	r := resources.NewSessionContextResource(querier)

	id := &model.Identity{TeamID: "team-99", OwnerID: "user-1"}
	ctx := mcp.WithIdentity(context.Background(), id)

	_, err := r.Read(ctx, "iclude://context/session/sess-xyz")
	require.NoError(t, err)

	require.NotNil(t, querier.lastReq)
	assert.Equal(t, "team-99", querier.lastReq.TeamID)
	assert.Equal(t, "user-1", querier.lastReq.OwnerID)
}

func TestSessionContextResource_Read_propagatesError(t *testing.T) {
	querier := &mockTimelineQuerier{err: errors.New("timeline error")}
	r := resources.NewSessionContextResource(querier)

	_, err := r.Read(context.Background(), "iclude://context/session/sess-abc")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "timeline error")
}
