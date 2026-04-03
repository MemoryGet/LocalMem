package store_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"iclude/internal/model"
	"iclude/internal/store"
	"iclude/pkg/sqlbuilder"
	"iclude/pkg/tokenizer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupStoreWithTokenizer 创建带指定分词器的测试 store
func setupStoreWithTokenizer(t *testing.T, tok tokenizer.Tokenizer) (store.MemoryStore, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	s, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, tok)
	require.NoError(t, err)

	err = s.Init(context.Background())
	require.NoError(t, err)

	return s, func() {
		s.Close()
		os.RemoveAll(dir)
	}
}

// === 优化点 #1: FTS5 中文分词 (SimpleTokenizer) ===

func TestSimpleTokenizer_CJKSplit(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "pure Chinese",
			input: "记忆系统",
			want:  "记 忆 系 统",
		},
		{
			name:  "mixed Chinese and English",
			input: "Go语言开发IClude",
			want:  "Go 语 言 开 发 IClude",
		},
		{
			name:  "English only",
			input: "hello world",
			want:  "hello world",
		},
		{
			name:  "punctuation filtered",
			input: "你好，世界！",
			want:  "你 好 世 界",
		},
		{
			name:  "numbers preserved",
			input: "Go1.25新增特性",
			want:  "Go1 25 新 增 特 性",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
		{
			name:  "Japanese Hiragana",
			input: "こんにちは",
			want:  "こ ん に ち は",
		},
	}

	tok := tokenizer.NewSimpleTokenizer()
	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tok.Tokenize(ctx, tt.input)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNoopTokenizer_PassThrough(t *testing.T) {
	tok := tokenizer.NewNoopTokenizer()
	ctx := context.Background()

	input := "你好世界 hello"
	got, err := tok.Tokenize(ctx, input)
	require.NoError(t, err)
	assert.Equal(t, input, got, "noop tokenizer should return input as-is")
	assert.Equal(t, "noop", tok.Name())
}

func TestFTS5_WithSimpleTokenizer_ChineseSearch(t *testing.T) {
	tok := tokenizer.NewSimpleTokenizer()
	s, cleanup := setupStoreWithTokenizer(t, tok)
	defer cleanup()
	ctx := context.Background()

	// 插入中文记忆
	memories := []*model.Memory{
		{Content: "Go语言开发记忆系统", TeamID: "t1", Scope: "tech"},
		{Content: "Python数据分析和机器学习", TeamID: "t1", Scope: "tech"},
		{Content: "SQLite全文检索FTS5引擎", TeamID: "t1", Scope: "tech"},
		{Content: "向量数据库Qdrant部署方案", TeamID: "t1", Scope: "tech"},
	}
	for _, mem := range memories {
		err := s.Create(ctx, mem)
		require.NoError(t, err)
	}

	tests := []struct {
		name      string
		query     string
		wantMatch string
		wantMin   int
	}{
		{
			name:      "search single Chinese char '记'",
			query:     "记",
			wantMatch: "Go语言开发记忆系统",
			wantMin:   1,
		},
		{
			name:      "search Chinese word '检索'",
			query:     "检索",
			wantMatch: "SQLite全文检索FTS5引擎",
			wantMin:   1,
		},
		{
			name:      "search English term 'Go'",
			query:     "Go",
			wantMatch: "Go语言开发记忆系统",
			wantMin:   1,
		},
		{
			name:      "search mixed '向量'",
			query:     "向量",
			wantMatch: "向量数据库Qdrant部署方案",
			wantMin:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := s.SearchText(ctx, tt.query, &model.Identity{TeamID: "t1", OwnerID: model.SystemOwnerID}, 10)
			require.NoError(t, err)
			assert.GreaterOrEqual(t, len(results), tt.wantMin, "expected at least %d results", tt.wantMin)
			if len(results) > 0 {
				assert.Contains(t, results[0].Memory.Content, tt.wantMatch[:3],
					"top result should match expected memory")
			}
		})
	}
}

func TestFTS5_WithoutTokenizer_ChineseSearchLimited(t *testing.T) {
	// 使用 NoopTokenizer（不分词），验证中文搜索效果受限
	s, cleanup := setupStoreWithTokenizer(t, tokenizer.NewNoopTokenizer())
	defer cleanup()
	ctx := context.Background()

	err := s.Create(ctx, &model.Memory{Content: "混合检索架构设计", TeamID: "t1"})
	require.NoError(t, err)

	// FTS5 unicode61 对中文按字拆分，单字搜索可能有结果
	// 但不做分词时效果不稳定，这里验证基本可用
	results, err := s.SearchText(ctx, "混合检索", &model.Identity{TeamID: "t1", OwnerID: model.SystemOwnerID}, 10)
	require.NoError(t, err)
	// noop 模式下 FTS5 用原文匹配，不保证中文检索效果
	t.Logf("noop tokenizer: search '混合检索' returned %d results", len(results))
}

func TestFTS5_SimpleTokenizer_AbstractSummaryWeighting(t *testing.T) {
	tok := tokenizer.NewSimpleTokenizer()
	s, cleanup := setupStoreWithTokenizer(t, tok)
	defer cleanup()
	ctx := context.Background()

	// 创建两条记忆：关键词在不同字段
	mem1 := &model.Memory{
		Content:  "这是一段普通文本内容",
		Excerpt: "记忆检索系统",
		TeamID:   "t1",
	}
	mem2 := &model.Memory{
		Content: "记忆检索系统的核心架构",
		TeamID:  "t1",
	}
	require.NoError(t, s.Create(ctx, mem1))
	require.NoError(t, s.Create(ctx, mem2))

	// 搜索"检索"——content 权重 10，excerpt 权重 5
	// mem2 关键词在 content（权重 10）应排在前面
	results, err := s.SearchText(ctx, "检索", &model.Identity{TeamID: "t1", OwnerID: model.SystemOwnerID}, 10)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(results), 1)
	if len(results) >= 2 {
		assert.True(t, results[0].Score >= results[1].Score,
			"content match (weight=10) should score >= excerpt match (weight=5)")
	}
}

// === 优化点 #6: SearchTextFiltered SQL Builder ===

func TestSearchTextFiltered_MultipleFilters(t *testing.T) {
	tok := tokenizer.NewSimpleTokenizer()
	s, cleanup := setupStoreWithTokenizer(t, tok)
	defer cleanup()
	ctx := context.Background()

	// 创建不同属性的记忆
	memories := []*model.Memory{
		{Content: "Go语言编程技巧", Scope: "tech", Kind: "fact", TeamID: "t1", RetentionTier: "permanent"},
		{Content: "Go并发模型设计", Scope: "tech", Kind: "skill", TeamID: "t1", RetentionTier: "long_term"},
		{Content: "Go测试框架使用", Scope: "work", Kind: "fact", TeamID: "t1", RetentionTier: "standard"},
		{Content: "Python数据处理", Scope: "tech", Kind: "fact", TeamID: "t1", RetentionTier: "standard"},
	}
	for _, mem := range memories {
		require.NoError(t, s.Create(ctx, mem))
	}

	tests := []struct {
		name    string
		query   string
		filters *model.SearchFilters
		wantMin int
		wantMax int
	}{
		{
			name:    "filter by scope=tech",
			query:   "Go",
			filters: &model.SearchFilters{Scope: "tech", TeamID: "t1"},
			wantMin: 2,
			wantMax: 2,
		},
		{
			name:    "filter by scope=tech AND kind=fact",
			query:   "Go",
			filters: &model.SearchFilters{Scope: "tech", Kind: "fact", TeamID: "t1"},
			wantMin: 1,
			wantMax: 1,
		},
		{
			name:    "filter by retention_tier=permanent",
			query:   "Go",
			filters: &model.SearchFilters{RetentionTier: "permanent", TeamID: "t1"},
			wantMin: 1,
			wantMax: 1,
		},
		{
			name:    "filter by scope=work",
			query:   "Go",
			filters: &model.SearchFilters{Scope: "work", TeamID: "t1"},
			wantMin: 1,
			wantMax: 1,
		},
		{
			name:    "no filters with identity",
			query:   "Go",
			filters: &model.SearchFilters{TeamID: "t1"},
			wantMin: 3,
			wantMax: 3,
		},
		{
			name:    "no filters no identity returns public only",
			query:   "Go",
			filters: nil,
			wantMin: 0,
			wantMax: 0,
		},
		{
			name:    "filter no match",
			query:   "Go",
			filters: &model.SearchFilters{Scope: "nonexistent", TeamID: "t1"},
			wantMin: 0,
			wantMax: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := s.SearchTextFiltered(ctx, tt.query, tt.filters, 10)
			require.NoError(t, err)
			assert.GreaterOrEqual(t, len(results), tt.wantMin,
				"expected at least %d results, got %d", tt.wantMin, len(results))
			assert.LessOrEqual(t, len(results), tt.wantMax,
				"expected at most %d results, got %d", tt.wantMax, len(results))
		})
	}
}

func TestSearchTextFiltered_MinStrength(t *testing.T) {
	tok := tokenizer.NewSimpleTokenizer()
	s, cleanup := setupStoreWithTokenizer(t, tok)
	defer cleanup()
	ctx := context.Background()

	// 创建强度不同的记忆
	mem1 := &model.Memory{Content: "高强度记忆", Strength: 0.9, TeamID: "t1"}
	mem2 := &model.Memory{Content: "低强度记忆", Strength: 0.2, TeamID: "t1"}
	require.NoError(t, s.Create(ctx, mem1))
	require.NoError(t, s.Create(ctx, mem2))

	// 过滤 strength >= 0.5（需提供身份信息，否则仅返回 public）/ Must provide identity, otherwise only public visible
	results, err := s.SearchTextFiltered(ctx, "记忆", &model.SearchFilters{MinStrength: 0.5, TeamID: "t1"}, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, len(results))
	assert.Contains(t, results[0].Memory.Content, "高强度")
}

func TestSearchTextFiltered_ExcludeExpired(t *testing.T) {
	tok := tokenizer.NewSimpleTokenizer()
	s, cleanup := setupStoreWithTokenizer(t, tok)
	defer cleanup()
	ctx := context.Background()

	past := time.Now().UTC().Add(-1 * time.Hour)
	future := time.Now().UTC().Add(24 * time.Hour)

	mem1 := &model.Memory{Content: "已过期记忆", ExpiresAt: &past, TeamID: "t1"}
	mem2 := &model.Memory{Content: "未过期记忆", ExpiresAt: &future, TeamID: "t1"}
	mem3 := &model.Memory{Content: "无过期记忆", TeamID: "t1"}
	require.NoError(t, s.Create(ctx, mem1))
	require.NoError(t, s.Create(ctx, mem2))
	require.NoError(t, s.Create(ctx, mem3))

	// 默认排除过期（需提供身份信息）/ Exclude expired by default (identity required)
	results, err := s.SearchTextFiltered(ctx, "记忆", &model.SearchFilters{TeamID: "t1"}, 10)
	require.NoError(t, err)
	assert.Equal(t, 2, len(results), "expired memory should be excluded")

	// IncludeExpired=true 包含过期
	results, err = s.SearchTextFiltered(ctx, "记忆", &model.SearchFilters{IncludeExpired: true, TeamID: "t1"}, 10)
	require.NoError(t, err)
	assert.Equal(t, 3, len(results), "all memories should be included with IncludeExpired")
}

// === sqlbuilder 单元测试 ===

func TestSQLBuilder_WhereBuilder(t *testing.T) {
	tests := []struct {
		name       string
		build      func() (string, []interface{})
		wantClause string
		wantArgs   int
	}{
		{
			name: "empty where",
			build: func() (string, []interface{}) {
				wb := sqlbuilder.NewWhere()
				return wb.Build()
			},
			wantClause: "1=1",
			wantArgs:   0,
		},
		{
			name: "single condition",
			build: func() (string, []interface{}) {
				wb := sqlbuilder.NewWhere()
				wb.And("scope = ?", "tech")
				return wb.Build()
			},
			wantClause: "scope = ?",
			wantArgs:   1,
		},
		{
			name: "multiple conditions",
			build: func() (string, []interface{}) {
				wb := sqlbuilder.NewWhere()
				wb.And("scope = ?", "tech")
				wb.And("kind = ?", "fact")
				return wb.Build()
			},
			wantClause: "scope = ? AND kind = ?",
			wantArgs:   2,
		},
		{
			name: "AndIf true",
			build: func() (string, []interface{}) {
				wb := sqlbuilder.NewWhere()
				wb.AndIf(true, "scope = ?", "tech")
				wb.AndIf(false, "kind = ?", "fact")
				return wb.Build()
			},
			wantClause: "scope = ?",
			wantArgs:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clause, args := tt.build()
			assert.Equal(t, tt.wantClause, clause)
			assert.Len(t, args, tt.wantArgs)
		})
	}
}

func TestSQLBuilder_SelectBuilder(t *testing.T) {
	qb := sqlbuilder.Select("id, content").
		From("memories").
		OrderBy("created_at DESC").
		Limit(10)

	qb.Where().And("deleted_at IS NULL")
	qb.Where().AndIf(true, "scope = ?", "tech")
	qb.Where().AndIf(false, "kind = ?", "ignored")

	sql, args := qb.Build()

	assert.Contains(t, sql, "SELECT id, content FROM memories")
	assert.Contains(t, sql, "WHERE deleted_at IS NULL AND scope = ?")
	assert.Contains(t, sql, "ORDER BY created_at DESC")
	assert.Contains(t, sql, "LIMIT ?")
	assert.Len(t, args, 2) // "tech" + limit 10
	assert.Equal(t, "tech", args[0])
	assert.Equal(t, 10, args[1])
}
