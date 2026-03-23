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
	suiteStore     = "记忆 CRUD"
	suiteStoreIcon = "\U0001F4BE"
	suiteStoreDesc = "MemoryStore 核心操作: 创建、读取、更新、删除、列表"
)

func TestStore_CreateAndGet(t *testing.T) {
	tc := testreport.NewCase(t, suiteStore, suiteStoreIcon, suiteStoreDesc,
		"创建记忆并读取")
	defer tc.Done()

	tc.Input("content", "Go 1.25 新增了迭代器语法")
	tc.Input("scope", "tech")
	tc.Input("kind", "fact")
	tc.Input("retention_tier", "long_term")

	s := newTestStore(t, tokenizer.NewSimpleTokenizer())
	defer s.Close()
	tc.Step("创建 SQLite store")

	ctx := context.Background()
	mem := &model.Memory{
		Content:       "Go 1.25 新增了迭代器语法",
		Scope:         "tech",
		Kind:          "fact",
		RetentionTier: "long_term",
		TeamID:        "team-test",
	}
	err := s.Create(ctx, mem)
	require.NoError(t, err)
	tc.Step("调用 Create()", fmt.Sprintf("生成 ID=%s", mem.ID))

	got, err := s.Get(ctx, mem.ID)
	require.NoError(t, err)
	tc.Step("调用 Get(id)", fmt.Sprintf("ID=%s", mem.ID))

	assert.Equal(t, mem.Content, got.Content)
	assert.Equal(t, "tech", got.Scope)
	assert.Equal(t, "fact", got.Kind)
	assert.Equal(t, "long_term", got.RetentionTier)
	assert.Equal(t, 1.0, got.Strength)
	assert.Equal(t, 1, got.AccessCount) // Get 自动 +1
	tc.Step("验证所有字段", "content/scope/kind/retention_tier/strength/access_count 全部匹配")

	tc.Output("ID", got.ID)
	tc.Output("content", got.Content)
	tc.Output("scope", got.Scope)
	tc.Output("strength", fmt.Sprintf("%.1f (默认值)", got.Strength))
	tc.Output("access_count", fmt.Sprintf("%d (Get 自动递增)", got.AccessCount))
}

func TestStore_Update(t *testing.T) {
	tc := testreport.NewCase(t, suiteStore, suiteStoreIcon, suiteStoreDesc,
		"更新记忆")
	defer tc.Done()

	s := newTestStore(t, tokenizer.NewSimpleTokenizer())
	defer s.Close()
	ctx := context.Background()

	mem := &model.Memory{Content: "原始内容", Kind: "note", TeamID: "t1"}
	require.NoError(t, s.Create(ctx, mem))
	tc.Input("原始 content", "原始内容")
	tc.Input("原始 kind", "note")
	tc.Step("创建初始记忆", fmt.Sprintf("ID=%s", mem.ID))

	mem.Content = "更新后的内容"
	mem.Kind = "fact"
	tc.Input("新 content", "更新后的内容")
	tc.Input("新 kind", "fact")

	err := s.Update(ctx, mem)
	require.NoError(t, err)
	tc.Step("调用 Update()")

	got, err := s.Get(ctx, mem.ID)
	require.NoError(t, err)
	assert.Equal(t, "更新后的内容", got.Content)
	assert.Equal(t, "fact", got.Kind)
	tc.Step("验证更新生效", fmt.Sprintf("content=%q kind=%s", got.Content, got.Kind))

	// 验证 FTS5 同步
	results, err := s.SearchText(ctx, "更新", &model.Identity{TeamID: "t1", OwnerID: model.SystemOwnerID}, 10)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(results), 1)
	tc.Step("验证 FTS5 索引同步更新", fmt.Sprintf("搜索 '更新' 命中 %d 条", len(results)))

	tc.Output("更新后 content", got.Content)
	tc.Output("更新后 kind", got.Kind)
	tc.Output("FTS5 同步", "已更新")
}

func TestStore_SoftDeleteAndRestore(t *testing.T) {
	tc := testreport.NewCase(t, suiteStore, suiteStoreIcon, suiteStoreDesc,
		"软删除与恢复")
	defer tc.Done()

	s := newTestStore(t, tokenizer.NewSimpleTokenizer())
	defer s.Close()
	ctx := context.Background()

	mem := &model.Memory{Content: "待删除记忆", TeamID: "t1"}
	require.NoError(t, s.Create(ctx, mem))
	tc.Input("ID", mem.ID)
	tc.Input("content", "待删除记忆")
	tc.Step("创建记忆")

	err := s.SoftDelete(ctx, mem.ID)
	require.NoError(t, err)
	tc.Step("调用 SoftDelete()", "设置 deleted_at 时间戳")

	_, err = s.Get(ctx, mem.ID)
	assert.ErrorIs(t, err, model.ErrMemoryNotFound)
	tc.Step("验证: Get() 返回 404", "软删除后不可见")

	err = s.Restore(ctx, mem.ID)
	require.NoError(t, err)
	tc.Step("调用 Restore()", "清除 deleted_at")

	got, err := s.Get(ctx, mem.ID)
	require.NoError(t, err)
	assert.Equal(t, "待删除记忆", got.Content)
	tc.Step("验证: 恢复后可正常读取", fmt.Sprintf("content=%q", got.Content))

	tc.Output("软删除后 Get", "ErrMemoryNotFound (404)")
	tc.Output("恢复后 Get", fmt.Sprintf("content=%q (数据完整)", got.Content))
}

func TestStore_Reinforce(t *testing.T) {
	tc := testreport.NewCase(t, suiteStore, suiteStoreIcon, suiteStoreDesc,
		"记忆强化 (Reinforce)")
	defer tc.Done()

	tc.Input("初始 strength", "1.0 (默认)")
	tc.Input("公式", "strength += 0.1 * (1 - strength)")
	tc.Input("强化次数", "3")

	s := newTestStore(t, tokenizer.NewSimpleTokenizer())
	defer s.Close()
	ctx := context.Background()

	mem := &model.Memory{Content: "待强化记忆", TeamID: "t1"}
	require.NoError(t, s.Create(ctx, mem))
	tc.Step("创建记忆", fmt.Sprintf("初始 strength=%.1f", mem.Strength))

	// 先设置 strength 为 0.5 以便观察变化
	mem.Strength = 0.5
	require.NoError(t, s.Update(ctx, mem))
	tc.Step("手动设置 strength=0.5", "便于观察强化效果")

	for i := 1; i <= 3; i++ {
		err := s.Reinforce(ctx, mem.ID)
		require.NoError(t, err)
		got, _ := s.Get(ctx, mem.ID)
		tc.Step(fmt.Sprintf("第 %d 次强化", i),
			fmt.Sprintf("strength=%.4f, reinforced_count=%d", got.Strength, got.ReinforcedCount))
	}

	got, _ := s.Get(ctx, mem.ID)
	// 0.5 → 0.55 → 0.595 → 0.6355
	assert.InDelta(t, 0.6355, got.Strength, 0.001)
	assert.Equal(t, 3, got.ReinforcedCount)
	tc.Step("验证最终状态", fmt.Sprintf("strength=%.4f (期望≈0.6355), count=%d", got.Strength, got.ReinforcedCount))

	tc.Output("最终 strength", fmt.Sprintf("%.4f", got.Strength))
	tc.Output("reinforced_count", fmt.Sprintf("%d", got.ReinforcedCount))
	tc.Output("计算过程", "0.5 → 0.55 → 0.595 → 0.6355")
}

func TestStore_CleanupExpired(t *testing.T) {
	tc := testreport.NewCase(t, suiteStore, suiteStoreIcon, suiteStoreDesc,
		"过期记忆清理")
	defer tc.Done()

	past := time.Now().UTC().Add(-1 * time.Hour)
	future := time.Now().UTC().Add(24 * time.Hour)
	tc.Input("过期记忆", fmt.Sprintf("expires_at=%s (1小时前)", past.Format("15:04:05")))
	tc.Input("正常记忆", fmt.Sprintf("expires_at=%s (24小时后)", future.Format("15:04:05")))

	s := newTestStore(t, tokenizer.NewSimpleTokenizer())
	defer s.Close()
	ctx := context.Background()

	mem1 := &model.Memory{Content: "已过期", ExpiresAt: &past, TeamID: "t1"}
	mem2 := &model.Memory{Content: "未过期", ExpiresAt: &future, TeamID: "t1"}
	require.NoError(t, s.Create(ctx, mem1))
	require.NoError(t, s.Create(ctx, mem2))
	tc.Step("插入 2 条记忆", "1 已过期 + 1 未过期")

	cleaned, err := s.CleanupExpired(ctx)
	require.NoError(t, err)
	tc.Step("调用 CleanupExpired()", fmt.Sprintf("清理了 %d 条", cleaned))

	assert.Equal(t, 1, cleaned)
	tc.Step("验证: 清理数量为 1")

	_, err = s.Get(ctx, mem1.ID)
	assert.ErrorIs(t, err, model.ErrMemoryNotFound)
	tc.Step("验证: 过期记忆不可见 (已软删除)")

	got, err := s.Get(ctx, mem2.ID)
	require.NoError(t, err)
	assert.Equal(t, "未过期", got.Content)
	tc.Step("验证: 未过期记忆正常", fmt.Sprintf("content=%q", got.Content))

	tc.Output("清理数量", fmt.Sprintf("%d", cleaned))
	tc.Output("过期记忆状态", "已软删除 (deleted_at 非空)")
	tc.Output("正常记忆状态", "不受影响")
}

func TestStore_Timeline(t *testing.T) {
	tc := testreport.NewCase(t, suiteStore, suiteStoreIcon, suiteStoreDesc,
		"时间线查询 (ListTimeline)")
	defer tc.Done()

	tc.Input("查询参数", "scope=tech, limit=10")

	s := newTestStore(t, tokenizer.NewSimpleTokenizer())
	defer s.Close()
	ctx := context.Background()

	seedMemories(t, s, []*model.Memory{
		{Content: "第一条", Scope: "tech", TeamID: "t1"},
		{Content: "第二条", Scope: "tech", TeamID: "t1"},
		{Content: "其他scope", Scope: "work", TeamID: "t1"},
	})
	tc.Step("插入 3 条记忆", "tech:2条, work:1条")

	results, err := s.ListTimeline(ctx, &model.TimelineRequest{Scope: "tech", Limit: 10})
	require.NoError(t, err)
	tc.Step("调用 ListTimeline(scope=tech)", fmt.Sprintf("返回 %d 条", len(results)))

	assert.Equal(t, 2, len(results))
	tc.Step("验证: 仅返回 scope=tech 的记忆")

	// 验证按时间倒序
	if len(results) >= 2 {
		assert.True(t, !results[0].UpdatedAt.Before(results[1].UpdatedAt))
		tc.Step("验证: 按时间倒序排列")
	}

	for i, r := range results {
		tc.Output(fmt.Sprintf("时间线[%d]", i), fmt.Sprintf("content=%q scope=%s", r.Content, r.Scope))
	}
}
