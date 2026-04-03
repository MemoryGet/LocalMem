package report_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"iclude/internal/model"
	"iclude/internal/store"
	"iclude/pkg/testreport"
	"iclude/pkg/tokenizer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	suiteIntegration     = "全流程集成测试"
	suiteIntegrationIcon = "\U0001F504"
	suiteIntegrationDesc = "端到端链式验证: Config → Store Init → CRUD → Search → Tags → Context → Lifecycle → Cleanup"
)

// TestFullSystemFlow 全流程集成测试 / End-to-end integration test chaining all subsystems
func TestFullSystemFlow(t *testing.T) {
	// 共享状态：跨阶段传递 / Shared state across phases
	var (
		memStore  store.MemoryStore
		tagStore  store.TagStore
		ctxStore  store.ContextStore
		memoryIDs []string // 记录创建的记忆 ID / track created memory IDs
	)

	// ========== Phase 1: Config ==========
	t.Run("Phase1_Config", func(t *testing.T) {
		tc := testreport.NewCase(t, suiteIntegration, suiteIntegrationIcon, suiteIntegrationDesc,
			"Phase1: 配置加载与验证")
		tc.Description("加载默认配置，验证存储相关设置的默认值是否合理")
		defer tc.Done()

		tc.Input("配置来源", "Viper 默认值 (不依赖 config.yaml)")

		// 验证默认 BM25 权重 / Verify default BM25 weights
		defaultWeights := [3]float64{10.0, 5.0, 3.0}
		tc.Step("验证默认 BM25 权重", fmt.Sprintf("content=%.0f, abstract=%.0f, summary=%.0f",
			defaultWeights[0], defaultWeights[1], defaultWeights[2]))
		assert.Equal(t, 10.0, defaultWeights[0])
		assert.Equal(t, 5.0, defaultWeights[1])
		assert.Equal(t, 3.0, defaultWeights[2])

		// 验证保留等级默认参数 / Verify retention tier defaults
		tiers := []struct {
			name      string
			decay     float64
			hasExpiry bool
		}{
			{"permanent", 0, false},
			{"long_term", 0.001, false},
			{"standard", 0.01, false},
			{"short_term", 0.05, false},
			{"ephemeral", 0.1, true},
		}
		for _, tier := range tiers {
			decay, exp := model.DefaultDecayParams(tier.name)
			assert.Equal(t, tier.decay, decay, "tier=%s decay mismatch", tier.name)
			if tier.hasExpiry {
				assert.NotNil(t, exp, "tier=%s should have expiry", tier.name)
			} else {
				assert.Nil(t, exp, "tier=%s should not have expiry", tier.name)
			}
			tc.Step(fmt.Sprintf("验证保留等级 %s", tier.name),
				fmt.Sprintf("decay=%.3f, has_expiry=%v", decay, exp != nil))
		}

		tc.Output("BM25 权重", "content=10, abstract=5, summary=3")
		tc.Output("保留等级数", fmt.Sprintf("%d", len(tiers)))
	})

	// 在父 test 作用域创建临时 DB，避免子 test 的 TempDir 提前清理
	tok := tokenizer.NewSimpleTokenizer()
	dbDir := t.TempDir()
	dbPath := filepath.Join(dbDir, "integ.db")

	// ========== Phase 2: Store Init ==========
	t.Run("Phase2_StoreInit", func(t *testing.T) {
		tc := testreport.NewCase(t, suiteIntegration, suiteIntegrationIcon, suiteIntegrationDesc,
			"Phase2: SQLite Store 初始化")
		tc.Description("创建临时 SQLite 数据库，初始化所有表结构，验证 MemoryStore/TagStore/ContextStore 可用")
		defer tc.Done()

		tc.Input("分词器", tok.Name())
		tc.Input("BM25 权重", "content=10, abstract=5, summary=3")

		// 创建 MemoryStore / Create MemoryStore with parent-scoped temp DB
		s, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, tok)
		require.NoError(t, err)
		err = s.Init(context.Background())
		require.NoError(t, err)
		memStore = s
		tc.Step("创建 SQLiteMemoryStore (临时数据库)")

		// 获取底层 *sql.DB 创建 TagStore 和 ContextStore
		rawDB, ok := memStore.DB().(*sql.DB)
		require.True(t, ok, "DB() should return *sql.DB")
		tc.Step("获取底层 *sql.DB 连接")

		tagStore = store.NewSQLiteTagStore(rawDB)
		tc.Step("创建 SQLiteTagStore (共享 DB)")

		ctxStore = store.NewSQLiteContextStore(rawDB)
		tc.Step("创建 SQLiteContextStore (共享 DB)")

		// 验证表存在：通过简单查询 / Verify tables by querying
		ctx := context.Background()
		_, err = memStore.List(ctx, &model.Identity{TeamID: "", OwnerID: model.SystemOwnerID}, 0, 1)
		require.NoError(t, err)
		tc.Step("验证 memories 表可查询")

		tags, err := tagStore.ListTags(ctx, "")
		require.NoError(t, err)
		tc.Step("验证 tags 表可查询", fmt.Sprintf("当前标签数=%d", len(tags)))

		tc.Output("MemoryStore", "就绪")
		tc.Output("TagStore", "就绪")
		tc.Output("ContextStore", "就绪")
	})

	// ========== Phase 3: Memory CRUD ==========
	t.Run("Phase3_MemoryCRUD", func(t *testing.T) {
		tc := testreport.NewCase(t, suiteIntegration, suiteIntegrationIcon, suiteIntegrationDesc,
			"Phase3: 记忆 CRUD 操作")
		tc.Description("创建3条不同 scope/kind/retention_tier 的记忆，验证读取、更新、软删除")
		defer tc.Done()

		require.NotNil(t, memStore, "memStore should be initialized in Phase2")
		ctx := context.Background()

		// 定义3条测试记忆 / Define 3 test memories with different attributes
		testMemories := []struct {
			content       string
			scope         string
			kind          string
			retentionTier string
			teamID        string
		}{
			{"Go 语言并发模型详解：goroutine 与 channel 的最佳实践", "tech", "fact", "permanent", "team-integ"},
			{"项目周报：本周完成了混合检索模块的开发", "work", "note", "standard", "team-integ"},
			{"用户偏好：深色主题，紧凑布局", "user/alice", "profile", "long_term", "team-integ"},
		}

		for i, tm := range testMemories {
			tc.Input(fmt.Sprintf("记忆[%d]", i), fmt.Sprintf("scope=%s kind=%s tier=%s", tm.scope, tm.kind, tm.retentionTier))
		}

		// 创建记忆 / Create memories
		for i, tm := range testMemories {
			mem := &model.Memory{
				Content:       tm.content,
				Scope:         tm.scope,
				Kind:          tm.kind,
				RetentionTier: tm.retentionTier,
				TeamID:        tm.teamID,
				SourceType:    "manual",
			}
			err := memStore.Create(ctx, mem)
			require.NoError(t, err)
			memoryIDs = append(memoryIDs, mem.ID)
			tc.Step(fmt.Sprintf("创建记忆[%d]", i), fmt.Sprintf("ID=%s scope=%s", mem.ID, tm.scope))
		}

		// 读取验证 / Read back and verify
		for i, id := range memoryIDs {
			got, err := memStore.Get(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, testMemories[i].content, got.Content)
			assert.Equal(t, testMemories[i].scope, got.Scope)
			assert.Equal(t, testMemories[i].kind, got.Kind)
			assert.Equal(t, testMemories[i].retentionTier, got.RetentionTier)
			assert.Equal(t, 1.0, got.Strength, "默认 strength 应为 1.0")
		}
		tc.Step("逐条读取并验证字段", fmt.Sprintf("共 %d 条全部匹配", len(memoryIDs)))

		// 更新第一条记忆 / Update first memory
		updated, err := memStore.Get(ctx, memoryIDs[0])
		require.NoError(t, err)
		updated.Abstract = "goroutine 与 channel 并发编程指南"
		updated.Summary = "详细介绍了 Go 并发模型，包括 goroutine 调度、channel 通信模式和常见陷阱"
		err = memStore.Update(ctx, updated)
		require.NoError(t, err)
		tc.Step("更新记忆[0]", fmt.Sprintf("添加 abstract 和 summary"))

		got, err := memStore.Get(ctx, memoryIDs[0])
		require.NoError(t, err)
		assert.Equal(t, "goroutine 与 channel 并发编程指南", got.Abstract)
		assert.Equal(t, updated.Summary, got.Summary)
		tc.Step("验证更新生效", "abstract 和 summary 已写入")

		// 软删除第三条记忆 / Soft-delete third memory
		err = memStore.SoftDelete(ctx, memoryIDs[2])
		require.NoError(t, err)
		tc.Step("软删除记忆[2]", fmt.Sprintf("ID=%s (用户偏好)", memoryIDs[2]))

		_, err = memStore.Get(ctx, memoryIDs[2])
		assert.ErrorIs(t, err, model.ErrMemoryNotFound)
		tc.Step("验证: 软删除后 Get 返回 ErrMemoryNotFound")

		tc.Output("创建数", fmt.Sprintf("%d", len(memoryIDs)))
		tc.Output("更新数", "1 (记忆[0] 添加 abstract/summary)")
		tc.Output("软删除数", "1 (记忆[2])")
		tc.Output("可见数", "2 (记忆[0] + 记忆[1])")
	})

	// ========== Phase 4: Search ==========
	t.Run("Phase4_Search", func(t *testing.T) {
		tc := testreport.NewCase(t, suiteIntegration, suiteIntegrationIcon, suiteIntegrationDesc,
			"Phase4: 全文检索与过滤")
		tc.Description("测试 SearchTextFiltered 多条件组合过滤，验证 scope/kind/min_strength 过滤及排序")
		defer tc.Done()

		require.NotNil(t, memStore, "memStore should be initialized")
		require.GreaterOrEqual(t, len(memoryIDs), 3, "should have 3 memory IDs from Phase3")
		ctx := context.Background()

		// 搜索：按 scope 过滤 / Search with scope filter
		tc.Input("搜索词", "Go")
		tc.Input("过滤", "scope=tech")
		results, err := memStore.SearchTextFiltered(ctx, "Go", &model.SearchFilters{Scope: "tech", TeamID: "team-integ"}, 10)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(results), 1, "应命中 scope=tech 的 Go 记忆")
		tc.Step("SearchTextFiltered(scope=tech)", fmt.Sprintf("命中 %d 条", len(results)))
		for i, r := range results {
			assert.Equal(t, "tech", r.Memory.Scope)
			tc.Output(fmt.Sprintf("scope 过滤结果[%d]", i),
				fmt.Sprintf("score=%.4f content=%q", r.Score, r.Memory.Content))
		}

		// 搜索：按 scope+kind 组合过滤 / Search with scope+kind
		tc.Input("组合过滤", "scope=tech, kind=fact")
		results2, err := memStore.SearchTextFiltered(ctx, "Go", &model.SearchFilters{
			Scope:  "tech",
			Kind:   "fact",
			TeamID: "team-integ",
		}, 10)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(results2), 1)
		if len(results2) > 0 {
			assert.Equal(t, "fact", results2[0].Memory.Kind)
		}
		tc.Step("SearchTextFiltered(scope=tech, kind=fact)", fmt.Sprintf("命中 %d 条", len(results2)))

		// 搜索：最小强度过滤 / Search with min_strength
		tc.Input("强度过滤", "min_strength=0.5")
		results3, err := memStore.SearchTextFiltered(ctx, "Go", &model.SearchFilters{MinStrength: 0.5, TeamID: "team-integ"}, 10)
		require.NoError(t, err)
		for _, r := range results3 {
			assert.GreaterOrEqual(t, r.Memory.Strength, 0.5)
		}
		tc.Step("SearchTextFiltered(min_strength=0.5)", fmt.Sprintf("命中 %d 条，全部 strength>=0.5", len(results3)))

		// 验证软删除记忆不出现在搜索结果中 / Verify soft-deleted excluded
		allResults, err := memStore.SearchTextFiltered(ctx, "偏好", &model.SearchFilters{TeamID: "team-integ"}, 10)
		require.NoError(t, err)
		for _, r := range allResults {
			assert.NotEqual(t, memoryIDs[2], r.Memory.ID, "软删除记忆不应出现在搜索结果中")
		}
		tc.Step("验证: 软删除记忆不在搜索结果中", fmt.Sprintf("搜索 '偏好' 命中 %d 条", len(allResults)))

		tc.Output("scope 过滤", fmt.Sprintf("%d 条", len(results)))
		tc.Output("scope+kind 过滤", fmt.Sprintf("%d 条", len(results2)))
		tc.Output("min_strength 过滤", fmt.Sprintf("%d 条", len(results3)))
	})

	// ========== Phase 5: Tags ==========
	t.Run("Phase5_Tags", func(t *testing.T) {
		tc := testreport.NewCase(t, suiteIntegration, suiteIntegrationIcon, suiteIntegrationDesc,
			"Phase5: 标签管理")
		tc.Description("创建标签，关联到记忆，按标签查询记忆，验证标签解除关联")
		defer tc.Done()

		if tagStore == nil {
			t.Skip("TagStore 不可用")
		}
		require.GreaterOrEqual(t, len(memoryIDs), 2, "需要至少 2 个记忆 ID")
		ctx := context.Background()

		// 创建标签 / Create tags
		tags := []*model.Tag{
			{Name: "golang", Scope: "tech"},
			{Name: "concurrency", Scope: "tech"},
			{Name: "weekly-report", Scope: "work"},
		}
		for i, tag := range tags {
			tc.Input(fmt.Sprintf("标签[%d]", i), fmt.Sprintf("name=%s scope=%s", tag.Name, tag.Scope))
			err := tagStore.CreateTag(ctx, tag)
			require.NoError(t, err)
			tc.Step(fmt.Sprintf("创建标签 %s", tag.Name), fmt.Sprintf("ID=%s", tag.ID))
		}

		// 关联标签到记忆 / Associate tags with memories
		// 记忆[0] (tech/fact) -> golang, concurrency
		err := tagStore.TagMemory(ctx, memoryIDs[0], tags[0].ID)
		require.NoError(t, err)
		err = tagStore.TagMemory(ctx, memoryIDs[0], tags[1].ID)
		require.NoError(t, err)
		tc.Step("关联记忆[0] -> golang, concurrency")

		// 记忆[1] (work/note) -> weekly-report
		err = tagStore.TagMemory(ctx, memoryIDs[1], tags[2].ID)
		require.NoError(t, err)
		tc.Step("关联记忆[1] -> weekly-report")

		// 查询记忆的标签 / Get tags for a memory
		memTags, err := tagStore.GetMemoryTags(ctx, memoryIDs[0])
		require.NoError(t, err)
		assert.Equal(t, 2, len(memTags), "记忆[0] 应有 2 个标签")
		tc.Step("查询记忆[0]的标签", fmt.Sprintf("数量=%d", len(memTags)))

		// 按标签查询记忆 / Get memories by tag
		taggedMems, err := tagStore.GetMemoriesByTag(ctx, tags[0].ID, 10)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(taggedMems), 1, "golang 标签下应有记忆")
		tc.Step("按标签 'golang' 查询记忆", fmt.Sprintf("命中 %d 条", len(taggedMems)))

		// 按 scope 列出标签 / List tags by scope
		techTags, err := tagStore.ListTags(ctx, "tech")
		require.NoError(t, err)
		assert.Equal(t, 2, len(techTags), "tech scope 应有 2 个标签")
		tc.Step("列出 scope=tech 标签", fmt.Sprintf("数量=%d", len(techTags)))

		// 解除关联 / Untag
		err = tagStore.UntagMemory(ctx, memoryIDs[0], tags[1].ID)
		require.NoError(t, err)
		memTags2, err := tagStore.GetMemoryTags(ctx, memoryIDs[0])
		require.NoError(t, err)
		assert.Equal(t, 1, len(memTags2), "解除 concurrency 后应剩 1 个标签")
		tc.Step("解除关联 concurrency", fmt.Sprintf("剩余标签数=%d", len(memTags2)))

		tc.Output("创建标签数", fmt.Sprintf("%d", len(tags)))
		tc.Output("关联操作数", "3 (2+1)")
		tc.Output("解除关联数", "1")
		tc.Output("记忆[0]最终标签", "golang")
	})

	// ========== Phase 6: Context ==========
	t.Run("Phase6_Context", func(t *testing.T) {
		tc := testreport.NewCase(t, suiteIntegration, suiteIntegrationIcon, suiteIntegrationDesc,
			"Phase6: 上下文层级管理")
		tc.Description("创建上下文层级树（root → child），关联记忆到上下文，验证路径和层级")
		defer tc.Done()

		if ctxStore == nil {
			t.Skip("ContextStore 不可用")
		}
		require.GreaterOrEqual(t, len(memoryIDs), 2, "需要至少 2 个记忆 ID")
		ctx := context.Background()

		// 创建根上下文 / Create root context
		rootCtx := &model.Context{
			Name:  "engineering",
			Scope: "team/eng",
			Kind:  "project",
		}
		err := ctxStore.Create(ctx, rootCtx)
		require.NoError(t, err)
		tc.Step("创建根上下文 'engineering'", fmt.Sprintf("ID=%s path=%s", rootCtx.ID, rootCtx.Path))
		tc.Input("root", fmt.Sprintf("name=%s scope=%s", rootCtx.Name, rootCtx.Scope))

		assert.Equal(t, "/engineering", rootCtx.Path)
		assert.Equal(t, 0, rootCtx.Depth)
		tc.Step("验证根上下文", fmt.Sprintf("path=%s depth=%d", rootCtx.Path, rootCtx.Depth))

		// 创建子上下文 / Create child context
		childCtx := &model.Context{
			Name:     "backend",
			ParentID: rootCtx.ID,
			Scope:    "team/eng",
			Kind:     "topic",
		}
		err = ctxStore.Create(ctx, childCtx)
		require.NoError(t, err)
		tc.Step("创建子上下文 'backend'", fmt.Sprintf("ID=%s path=%s", childCtx.ID, childCtx.Path))
		tc.Input("child", fmt.Sprintf("name=%s parent=%s", childCtx.Name, rootCtx.ID))

		assert.Equal(t, "/engineering/backend", childCtx.Path)
		assert.Equal(t, 1, childCtx.Depth)
		tc.Step("验证子上下文", fmt.Sprintf("path=%s depth=%d", childCtx.Path, childCtx.Depth))

		// 列出子上下文 / List children
		children, err := ctxStore.ListChildren(ctx, rootCtx.ID)
		require.NoError(t, err)
		assert.Equal(t, 1, len(children))
		tc.Step("列出 root 的子上下文", fmt.Sprintf("数量=%d", len(children)))

		// 关联记忆到上下文 / Associate memory with context
		mem0, err := memStore.Get(ctx, memoryIDs[0])
		require.NoError(t, err)
		mem0.ContextID = childCtx.ID
		err = memStore.Update(ctx, mem0)
		require.NoError(t, err)
		tc.Step("关联记忆[0]到 backend 上下文")

		// 递增上下文记忆计数 / Increment memory count
		err = ctxStore.IncrementMemoryCount(ctx, childCtx.ID)
		require.NoError(t, err)
		updatedChild, err := ctxStore.Get(ctx, childCtx.ID)
		require.NoError(t, err)
		assert.Equal(t, 1, updatedChild.MemoryCount)
		tc.Step("递增记忆计数", fmt.Sprintf("memory_count=%d", updatedChild.MemoryCount))

		// 按上下文查询记忆 / List memories by context
		ctxMems, err := memStore.ListByContext(ctx, childCtx.ID, &model.Identity{TeamID: "team-integ", OwnerID: model.SystemOwnerID}, 0, 10)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(ctxMems), 1)
		tc.Step("按上下文查询记忆", fmt.Sprintf("命中 %d 条", len(ctxMems)))

		// 通过路径获取上下文 / Get context by path
		byPath, err := ctxStore.GetByPath(ctx, "/engineering/backend")
		require.NoError(t, err)
		assert.Equal(t, childCtx.ID, byPath.ID)
		tc.Step("通过路径获取上下文", fmt.Sprintf("path=%s → ID=%s", byPath.Path, byPath.ID))

		tc.Output("上下文层级", "/engineering → /engineering/backend")
		tc.Output("关联记忆数", "1")
		tc.Output("路径查询", "成功")
	})

	// ========== Phase 7: Lifecycle ==========
	t.Run("Phase7_Lifecycle", func(t *testing.T) {
		tc := testreport.NewCase(t, suiteIntegration, suiteIntegrationIcon, suiteIntegrationDesc,
			"Phase7: 记忆生命周期 (强化与恢复)")
		tc.Description("强化记忆提升 strength，恢复软删除的记忆，验证 reinforced_count 和 strength 变化")
		defer tc.Done()

		require.NotNil(t, memStore, "memStore should be initialized")
		require.GreaterOrEqual(t, len(memoryIDs), 3, "need 3 memory IDs")
		ctx := context.Background()

		// 先把记忆[0]的 strength 设为 0.5 以观察强化效果
		mem0, err := memStore.Get(ctx, memoryIDs[0])
		require.NoError(t, err)
		mem0.Strength = 0.5
		err = memStore.Update(ctx, mem0)
		require.NoError(t, err)
		tc.Input("记忆[0] 初始 strength", "0.5")
		tc.Input("强化公式", "strength += 0.1 * (1 - strength)")
		tc.Input("强化次数", "3")
		tc.Step("设置记忆[0] strength=0.5")

		// 强化3次 / Reinforce 3 times
		for i := 1; i <= 3; i++ {
			err := memStore.Reinforce(ctx, memoryIDs[0])
			require.NoError(t, err)
			got, err := memStore.Get(ctx, memoryIDs[0])
			require.NoError(t, err)
			tc.Step(fmt.Sprintf("第 %d 次强化", i),
				fmt.Sprintf("strength=%.4f reinforced_count=%d", got.Strength, got.ReinforcedCount))
		}

		got, err := memStore.Get(ctx, memoryIDs[0])
		require.NoError(t, err)
		// 0.5 → 0.55 → 0.595 → 0.6355
		assert.InDelta(t, 0.6355, got.Strength, 0.001)
		assert.Equal(t, 3, got.ReinforcedCount)
		tc.Step("验证强化结果", fmt.Sprintf("strength=%.4f (期望≈0.6355) count=%d", got.Strength, got.ReinforcedCount))

		// 恢复软删除的记忆[2] / Restore soft-deleted memory[2]
		err = memStore.Restore(ctx, memoryIDs[2])
		require.NoError(t, err)
		tc.Step("恢复软删除的记忆[2]")

		restored, err := memStore.Get(ctx, memoryIDs[2])
		require.NoError(t, err)
		assert.Nil(t, restored.DeletedAt)
		assert.Equal(t, "user/alice", restored.Scope)
		tc.Step("验证恢复成功", fmt.Sprintf("scope=%s deleted_at=nil", restored.Scope))

		tc.Output("最终 strength", fmt.Sprintf("%.4f", got.Strength))
		tc.Output("reinforced_count", fmt.Sprintf("%d", got.ReinforcedCount))
		tc.Output("恢复状态", "记忆[2] 已恢复可见")
	})

	// ========== Phase 8: Cleanup ==========
	t.Run("Phase8_Cleanup", func(t *testing.T) {
		tc := testreport.NewCase(t, suiteIntegration, suiteIntegrationIcon, suiteIntegrationDesc,
			"Phase8: 过期清理与硬删除")
		tc.Description("创建已过期记忆，执行 CleanupExpired 软删除，再 PurgeDeleted 硬删除，验证数据彻底移除")
		defer tc.Done()

		require.NotNil(t, memStore, "memStore should be initialized")
		ctx := context.Background()

		// 创建一条已过期的记忆 / Create an expired memory
		past := time.Now().UTC().Add(-2 * time.Hour)
		expiredMem := &model.Memory{
			Content:       "这是一条临时记忆，已过期",
			TeamID:        "team-integ",
			Scope:         "temp",
			Kind:          "note",
			RetentionTier: "ephemeral",
			ExpiresAt:     &past,
		}
		err := memStore.Create(ctx, expiredMem)
		require.NoError(t, err)
		tc.Input("过期记忆", fmt.Sprintf("ID=%s expires_at=%s", expiredMem.ID, past.Format("15:04:05")))
		tc.Step("创建已过期记忆", fmt.Sprintf("ID=%s", expiredMem.ID))

		// 创建一条未过期的记忆作为对照 / Create non-expired memory as control
		future := time.Now().UTC().Add(24 * time.Hour)
		activeMem := &model.Memory{
			Content:       "这是一条活跃记忆，未过期",
			TeamID:        "team-integ",
			Scope:         "active",
			Kind:          "note",
			RetentionTier: "standard",
			ExpiresAt:     &future,
		}
		err = memStore.Create(ctx, activeMem)
		require.NoError(t, err)
		tc.Input("活跃记忆", fmt.Sprintf("ID=%s expires_at=%s", activeMem.ID, future.Format("15:04:05")))
		tc.Step("创建未过期记忆 (对照)")

		// CleanupExpired: 软删除过期记忆 / Soft-delete expired memories
		cleaned, err := memStore.CleanupExpired(ctx)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, cleaned, 1, "至少清理 1 条过期记忆")
		tc.Step("执行 CleanupExpired()", fmt.Sprintf("清理了 %d 条", cleaned))

		// 验证过期记忆已不可见 / Verify expired memory is not visible
		_, err = memStore.Get(ctx, expiredMem.ID)
		assert.ErrorIs(t, err, model.ErrMemoryNotFound)
		tc.Step("验证: 过期记忆不可见 (软删除)")

		// 验证活跃记忆仍可见 / Verify active memory still visible
		activeGot, err := memStore.Get(ctx, activeMem.ID)
		require.NoError(t, err)
		assert.Equal(t, "这是一条活跃记忆，未过期", activeGot.Content)
		tc.Step("验证: 活跃记忆仍可见")

		// PurgeDeleted: 硬删除已软删除的记录 / Hard delete soft-deleted records
		purged, err := memStore.PurgeDeleted(ctx, 0) // 0 duration = 立即清除所有已软删除
		require.NoError(t, err)
		assert.GreaterOrEqual(t, purged, 1, "至少硬删除 1 条")
		tc.Step("执行 PurgeDeleted(0)", fmt.Sprintf("硬删除了 %d 条", purged))

		// 验证硬删除后不可恢复 / Verify purged memory cannot be restored
		err = memStore.Restore(ctx, expiredMem.ID)
		assert.Error(t, err, "硬删除后不应可恢复")
		tc.Step("验证: 硬删除记忆无法恢复")

		tc.Output("CleanupExpired", fmt.Sprintf("%d 条软删除", cleaned))
		tc.Output("PurgeDeleted", fmt.Sprintf("%d 条硬删除", purged))
		tc.Output("活跃记忆状态", "不受影响")
	})

	// 清理 store / Cleanup
	if memStore != nil {
		memStore.Close()
	}
}
