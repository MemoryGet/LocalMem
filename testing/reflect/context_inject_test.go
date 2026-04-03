// Package reflect_test Context 行为约束注入测试 / Tests for context behavioral constraint injection into Reflect prompts
package reflect_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"iclude/internal/config"
	"iclude/internal/model"
	reflectpkg "iclude/internal/reflect"
	"iclude/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockContextStore 简易 ContextStore mock / Simple ContextStore mock for testing
type mockContextStore struct {
	contexts map[string]*model.Context
}

func (m *mockContextStore) Create(_ context.Context, c *model.Context) error {
	m.contexts[c.ID] = c
	return nil
}

func (m *mockContextStore) Get(_ context.Context, id string) (*model.Context, error) {
	c, ok := m.contexts[id]
	if !ok {
		return nil, model.ErrContextNotFound
	}
	return c, nil
}

func (m *mockContextStore) GetByPath(_ context.Context, _ string) (*model.Context, error) {
	return nil, model.ErrContextNotFound
}

func (m *mockContextStore) Update(_ context.Context, _ *model.Context) error { return nil }
func (m *mockContextStore) Delete(_ context.Context, _ string) error        { return nil }
func (m *mockContextStore) ListChildren(_ context.Context, _ string) ([]*model.Context, error) {
	return nil, nil
}
func (m *mockContextStore) ListSubtree(_ context.Context, _ string) ([]*model.Context, error) {
	return nil, nil
}
func (m *mockContextStore) Move(_ context.Context, _, _ string) error              { return nil }
func (m *mockContextStore) IncrementMemoryCount(_ context.Context, _ string) error { return nil }
func (m *mockContextStore) IncrementMemoryCountBy(_ context.Context, _ string, _ int) error {
	return nil
}
func (m *mockContextStore) DecrementMemoryCount(_ context.Context, _ string) error { return nil }

// compile-time check / 编译期验证接口实现
var _ store.ContextStore = (*mockContextStore)(nil)

// TestBuildSystemPrompt_NoContextID 无 ContextID 时返回基础提示词 / Returns base prompt when ContextID is empty
func TestBuildSystemPrompt_NoContextID(t *testing.T) {
	engine := reflectpkg.NewReflectEngine(nil, nil, nil, &mockLLMProvider{}, config.ReflectConfig{})

	prompt := engine.BuildSystemPrompt(context.Background(), "")

	assert.Contains(t, prompt, "You are a reflection engine")
	assert.NotContains(t, prompt, "Context behavioral constraints")
}

// TestBuildSystemPrompt_NilContextStore nil ContextStore 时返回基础提示词 / Returns base prompt when ContextStore is nil
func TestBuildSystemPrompt_NilContextStore(t *testing.T) {
	engine := reflectpkg.NewReflectEngine(nil, nil, nil, &mockLLMProvider{}, config.ReflectConfig{})

	prompt := engine.BuildSystemPrompt(context.Background(), "some-id")

	assert.Contains(t, prompt, "You are a reflection engine")
	assert.NotContains(t, prompt, "Context behavioral constraints")
}

// TestBuildSystemPrompt_AllFields ContextID + 全部 3 字段 → 提示词包含所有约束 / All 3 behavioral fields present
func TestBuildSystemPrompt_AllFields(t *testing.T) {
	cs := &mockContextStore{
		contexts: map[string]*model.Context{
			"ctx-1": {
				ID:          "ctx-1",
				Name:        "Test Context",
				Mission:     "Help users understand Go concurrency",
				Directives:  "Always provide code examples\nUse simple language",
				Disposition: "Patient and thorough",
				CreatedAt:   time.Now(),
				UpdatedAt:   time.Now(),
			},
		},
	}

	engine := reflectpkg.NewReflectEngine(nil, nil, cs, &mockLLMProvider{}, config.ReflectConfig{})

	prompt := engine.BuildSystemPrompt(context.Background(), "ctx-1")

	assert.Contains(t, prompt, "Context behavioral constraints:")
	assert.Contains(t, prompt, "Mission: Help users understand Go concurrency")
	assert.Contains(t, prompt, "Directives:\nAlways provide code examples\nUse simple language")
	assert.Contains(t, prompt, "Disposition: Patient and thorough")
}

// TestBuildSystemPrompt_PartialFields 仅部分字段非空 → 只包含非空字段 / Only non-empty fields included
func TestBuildSystemPrompt_PartialFields(t *testing.T) {
	tests := []struct {
		name        string
		ctx         *model.Context
		wantParts   []string
		absentParts []string
	}{
		{
			name: "mission only",
			ctx: &model.Context{
				ID:        "ctx-m",
				Name:      "Mission Only",
				Mission:   "Build reliable systems",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			wantParts:   []string{"Mission: Build reliable systems"},
			absentParts: []string{"Directives:", "Disposition:"},
		},
		{
			name: "directives only",
			ctx: &model.Context{
				ID:         "ctx-d",
				Name:       "Directives Only",
				Directives: "Be concise",
				CreatedAt:  time.Now(),
				UpdatedAt:  time.Now(),
			},
			wantParts:   []string{"Directives:\nBe concise"},
			absentParts: []string{"Mission:", "Disposition:"},
		},
		{
			name: "disposition only",
			ctx: &model.Context{
				ID:          "ctx-p",
				Name:        "Disposition Only",
				Disposition: "Formal and precise",
				CreatedAt:   time.Now(),
				UpdatedAt:   time.Now(),
			},
			wantParts:   []string{"Disposition: Formal and precise"},
			absentParts: []string{"Mission:", "Directives:"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cs := &mockContextStore{
				contexts: map[string]*model.Context{tt.ctx.ID: tt.ctx},
			}
			engine := reflectpkg.NewReflectEngine(nil, nil, cs, &mockLLMProvider{}, config.ReflectConfig{})

			prompt := engine.BuildSystemPrompt(context.Background(), tt.ctx.ID)

			assert.Contains(t, prompt, "Context behavioral constraints:")
			for _, part := range tt.wantParts {
				assert.Contains(t, prompt, part)
			}
			for _, absent := range tt.absentParts {
				assert.NotContains(t, prompt, absent)
			}
		})
	}
}

// TestBuildSystemPrompt_EmptyBehavioralFields Context 存在但行为字段全空 → 返回基础提示词 / Context found but all behavioral fields empty
func TestBuildSystemPrompt_EmptyBehavioralFields(t *testing.T) {
	cs := &mockContextStore{
		contexts: map[string]*model.Context{
			"ctx-empty": {
				ID:        "ctx-empty",
				Name:      "Empty Behavioral",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
		},
	}

	engine := reflectpkg.NewReflectEngine(nil, nil, cs, &mockLLMProvider{}, config.ReflectConfig{})

	prompt := engine.BuildSystemPrompt(context.Background(), "ctx-empty")

	assert.Contains(t, prompt, "You are a reflection engine")
	assert.NotContains(t, prompt, "Context behavioral constraints")
}

// TestBuildSystemPrompt_ContextNotFound Context 不存在时降级为基础提示词 / Context not found falls back to base prompt
func TestBuildSystemPrompt_ContextNotFound(t *testing.T) {
	cs := &mockContextStore{contexts: map[string]*model.Context{}}

	engine := reflectpkg.NewReflectEngine(nil, nil, cs, &mockLLMProvider{}, config.ReflectConfig{})

	prompt := engine.BuildSystemPrompt(context.Background(), "nonexistent-id")

	assert.Contains(t, prompt, "You are a reflection engine")
	assert.NotContains(t, prompt, "Context behavioral constraints")
}

// TestBuildSystemPrompt_BasePromptIntact 注入约束后基础提示词完整保留 / Base prompt remains intact after injection
func TestBuildSystemPrompt_BasePromptIntact(t *testing.T) {
	cs := &mockContextStore{
		contexts: map[string]*model.Context{
			"ctx-check": {
				ID:        "ctx-check",
				Name:      "Integrity Check",
				Mission:   "Test integrity",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
		},
	}

	engine := reflectpkg.NewReflectEngine(nil, nil, cs, &mockLLMProvider{}, config.ReflectConfig{})

	withConstraints := engine.BuildSystemPrompt(context.Background(), "ctx-check")
	baseOnly := engine.BuildSystemPrompt(context.Background(), "")

	// 注入后的提示词应以基础提示词开头 / Prompt with constraints should start with base prompt
	require.True(t, strings.HasPrefix(withConstraints, baseOnly))
	// 约束部分在基础提示词之后 / Constraints come after base prompt
	assert.Contains(t, withConstraints[len(baseOnly):], "Mission: Test integrity")
}
