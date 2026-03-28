# MCP 渐进式披露优化 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 优化 MCP scan/fetch 工作流，通过 abstract 自动生成 + scan 索引增强 + 工具描述引导，将 MCP 场景 token 消耗降低 ~80%。

**Architecture:** Manager.Create() 异步生成 abstract 摘要（复用现有 LLM Provider），Heartbeat 兜底补漏。Scan 索引增加 tags/scope 字段（批量 JOIN 查询）。工具描述引导 agent 优先走 scan→fetch。

**Tech Stack:** Go, SQLite, LLM Provider (existing)

**Spec:** `docs/superpowers/specs/2026-03-29-progressive-disclosure-design.md`

---

### Task 1: MemoryStore 新增 ListMissingAbstract 接口与实现

**Files:**
- Modify: `internal/store/interfaces.go` — MemoryStore 接口新增方法
- Modify: `internal/store/sqlite.go` — SQLite 实现
- Test: `testing/store/sqlite_test.go`

- [ ] **Step 1: 写失败测试**

在 `testing/store/sqlite_test.go` 末尾追加：

```go
func TestListMissingAbstract(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()

	tests := []struct {
		name     string
		setup    func()
		limit    int
		wantLen  int
	}{
		{
			name: "returns memories with empty abstract",
			setup: func() {
				s.createTestMemory(t, ctx, &model.Memory{Content: "no abstract", Abstract: ""})
				s.createTestMemory(t, ctx, &model.Memory{Content: "has abstract", Abstract: "summary"})
				s.createTestMemory(t, ctx, &model.Memory{Content: "also no abstract", Abstract: ""})
			},
			limit:   10,
			wantLen: 2,
		},
		{
			name:    "respects limit",
			limit:   1,
			wantLen: 1,
		},
		{
			name: "excludes soft-deleted",
			setup: func() {
				mem := s.createTestMemory(t, ctx, &model.Memory{Content: "deleted no abstract", Abstract: ""})
				_ = s.store.SoftDelete(ctx, mem.ID)
			},
			limit:   10,
			wantLen: 2, // still 2 from previous setup
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.setup != nil {
				tt.setup()
			}
			results, err := s.store.ListMissingAbstract(ctx, tt.limit)
			if err != nil {
				t.Fatalf("ListMissingAbstract() error = %v", err)
			}
			if len(results) != tt.wantLen {
				t.Errorf("got %d results, want %d", len(results), tt.wantLen)
			}
			for _, mem := range results {
				if mem.Abstract != "" {
					t.Errorf("returned memory %s has non-empty abstract", mem.ID)
				}
			}
		})
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./testing/store/ -run TestListMissingAbstract -v -count=1`
Expected: 编译失败 — `ListMissingAbstract` 未定义

- [ ] **Step 3: 在接口中添加方法声明**

在 `internal/store/interfaces.go` 的 `MemoryStore` 接口中，`GetOwnerID` 方法后面添加：

```go
	// ListMissingAbstract 列出缺少摘要的记忆（排除软删除）/ List memories missing abstract (excluding soft-deleted)
	ListMissingAbstract(ctx context.Context, limit int) ([]*model.Memory, error)
```

- [ ] **Step 4: 在 SQLite 中实现**

在 `internal/store/sqlite.go` 末尾添加：

```go
// ListMissingAbstract 列出缺少摘要的记忆 / List memories missing abstract
func (s *SQLiteMemoryStore) ListMissingAbstract(ctx context.Context, limit int) ([]*model.Memory, error) {
	if limit <= 0 {
		limit = 20
	}
	query := `SELECT ` + memoryColumns + ` FROM memories WHERE (abstract = '' OR abstract IS NULL) AND deleted_at IS NULL ORDER BY created_at DESC LIMIT ?`
	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list missing abstract: %w", err)
	}
	defer rows.Close()

	var memories []*model.Memory
	for rows.Next() {
		mem, err := s.scanMemoryFromRows(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan memory row: %w", err)
		}
		memories = append(memories, mem)
	}
	return memories, rows.Err()
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./testing/store/ -run TestListMissingAbstract -v -count=1`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add internal/store/interfaces.go internal/store/sqlite.go testing/store/sqlite_test.go
git commit -m "feat(store): add ListMissingAbstract to MemoryStore interface"
```

---

### Task 2: TagStore 新增 GetTagNamesByMemoryIDs 接口与实现

**Files:**
- Modify: `internal/store/interfaces.go` — TagStore 接口新增方法
- Modify: `internal/store/sqlite_tags.go` — SQLite 实现
- Test: `testing/store/sqlite_test.go`

- [ ] **Step 1: 写失败测试**

在 `testing/store/sqlite_test.go` 末尾追加：

```go
func TestGetTagNamesByMemoryIDs(t *testing.T) {
	s := setupTestStore(t)
	ctx := context.Background()

	// setup: 2 memories, 2 tags
	mem1 := s.createTestMemory(t, ctx, &model.Memory{Content: "mem1"})
	mem2 := s.createTestMemory(t, ctx, &model.Memory{Content: "mem2"})
	tag1 := &model.Tag{Name: "architecture", Scope: "test"}
	tag2 := &model.Tag{Name: "golang", Scope: "test"}
	if err := s.tagStore.CreateTag(ctx, tag1); err != nil {
		t.Fatal(err)
	}
	if err := s.tagStore.CreateTag(ctx, tag2); err != nil {
		t.Fatal(err)
	}
	_ = s.tagStore.TagMemory(ctx, mem1.ID, tag1.ID)
	_ = s.tagStore.TagMemory(ctx, mem1.ID, tag2.ID)
	_ = s.tagStore.TagMemory(ctx, mem2.ID, tag1.ID)

	tests := []struct {
		name    string
		ids     []string
		wantMap map[string]int // memory_id → expected tag count
	}{
		{
			name:    "both memories",
			ids:     []string{mem1.ID, mem2.ID},
			wantMap: map[string]int{mem1.ID: 2, mem2.ID: 1},
		},
		{
			name:    "single memory",
			ids:     []string{mem2.ID},
			wantMap: map[string]int{mem2.ID: 1},
		},
		{
			name:    "empty ids",
			ids:     []string{},
			wantMap: map[string]int{},
		},
		{
			name:    "nonexistent id",
			ids:     []string{"nonexistent"},
			wantMap: map[string]int{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := s.tagStore.GetTagNamesByMemoryIDs(ctx, tt.ids)
			if err != nil {
				t.Fatalf("GetTagNamesByMemoryIDs() error = %v", err)
			}
			for id, wantCount := range tt.wantMap {
				if got := len(result[id]); got != wantCount {
					t.Errorf("memory %s: got %d tags, want %d", id, got, wantCount)
				}
			}
		})
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./testing/store/ -run TestGetTagNamesByMemoryIDs -v -count=1`
Expected: 编译失败 — `GetTagNamesByMemoryIDs` 未定义

- [ ] **Step 3: 在接口中添加方法声明**

在 `internal/store/interfaces.go` 的 `TagStore` 接口中，`GetMemoriesByTag` 方法后面添加：

```go
	// GetTagNamesByMemoryIDs 批量获取多条记忆的标签名 / Batch get tag names for multiple memories
	GetTagNamesByMemoryIDs(ctx context.Context, ids []string) (map[string][]string, error)
```

- [ ] **Step 4: 在 SQLite 中实现**

在 `internal/store/sqlite_tags.go` 末尾添加：

```go
// GetTagNamesByMemoryIDs 批量获取多条记忆的标签名 / Batch get tag names for multiple memories
func (s *SQLiteTagStore) GetTagNamesByMemoryIDs(ctx context.Context, ids []string) (map[string][]string, error) {
	result := make(map[string][]string)
	if len(ids) == 0 {
		return result, nil
	}

	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}

	query := fmt.Sprintf(
		`SELECT mt.memory_id, t.name FROM memory_tags mt JOIN tags t ON mt.tag_id = t.id WHERE mt.memory_id IN (%s) ORDER BY t.name`,
		strings.Join(placeholders, ","),
	)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to batch get tag names: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var memID, tagName string
		if err := rows.Scan(&memID, &tagName); err != nil {
			return nil, fmt.Errorf("failed to scan tag name row: %w", err)
		}
		result[memID] = append(result[memID], tagName)
	}
	return result, rows.Err()
}
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./testing/store/ -run TestGetTagNamesByMemoryIDs -v -count=1`
Expected: PASS

- [ ] **Step 6: 提交**

```bash
git add internal/store/interfaces.go internal/store/sqlite_tags.go testing/store/sqlite_test.go
git commit -m "feat(store): add GetTagNamesByMemoryIDs to TagStore interface"
```

---

### Task 3: Manager.Create() 异步生成 abstract

**Files:**
- Modify: `internal/memory/manager.go` — Create() 增加异步 abstract 生成
- Test: `testing/memory/manager_test.go`

**依赖:** Manager 需要访问 LLM Provider。当前 Manager 不持有 LLM，需通过 Extractor 间接使用或新增字段。

- [ ] **Step 1: Manager 新增 llm 字段**

在 `internal/memory/manager.go` 的 `Manager` struct 中添加：

```go
	llm          llm.Provider   // 可为 nil / may be nil (used for abstract generation)
```

在 `NewManager` 函数签名中新增参数（放在 `extractor` 之后，`cfg` 之前）：

```go
func NewManager(memStore store.MemoryStore, vecStore store.VectorStore, embedder store.Embedder, tagStore store.TagStore, contextStore store.ContextStore, extractor *Extractor, llmProvider llm.Provider, cfg ManagerConfig, taskQueue ...TaskEnqueuer) *Manager {
```

并在构造体中赋值：`llm: llmProvider,`

- [ ] **Step 2: 添加 asyncGenerateAbstract 方法**

在 `internal/memory/manager.go` 中 `asyncExtract` 方法附近添加：

```go
// asyncGenerateAbstract 异步生成记忆摘要 / Async generate memory abstract via LLM
func (m *Manager) asyncGenerateAbstract(memoryID, content string) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		abstract, err := m.generateAbstract(ctx, content)
		if err != nil {
			logger.Warn("async abstract generation failed",
				zap.String("memory_id", memoryID),
				zap.Error(err),
			)
			return
		}

		mem, err := m.memStore.Get(ctx, memoryID)
		if err != nil {
			logger.Warn("failed to get memory for abstract update",
				zap.String("memory_id", memoryID),
				zap.Error(err),
			)
			return
		}
		mem.Abstract = abstract
		if err := m.memStore.Update(ctx, mem); err != nil {
			logger.Warn("failed to update memory abstract",
				zap.String("memory_id", memoryID),
				zap.Error(err),
			)
		}
	}()
}

// generateAbstract 调用 LLM 生成一句话摘要 / Call LLM to generate one-line abstract
func (m *Manager) generateAbstract(ctx context.Context, content string) (string, error) {
	temp := 0.1
	resp, err := m.llm.Chat(ctx, &llm.ChatRequest{
		Messages: []llm.ChatMessage{
			{Role: "system", Content: "用一句话（≤100字）概括以下内容的核心信息，直接输出摘要，不加前缀。"},
			{Role: "user", Content: content},
		},
		Temperature: &temp,
	})
	if err != nil {
		return "", fmt.Errorf("llm chat failed: %w", err)
	}
	abstract := strings.TrimSpace(resp.Content)
	// 截断过长的摘要 / Truncate overly long abstracts
	if len([]rune(abstract)) > 150 {
		abstract = string([]rune(abstract)[:150])
	}
	return abstract, nil
}
```

需要在 import 中添加 `"strings"` 和 `"iclude/internal/llm"`。

- [ ] **Step 3: 在 Create() 中调用异步 abstract 生成**

在 `internal/memory/manager.go` 的 `Create()` 方法中，`return mem, nil` 之前（`asyncExtract` 代码块之后），添加：

```go
	// 异步生成摘要（content 短则直接用 content，否则调 LLM）/ Async abstract generation
	if mem.Abstract == "" && m.llm != nil {
		if len([]rune(mem.Content)) <= 50 {
			mem.Abstract = mem.Content
			_ = m.memStore.Update(ctx, mem)
		} else {
			m.asyncGenerateAbstract(mem.ID, mem.Content)
		}
	}
```

- [ ] **Step 4: 更新所有 NewManager 调用点**

需要更新 `internal/bootstrap/wiring.go` 和所有测试中的 `NewManager` 调用，在 `extractor` 参数后面加上 `llmProvider`（或 `nil`）。

在 `internal/bootstrap/wiring.go` 中找到 `NewManager` 调用，加入 LLM provider 参数。

在测试文件中的 `NewManager` 调用处传 `nil`（测试不需要 LLM）。

- [ ] **Step 5: 运行构建确认通过**

Run: `go build ./...`
Expected: 编译成功

- [ ] **Step 6: 运行已有测试确认不破坏**

Run: `go test ./testing/memory/ -v -count=1`
Expected: 全部 PASS

- [ ] **Step 7: 提交**

```bash
git add internal/memory/manager.go internal/bootstrap/wiring.go
git commit -m "feat(memory): async abstract generation on Create via LLM"
```

---

### Task 4: Heartbeat 兜底补漏 abstract

**Files:**
- Modify: `internal/heartbeat/engine.go` — 新增 runAbstractBackfill 方法
- Test: `testing/heartbeat/heartbeat_test.go`

**依赖:** Task 1 (ListMissingAbstract), Task 3 (generateAbstract pattern)

- [ ] **Step 1: Engine 新增 llm 直接依赖用于 abstract 生成**

Heartbeat Engine 已有 `llm llm.Provider` 字段，无需新增。但需要新增 `generateAbstract` 方法。

在 `internal/heartbeat/engine.go` 末尾添加：

```go
// runAbstractBackfill 补充缺少摘要的记忆 / Backfill memories missing abstract
func (e *Engine) runAbstractBackfill(ctx context.Context) error {
	if e.llm == nil {
		return nil
	}

	const batchLimit = 20
	memories, err := e.memStore.ListMissingAbstract(ctx, batchLimit)
	if err != nil {
		return fmt.Errorf("list missing abstract: %w", err)
	}
	if len(memories) == 0 {
		return nil
	}

	logger.Info("heartbeat: backfilling abstracts", zap.Int("count", len(memories)))

	filled := 0
	for _, mem := range memories {
		// 短内容直接用 content / Short content uses content directly
		if len([]rune(mem.Content)) <= 50 {
			mem.Abstract = mem.Content
		} else {
			abstract, err := e.generateAbstract(ctx, mem.Content)
			if err != nil {
				logger.Warn("heartbeat: abstract generation failed, skipping",
					zap.String("memory_id", mem.ID),
					zap.Error(err),
				)
				continue
			}
			mem.Abstract = abstract
		}

		if err := e.memStore.Update(ctx, mem); err != nil {
			logger.Warn("heartbeat: abstract update failed",
				zap.String("memory_id", mem.ID),
				zap.Error(err),
			)
			continue
		}
		filled++
	}

	logger.Info("heartbeat: abstract backfill completed", zap.Int("filled", filled))
	return nil
}

// generateAbstract 调用 LLM 生成摘要 / Generate abstract via LLM
func (e *Engine) generateAbstract(ctx context.Context, content string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	temp := 0.1
	resp, err := e.llm.Chat(ctx, &llm.ChatRequest{
		Messages: []llm.ChatMessage{
			{Role: "system", Content: "用一句话（≤100字）概括以下内容的核心信息，直接输出摘要，不加前缀。"},
			{Role: "user", Content: content},
		},
		Temperature: &temp,
	})
	if err != nil {
		return "", fmt.Errorf("llm chat failed: %w", err)
	}
	abstract := strings.TrimSpace(resp.Content)
	if len([]rune(abstract)) > 150 {
		abstract = string([]rune(abstract)[:150])
	}
	return abstract, nil
}
```

需要在 import 中添加 `"strings"`, `"time"`, `"iclude/internal/llm"`。

- [ ] **Step 2: 在 Run() 中调用 abstractBackfill**

在 `internal/heartbeat/engine.go` 的 `Run()` 方法中，矛盾检测之后添加：

```go
	// 4. 摘要补漏（需要 LLM）/ Abstract backfill (requires LLM)
	if e.llm != nil {
		if err := e.runAbstractBackfill(ctx); err != nil {
			logger.Warn("heartbeat: abstract backfill failed", zap.Error(err))
		}
	}
```

- [ ] **Step 3: 运行构建确认通过**

Run: `go build ./...`
Expected: 编译成功

- [ ] **Step 4: 运行已有测试确认不破坏**

Run: `go test ./testing/heartbeat/ -v -count=1`
Expected: 全部 PASS

- [ ] **Step 5: 提交**

```bash
git add internal/heartbeat/engine.go
git commit -m "feat(heartbeat): add abstract backfill task for missing abstracts"
```

---

### Task 5: Scan 索引增强 — 返回 tags + scope

**Files:**
- Modify: `internal/mcp/tools/scan.go` — ScanResultItem 新增字段 + 批量查 tags
- Test: `testing/mcp/fetch_test.go`

**依赖:** Task 2 (GetTagNamesByMemoryIDs)

- [ ] **Step 1: ScanTool 新增 tagStore 依赖**

在 `internal/mcp/tools/scan.go` 中：

将 `ScanTool` struct 改为：

```go
type ScanTool struct {
	retriever MemoryRetriever
	tagStore  TagStoreReader // 可为 nil / may be nil
}
```

新增接口（在同文件顶部）：

```go
// TagStoreReader scan 工具需要的标签查询接口 / Tag query interface for scan tool
type TagStoreReader interface {
	GetTagNamesByMemoryIDs(ctx context.Context, ids []string) (map[string][]string, error)
}
```

修改构造函数：

```go
func NewScanTool(retriever MemoryRetriever, tagStore TagStoreReader) *ScanTool {
	return &ScanTool{retriever: retriever, tagStore: tagStore}
}
```

- [ ] **Step 2: ScanResultItem 增加 Scope 和 Tags**

```go
type ScanResultItem struct {
	ID            string     `json:"id"`
	Title         string     `json:"title"`
	Score         float64    `json:"score"`
	Source        string     `json:"source"`
	Kind          string     `json:"kind,omitempty"`
	Scope         string     `json:"scope,omitempty"`
	Tags          []string   `json:"tags,omitempty"`
	HappenedAt    *time.Time `json:"happened_at,omitempty"`
	TokenEstimate int        `json:"token_estimate"`
}
```

- [ ] **Step 3: Execute() 中填充 scope 和批量查 tags**

在 `Execute()` 方法中，构建 items 循环里为每个 item 填充 `Scope: r.Memory.Scope`。

在循环结束后，批量查 tags：

```go
	// 批量查询标签 / Batch query tags
	if t.tagStore != nil && len(items) > 0 {
		ids := make([]string, len(items))
		for i, item := range items {
			ids[i] = item.ID
		}
		tagMap, err := t.tagStore.GetTagNamesByMemoryIDs(ctx, ids)
		if err != nil {
			logger.Warn("scan: failed to batch get tags", zap.Error(err))
		} else {
			for i, item := range items {
				if tags, ok := tagMap[item.ID]; ok {
					items[i].Tags = tags
				}
			}
		}
	}
```

- [ ] **Step 4: 更新 NewScanTool 调用点**

在 `cmd/mcp/main.go` 或 `internal/bootstrap/wiring.go` 中找到 `NewScanTool` 调用，传入 tagStore。

- [ ] **Step 5: 运行构建确认通过**

Run: `go build ./...`
Expected: 编译成功

- [ ] **Step 6: 运行已有测试确认不破坏**

Run: `go test ./testing/mcp/ -v -count=1`
Expected: 全部 PASS

- [ ] **Step 7: 提交**

```bash
git add internal/mcp/tools/scan.go cmd/mcp/main.go
git commit -m "feat(mcp): scan index returns tags and scope fields"
```

---

### Task 6: 工具描述优化

**Files:**
- Modify: `internal/mcp/tools/scan.go` — Definition() 描述更新
- Modify: `internal/mcp/tools/recall.go` — Definition() 描述更新
- Modify: `internal/mcp/tools/fetch.go` — Definition() 描述更新

- [ ] **Step 1: 更新 iclude_scan 描述**

在 `internal/mcp/tools/scan.go` 的 `Definition()` 中替换 `Description`：

```go
Description: "**Primary search tool.** Returns compact index (ID, title, score, tags, scope, token estimate) for efficient browsing. Use this FIRST, then iclude_fetch for full content on selected items. Saves ~10x tokens vs iclude_recall.",
```

- [ ] **Step 2: 更新 iclude_recall 描述**

在 `internal/mcp/tools/recall.go` 的 `Definition()` 中替换 `Description`：

```go
Description: "Retrieve full memory content via semantic + full-text search. **High token cost** — prefer iclude_scan + iclude_fetch for MCP workflows. Use only when you need all results with full content in one call.",
```

- [ ] **Step 3: 更新 iclude_fetch 描述**

在 `internal/mcp/tools/fetch.go` 的 `Definition()` 中替换 `Description`：

```go
Description: "Fetch full memory content by IDs. Use after iclude_scan to get details for selected items only. Accepts up to 20 IDs per call.",
```

- [ ] **Step 4: 运行构建确认通过**

Run: `go build ./...`
Expected: 编译成功

- [ ] **Step 5: 提交**

```bash
git add internal/mcp/tools/scan.go internal/mcp/tools/recall.go internal/mcp/tools/fetch.go
git commit -m "docs(mcp): optimize tool descriptions to guide scan→fetch workflow"
```

---

### Task 7: 集成验证

- [ ] **Step 1: 全量构建**

Run: `go build ./...`
Expected: 编译成功

- [ ] **Step 2: 全量测试（分包执行）**

```bash
go test ./testing/store/ -v -count=1
go test ./testing/memory/ -v -count=1
go test ./testing/heartbeat/ -v -count=1
go test ./testing/mcp/ -v -count=1
go test ./testing/api/ -v -count=1
```

Expected: 全部 PASS

- [ ] **Step 3: 最终提交（如有遗漏修复）**

```bash
git add -A
git commit -m "fix: integration fixes for progressive disclosure feature"
```
