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

type mockMemCreatorEnrich struct{ created *model.Memory }

func (m *mockMemCreatorEnrich) Create(_ context.Context, mem *model.Memory) (*model.Memory, error) {
	m.created = mem
	mem.ID = "mem_new"
	return mem, nil
}

type mockDerivStore struct {
	calledSourceIDs []string
	calledTargetID  string
}

func (m *mockDerivStore) AddDerivations(_ context.Context, sourceIDs []string, targetID string) error {
	m.calledSourceIDs = sourceIDs
	m.calledTargetID = targetID
	return nil
}

func TestRetainTool_SummaryPassedToMemory(t *testing.T) {
	creator := &mockMemCreatorEnrich{}
	tool := tools.NewRetainTool(creator, nil, nil)
	args, _ := json.Marshal(map[string]any{
		"content": "Caroline got promoted.",
		"summary": "Caroline was promoted to senior engineer at Google.",
	})
	_, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.Equal(t, "Caroline was promoted to senior engineer at Google.", creator.created.Summary)
}

func TestRetainTool_DerivedFromCallsStore(t *testing.T) {
	creator := &mockMemCreatorEnrich{}
	derivStore := &mockDerivStore{}
	tool := tools.NewRetainTool(creator, nil, derivStore)
	args, _ := json.Marshal(map[string]any{
		"content":      "Caroline celebrated.",
		"derived_from": []string{"mem_abc", "mem_def"},
	})
	_, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
	assert.Equal(t, []string{"mem_abc", "mem_def"}, derivStore.calledSourceIDs)
	assert.Equal(t, "mem_new", derivStore.calledTargetID)
}

func TestRetainTool_NilDerivStoreSkipsDerivation(t *testing.T) {
	creator := &mockMemCreatorEnrich{}
	tool := tools.NewRetainTool(creator, nil, nil)
	args, _ := json.Marshal(map[string]any{
		"content":      "Some fact.",
		"derived_from": []string{"mem_abc"},
	})
	_, err := tool.Execute(context.Background(), args)
	require.NoError(t, err)
}
