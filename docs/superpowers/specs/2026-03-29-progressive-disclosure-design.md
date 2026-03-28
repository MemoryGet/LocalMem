# MCP 渐进式披露优化设计

## 背景

借鉴 [claude-mem](https://github.com/thedotmack/claude-mem) 的渐进式披露理念：先给 agent 轻量索引，让其自主决定获取哪些完整内容，而非一次性灌入全部记忆。IClude 已有 `iclude_scan`（轻量索引）和 `iclude_fetch`（按 ID 取详情）的基础设施，但存在两个短板：

1. `abstract` 字段大多为空，scan 回退到截取 content 前 100 字符，信息量不足以支撑 agent 决策
2. scan 索引缺少 `tags`/`scope`，agent 无法按领域/作用域筛选

## 目标

- MCP 场景 token 消耗降低 ~80%（scan 索引 ~500 tokens vs recall 完整内容 ~5000 tokens）
- agent 通过 scan 索引即可判断哪些记忆值得 fetch
- 不破坏现有 recall API 的向后兼容

## 改动范围

### A. Abstract 自动生成

**触发时机：双重保障**

1. **写入时异步生成**（主路径）：`Manager.Create()` 中，当 `abstract` 为空且 LLM 可用时，启动 goroutine 异步生成摘要并回写
2. **Heartbeat 兜底补漏**（容错路径）：`HeartbeatEngine.Run()` 中新增 abstract 补充任务，扫描 `abstract = '' AND deleted_at IS NULL` 的记忆，批量生成

**LLM Prompt**

```
用一句话（≤100字）概括以下内容的核心信息，直接输出摘要，不加前缀：

{content}
```

**约束**

- 内容过短（≤50 字符）时直接用 content 作为 abstract，不调 LLM
- LLM 失败不影响写入流程（best-effort）
- Heartbeat 批量补充每轮上限 20 条，避免 LLM 调用风暴
- 生成的 abstract 写入 `memories.abstract` 列（已有，无需迁移）

**涉及文件**

- `internal/memory/manager.go` — Create() 增加异步 abstract 生成
- `internal/heartbeat/engine.go` — 新增 abstractBackfill 任务
- `internal/store/interfaces.go` — MemoryStore 新增 `ListMissingAbstract(ctx, limit) ([]*Memory, error)`
- `internal/store/sqlite.go` — 实现 ListMissingAbstract

### B. Scan 索引增强

**ScanResultItem 新增字段**

```go
type ScanResultItem struct {
    ID            string     `json:"id"`
    Title         string     `json:"title"`
    Score         float64    `json:"score"`
    Source        string     `json:"source"`
    Kind          string     `json:"kind,omitempty"`
    Scope         string     `json:"scope,omitempty"`          // 新增
    Tags          []string   `json:"tags,omitempty"`           // 新增
    HappenedAt    *time.Time `json:"happened_at,omitempty"`
    TokenEstimate int        `json:"token_estimate"`
}
```

**Tags 查询策略**

scan 执行后，收集所有结果的 memory ID，一次 SQL JOIN 批量查询 tags，避免 N+1：

```sql
SELECT mt.memory_id, t.name
FROM memory_tags mt JOIN tags t ON mt.tag_id = t.id
WHERE mt.memory_id IN (?, ?, ...)
```

**涉及文件**

- `internal/mcp/tools/scan.go` — ScanResultItem 增加字段 + 批量查 tags 逻辑
- `internal/store/interfaces.go` — TagStore 新增 `GetTagNamesByMemoryIDs(ctx, ids []string) (map[string][]string, error)`
- `internal/store/sqlite_tags.go` — 实现 GetTagNamesByMemoryIDs

### C. 工具描述优化

| 工具 | 当前描述 | 新描述 |
|------|---------|--------|
| `iclude_scan` | "Lightweight memory scan returning compact index..." | "**Primary search tool.** Returns compact index (ID, title, score, tags, scope, token estimate) for efficient browsing. Use this first, then iclude_fetch for full content on selected items. Saves ~10x tokens vs iclude_recall." |
| `iclude_recall` | "Retrieve memories from IClude using semantic + full-text search..." | "Retrieve full memory content. **High token cost** — prefer iclude_scan + iclude_fetch for MCP workflows. Use only when you need all results with full content in one call." |
| `iclude_fetch` | "Fetch full memory content by IDs..." | "Fetch full memory content by IDs. Use after iclude_scan to get details for selected items only. Accepts up to 20 IDs per call." |

**涉及文件**

- `internal/mcp/tools/scan.go` — Definition() 描述更新
- `internal/mcp/tools/recall.go` — Definition() 描述更新
- `internal/mcp/tools/fetch.go` — Definition() 描述更新

## 新增接口

| 包 | 接口/方法 | 签名 |
|----|----------|------|
| `store.MemoryStore` | `ListMissingAbstract` | `(ctx context.Context, limit int) ([]*model.Memory, error)` |
| `store.TagStore` | `GetTagNamesByMemoryIDs` | `(ctx context.Context, ids []string) (map[string][]string, error)` |

## 不改动的部分

- `iclude_recall` 行为不变，保持向后兼容
- Memory 模型不变（abstract 字段已有）
- 数据库 schema 不变（无迁移）
- REST API 不变

## 测试计划

- `testing/memory/manager_test.go` — 验证 Create 后 abstract 异步生成
- `testing/heartbeat/heartbeat_test.go` — 验证 abstract 兜底补漏
- `testing/mcp/fetch_test.go` — 验证 scan 返回 tags/scope 字段
- `testing/store/sqlite_test.go` — 验证 ListMissingAbstract 和 GetTagNamesByMemoryIDs
