// Package memory_test 实体/关系类型配置化测试 / Entity/relation type configuration tests
package memory_test

import (
	"strings"
	"testing"
	"time"

	"iclude/internal/config"
	"iclude/internal/llm"
	"iclude/internal/memory"
	"iclude/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractor_CustomEntityTypes_Accepted(t *testing.T) {
	// 自定义实体类型应被接受 / Custom entity types should be accepted
	tests := []struct {
		name        string
		entityTypes []string
		entityType  string
		wantAccept  bool
	}{
		{
			name:        "custom type event accepted",
			entityTypes: []string{"person", "event", "product"},
			entityType:  "event",
			wantAccept:  true,
		},
		{
			name:        "custom type product accepted",
			entityTypes: []string{"person", "event", "product"},
			entityType:  "product",
			wantAccept:  true,
		},
		{
			name:        "default type person still works",
			entityTypes: []string{"person", "event", "product"},
			entityType:  "person",
			wantAccept:  true,
		},
		{
			name:        "unconfigured type org rejected",
			entityTypes: []string{"person", "event", "product"},
			entityType:  "org",
			wantAccept:  false,
		},
		{
			name:        "case insensitive matching",
			entityTypes: []string{"Person", "EVENT"},
			entityType:  "person",
			wantAccept:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			llmResp := entitiesJSON(
				[]map[string]string{
					{"name": "TestEntity", "entity_type": tt.entityType, "description": "Test"},
				},
				nil,
			)

			mock := &mockLLMProvider{
				responses: mockResponses(llmResp),
			}

			cfg := &config.ExtractConfig{
				MaxEntities:         20,
				MaxRelations:        30,
				NormalizeEnabled:    false,
				NormalizeCandidates: 0,
				Timeout:             30 * time.Second,
				EntityTypes:         tt.entityTypes,
				RelationTypes:       []string{"uses", "knows"},
			}

			ext, _, _, _ := setupExtractor(t, mock, cfg)
			resp, err := ext.Extract(t.Context(), &model.ExtractRequest{
				Content: "test content",
				Scope:   "test-scope",
			})
			require.NoError(t, err)

			if tt.wantAccept {
				assert.Len(t, resp.Entities, 1, "entity should be accepted")
			} else {
				assert.Empty(t, resp.Entities, "entity should be rejected")
			}
		})
	}
}

func TestExtractor_CustomRelationTypes_Accepted(t *testing.T) {
	// 自定义关系类型应被接受 / Custom relation types should be accepted
	tests := []struct {
		name          string
		relationTypes []string
		relationType  string
		wantAccept    bool
	}{
		{
			name:          "custom type employs accepted",
			relationTypes: []string{"employs", "manages"},
			relationType:  "employs",
			wantAccept:    true,
		},
		{
			name:          "unconfigured type uses rejected",
			relationTypes: []string{"employs", "manages"},
			relationType:  "uses",
			wantAccept:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			llmResp := entitiesJSON(
				[]map[string]string{
					{"name": "Alice", "entity_type": "person", "description": "Engineer"},
					{"name": "Bob", "entity_type": "person", "description": "Manager"},
				},
				[]map[string]string{
					{"source": "Alice", "target": "Bob", "relation_type": tt.relationType},
				},
			)

			mock := &mockLLMProvider{
				responses: mockResponses(llmResp),
			}

			cfg := &config.ExtractConfig{
				MaxEntities:         20,
				MaxRelations:        30,
				NormalizeEnabled:    false,
				NormalizeCandidates: 0,
				Timeout:             30 * time.Second,
				EntityTypes:         []string{"person"},
				RelationTypes:       tt.relationTypes,
			}

			ext, _, _, _ := setupExtractor(t, mock, cfg)
			resp, err := ext.Extract(t.Context(), &model.ExtractRequest{
				Content: "test content",
				Scope:   "test-scope",
			})
			require.NoError(t, err)

			if tt.wantAccept {
				assert.Len(t, resp.Relations, 1, "relation should be accepted")
			} else {
				assert.Empty(t, resp.Relations, "relation should be rejected")
			}
		})
	}
}

func TestExtractor_FallbackDefaults_WhenConfigEmpty(t *testing.T) {
	// 配置为空时应使用默认类型 / Should use default types when config is empty
	llmResp := entitiesJSON(
		[]map[string]string{
			{"name": "Alice", "entity_type": "person", "description": "Engineer"},
			{"name": "Go", "entity_type": "tool", "description": "Language"},
		},
		[]map[string]string{
			{"source": "Alice", "target": "Go", "relation_type": "uses"},
		},
	)

	mock := &mockLLMProvider{
		responses: mockResponses(llmResp),
	}

	cfg := &config.ExtractConfig{
		MaxEntities:         20,
		MaxRelations:        30,
		NormalizeEnabled:    false,
		NormalizeCandidates: 0,
		Timeout:             30 * time.Second,
		EntityTypes:         nil, // 空列表触发默认值 / Empty triggers defaults
		RelationTypes:       nil,
	}

	ext, _, _, _ := setupExtractor(t, mock, cfg)
	resp, err := ext.Extract(t.Context(), &model.ExtractRequest{
		Content: "test content",
		Scope:   "test-scope",
	})
	require.NoError(t, err)
	assert.Len(t, resp.Entities, 2, "default entity types should work")
	assert.Len(t, resp.Relations, 1, "default relation types should work")
}

func TestExtractor_PromptContainsConfiguredTypes(t *testing.T) {
	// 提示词应包含配置的类型 / Prompt should contain configured types
	mock := &mockLLMProvider{
		responses: mockResponses(entitiesJSON(nil, nil)),
	}

	cfg := &config.ExtractConfig{
		MaxEntities:  20,
		MaxRelations: 30,
		Timeout:      30 * time.Second,
		EntityTypes:  []string{"animal", "plant", "mineral"},
		RelationTypes: []string{"eats", "grows_on"},
	}

	ext, _, _, _ := setupExtractor(t, mock, cfg)

	// 触发一次抽取来捕获发送给 LLM 的 prompt / Trigger extraction to capture prompt sent to LLM
	_, _ = ext.Extract(t.Context(), &model.ExtractRequest{
		Content: "test",
		Scope:   "test-scope",
	})

	// 检查 mock 收到的系统提示 / Check system prompt received by mock
	require.Equal(t, 1, mock.callIndex, "should have called LLM once")

	// 使用 BuildExtractPrompt 的公开等价来验证 / Verify via exported helper
	prompt := memory.BuildExtractPromptForTest(ext)
	assert.Contains(t, prompt, "animal")
	assert.Contains(t, prompt, "plant")
	assert.Contains(t, prompt, "mineral")
	assert.Contains(t, prompt, "eats")
	assert.Contains(t, prompt, "grows_on")
	// 不应包含默认类型 / Should not contain default types not in config
	assert.NotContains(t, prompt, "person")
	assert.NotContains(t, prompt, "uses")
}

// mockResponses 构建 mock LLM 响应列表 / Build mock LLM response list
func mockResponses(contents ...string) []*llm.ChatResponse {
	resp := make([]*llm.ChatResponse, len(contents))
	for i, c := range contents {
		resp[i] = &llm.ChatResponse{Content: c}
	}
	return resp
}

func TestExtractor_TypesNotInConfig_Rejected(t *testing.T) {
	// 不在配置中的类型应被过滤 / Types not in config should be filtered out
	llmResp := entitiesJSON(
		[]map[string]string{
			{"name": "Alice", "entity_type": "person", "description": "Engineer"},
			{"name": "NYC", "entity_type": "city", "description": "A city"},     // city 不在白名单 / not in allow-list
			{"name": "Go", "entity_type": "tool", "description": "Language"},     // tool 不在白名单 / not in allow-list
		},
		[]map[string]string{
			{"source": "Alice", "target": "Go", "relation_type": "uses"},        // uses 不在白名单 / not in allow-list
			{"source": "Alice", "target": "NYC", "relation_type": "lives_in"},   // lives_in 在白名单 / in allow-list
		},
	)

	mock := &mockLLMProvider{
		responses: mockResponses(llmResp),
	}

	cfg := &config.ExtractConfig{
		MaxEntities:         20,
		MaxRelations:        30,
		NormalizeEnabled:    false,
		NormalizeCandidates: 0,
		Timeout:             30 * time.Second,
		EntityTypes:         []string{"person"},       // 只允许 person / Only person allowed
		RelationTypes:       []string{"lives_in"},     // 只允许 lives_in / Only lives_in allowed
	}

	ext, _, _, _ := setupExtractor(t, mock, cfg)
	resp, err := ext.Extract(t.Context(), &model.ExtractRequest{
		Content: "test content",
		Scope:   "test-scope",
	})
	require.NoError(t, err)

	// 只有 person 类型的 Alice 应该被保留 / Only Alice (person) should remain
	assert.Len(t, resp.Entities, 1)
	assert.Equal(t, "Alice", resp.Entities[0].Name)

	// lives_in 关系的 target (NYC) 被过滤，所以关系也不会创建 / Relation target filtered, so no relations
	// uses 关系类型不在白名单 / uses not in allow-list
	assert.Empty(t, resp.Relations)
}

func TestMapKeys_Sorted(t *testing.T) {
	// mapKeys 应返回排序后的键列表 / mapKeys should return sorted keys
	m := map[string]bool{"zebra": true, "apple": true, "mango": true}
	keys := memory.MapKeysForTest(m)
	assert.Equal(t, []string{"apple", "mango", "zebra"}, keys)
}

func TestMapKeys_Empty(t *testing.T) {
	keys := memory.MapKeysForTest(map[string]bool{})
	assert.Empty(t, keys)
}

func TestExtractor_PromptFormat(t *testing.T) {
	// 验证提示词格式正确 / Verify prompt format is correct
	mock := &mockLLMProvider{
		responses: mockResponses(entitiesJSON(nil, nil)),
	}

	cfg := &config.ExtractConfig{
		MaxEntities:   20,
		MaxRelations:  30,
		Timeout:       30 * time.Second,
		EntityTypes:   []string{"person", "org"},
		RelationTypes: []string{"knows", "uses"},
	}

	ext, _, _, _ := setupExtractor(t, mock, cfg)
	prompt := memory.BuildExtractPromptForTest(ext)

	// 应包含 "Entity types:" 行 / Should contain Entity types line
	assert.True(t, strings.Contains(prompt, "Entity types: org, person"), "entity types should be sorted alphabetically")
	assert.True(t, strings.Contains(prompt, "Relation types: knows, uses"), "relation types should be sorted alphabetically")
}
