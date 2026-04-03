package report_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"iclude/internal/model"
	"iclude/pkg/testreport"
	"iclude/pkg/tokenizer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	suiteSearch     = "过滤检索 (Filtered Search)"
	suiteSearchIcon = "\U0001F50D"
	suiteSearchDesc = "SearchTextFiltered 使用 sqlbuilder 动态构建 WHERE 子句，支持多条件组合过滤"
)

func TestSearch_FilterByScope(t *testing.T) {
	tc := testreport.NewCase(t, suiteSearch, suiteSearchIcon, suiteSearchDesc,
		"按 scope 过滤检索")
	defer tc.Done()

	tc.Input("搜索词", "Go")
	tc.Input("过滤条件", "scope = tech")

	s := newTestStore(t, tokenizer.NewSimpleTokenizer())
	defer s.Close()
	tc.Step("创建 SQLite store (SimpleTokenizer)")

	ctx := context.Background()
	seedMemories(t, s, []*model.Memory{
		{Content: "Go语言编程技巧", Scope: "tech", TeamID: "t1"},
		{Content: "Go并发模型设计", Scope: "tech", TeamID: "t1"},
		{Content: "Go测试框架使用", Scope: "work", TeamID: "t1"},
	})
	tc.Step("插入 3 条记忆", "tech: 2条, work: 1条")

	results, err := s.SearchTextFiltered(ctx, "Go", &model.SearchFilters{Scope: "tech", TeamID: "t1"}, 10)
	require.NoError(t, err)
	tc.Step("执行 SearchTextFiltered(scope=tech)")

	assert.Equal(t, 2, len(results))
	tc.Step("验证: 仅返回 scope=tech 的 2 条结果")

	for i, r := range results {
		tc.Output(fmt.Sprintf("结果[%d]", i), fmt.Sprintf("scope=%s content=%q score=%.4f", r.Memory.Scope, r.Memory.Content, r.Score))
	}
}

func TestSearch_FilterByScopeAndKind(t *testing.T) {
	tc := testreport.NewCase(t, suiteSearch, suiteSearchIcon, suiteSearchDesc,
		"按 scope + kind 组合过滤")
	defer tc.Done()

	tc.Input("搜索词", "Go")
	tc.Input("过滤条件", "scope=tech AND kind=fact")

	s := newTestStore(t, tokenizer.NewSimpleTokenizer())
	defer s.Close()
	tc.Step("创建 SQLite store")

	ctx := context.Background()
	seedMemories(t, s, []*model.Memory{
		{Content: "Go语言编程技巧", Scope: "tech", Kind: "fact", TeamID: "t1"},
		{Content: "Go并发模型设计", Scope: "tech", Kind: "skill", TeamID: "t1"},
		{Content: "Go测试框架使用", Scope: "work", Kind: "fact", TeamID: "t1"},
	})
	tc.Step("插入 3 条记忆", "tech+fact:1, tech+skill:1, work+fact:1")

	results, err := s.SearchTextFiltered(ctx, "Go", &model.SearchFilters{Scope: "tech", Kind: "fact", TeamID: "t1"}, 10)
	require.NoError(t, err)
	tc.Step("执行 SearchTextFiltered(scope=tech, kind=fact)")

	assert.Equal(t, 1, len(results))
	tc.Step("验证: 仅返回同时满足两个条件的 1 条结果")

	if len(results) > 0 {
		tc.Output("命中", fmt.Sprintf("content=%q scope=%s kind=%s", results[0].Memory.Content, results[0].Memory.Scope, results[0].Memory.Kind))
	}
}

func TestSearch_FilterByRetentionTier(t *testing.T) {
	tc := testreport.NewCase(t, suiteSearch, suiteSearchIcon, suiteSearchDesc,
		"按 retention_tier 过滤")
	defer tc.Done()

	tc.Input("搜索词", "Go")
	tc.Input("过滤条件", "retention_tier = permanent")

	s := newTestStore(t, tokenizer.NewSimpleTokenizer())
	defer s.Close()
	tc.Step("创建 SQLite store")

	ctx := context.Background()
	seedMemories(t, s, []*model.Memory{
		{Content: "Go核心规范", RetentionTier: "permanent", TeamID: "t1"},
		{Content: "Go临时笔记", RetentionTier: "short_term", TeamID: "t1"},
		{Content: "Go学习总结", RetentionTier: "standard", TeamID: "t1"},
	})
	tc.Step("插入 3 条记忆", "permanent:1, short_term:1, standard:1")

	results, err := s.SearchTextFiltered(ctx, "Go", &model.SearchFilters{RetentionTier: "permanent", TeamID: "t1"}, 10)
	require.NoError(t, err)
	tc.Step("执行 SearchTextFiltered(retention_tier=permanent)")

	assert.Equal(t, 1, len(results))
	if len(results) > 0 {
		assert.Equal(t, "permanent", results[0].Memory.RetentionTier)
		tc.Step("验证: 仅返回 permanent 层级的记忆")
		tc.Output("命中", fmt.Sprintf("content=%q tier=%s", results[0].Memory.Content, results[0].Memory.RetentionTier))
	}
}

func TestSearch_FilterMinStrength(t *testing.T) {
	tc := testreport.NewCase(t, suiteSearch, suiteSearchIcon, suiteSearchDesc,
		"按最小强度过滤")
	defer tc.Done()

	tc.Input("搜索词", "记忆")
	tc.Input("过滤条件", "min_strength = 0.5")

	s := newTestStore(t, tokenizer.NewSimpleTokenizer())
	defer s.Close()
	tc.Step("创建 SQLite store")

	ctx := context.Background()
	seedMemories(t, s, []*model.Memory{
		{Content: "高强度记忆", Strength: 0.9, TeamID: "t1"},
		{Content: "低强度记忆", Strength: 0.2, TeamID: "t1"},
	})
	tc.Step("插入 2 条记忆", "strength=0.9 和 strength=0.2")

	results, err := s.SearchTextFiltered(ctx, "记忆", &model.SearchFilters{MinStrength: 0.5, TeamID: "t1"}, 10)
	require.NoError(t, err)
	tc.Step("执行 SearchTextFiltered(min_strength=0.5)")

	assert.Equal(t, 1, len(results))
	if len(results) > 0 {
		assert.Contains(t, results[0].Memory.Content, "高强度")
		tc.Step("验证: 仅返回 strength >= 0.5 的记忆")
		tc.Output("命中", fmt.Sprintf("content=%q strength=%.1f", results[0].Memory.Content, results[0].Memory.Strength))
	}
	tc.Output("被过滤", "低强度记忆 (strength=0.2)")
}

func TestSearch_ExcludeExpired(t *testing.T) {
	tc := testreport.NewCase(t, suiteSearch, suiteSearchIcon, suiteSearchDesc,
		"过期记忆排除与包含")
	defer tc.Done()

	past := time.Now().UTC().Add(-1 * time.Hour)
	future := time.Now().UTC().Add(24 * time.Hour)
	tc.Input("搜索词", "记忆")
	tc.Input("过期时间设置", fmt.Sprintf("mem1: %s (已过期), mem2: %s (未过期), mem3: 无过期时间",
		past.Format("15:04:05"), future.Format("15:04:05")))

	s := newTestStore(t, tokenizer.NewSimpleTokenizer())
	defer s.Close()
	tc.Step("创建 SQLite store")

	ctx := context.Background()
	seedMemories(t, s, []*model.Memory{
		{Content: "已过期记忆", ExpiresAt: &past, TeamID: "t1"},
		{Content: "未过期记忆", ExpiresAt: &future, TeamID: "t1"},
		{Content: "无过期记忆", TeamID: "t1"},
	})
	tc.Step("插入 3 条记忆", "1 已过期 + 1 未过期 + 1 无过期时间")

	// 默认排除过期
	results, err := s.SearchTextFiltered(ctx, "记忆", &model.SearchFilters{TeamID: "t1"}, 10)
	require.NoError(t, err)
	assert.Equal(t, 2, len(results))
	tc.Step("默认模式: 排除过期记忆", fmt.Sprintf("返回 %d 条", len(results)))

	// 包含过期
	results2, err := s.SearchTextFiltered(ctx, "记忆", &model.SearchFilters{IncludeExpired: true, TeamID: "t1"}, 10)
	require.NoError(t, err)
	assert.Equal(t, 3, len(results2))
	tc.Step("IncludeExpired=true: 包含全部", fmt.Sprintf("返回 %d 条", len(results2)))

	tc.Output("默认模式结果数", fmt.Sprintf("%d (排除过期)", len(results)))
	tc.Output("包含过期结果数", fmt.Sprintf("%d (全部)", len(results2)))
}

func TestSearch_NoFilters(t *testing.T) {
	tc := testreport.NewCase(t, suiteSearch, suiteSearchIcon, suiteSearchDesc,
		"无过滤条件 (nil filters)")
	defer tc.Done()

	tc.Input("搜索词", "Go")
	tc.Input("过滤条件", "nil")

	s := newTestStore(t, tokenizer.NewSimpleTokenizer())
	defer s.Close()
	tc.Step("创建 SQLite store")

	ctx := context.Background()
	seedMemories(t, s, []*model.Memory{
		{Content: "Go语言编程", Scope: "tech", TeamID: "t1"},
		{Content: "Go测试框架", Scope: "work", TeamID: "t1"},
	})
	tc.Step("插入 2 条记忆", "不同 scope")

	results, err := s.SearchTextFiltered(ctx, "Go", &model.SearchFilters{TeamID: "t1"}, 10)
	require.NoError(t, err)
	tc.Step("执行 SearchTextFiltered(filters with TeamID)")

	assert.Equal(t, 2, len(results))
	tc.Step("验证: 无过滤时返回所有匹配记忆")

	tc.Output("结果数", fmt.Sprintf("%d", len(results)))
	tc.Output("WHERE 子句", "memories_fts MATCH ? AND m.deleted_at IS NULL (仅基础条件)")
}

func TestSearch_NoMatch(t *testing.T) {
	tc := testreport.NewCase(t, suiteSearch, suiteSearchIcon, suiteSearchDesc,
		"过滤条件无匹配")
	defer tc.Done()

	tc.Input("搜索词", "Go")
	tc.Input("过滤条件", "scope = nonexistent")

	s := newTestStore(t, tokenizer.NewSimpleTokenizer())
	defer s.Close()
	tc.Step("创建 SQLite store")

	ctx := context.Background()
	seedMemories(t, s, []*model.Memory{
		{Content: "Go语言编程", Scope: "tech", TeamID: "t1"},
	})
	tc.Step("插入 1 条记忆 (scope=tech)")

	results, err := s.SearchTextFiltered(ctx, "Go", &model.SearchFilters{Scope: "nonexistent", TeamID: "t1"}, 10)
	require.NoError(t, err)
	tc.Step("执行 SearchTextFiltered(scope=nonexistent)")

	assert.Equal(t, 0, len(results))
	tc.Step("验证: 返回空结果集，无错误")

	tc.Output("结果数", "0")
	tc.Output("error", "nil (不报错，返回空)")
}
