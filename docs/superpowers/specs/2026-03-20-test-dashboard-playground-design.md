# Test Dashboard 数据集驱动测试 + 交互式 Playground 设计文档

## 概述

扩展现有 test-dashboard，新增两大功能：
1. **数据集驱动批量测试** — 从 JSON fixture 文件加载预置数据（memories/entities/relations），执行预定义的 test_cases
2. **交互式 Playground** — 加载数据集后，用户手动输入任意 query，实时查看完整检索链路结果（预处理 → 三通道 → RRF 融合）

## 动机

当前测试用例的参数硬编码在 Go 测试代码中，无法在运行时调整。开发者需要一种方式来：
- 用不同的数据集测试检索效果
- 交互式调试 query preprocessor 的意图分类和权重调整
- 对比不同 query 在三通道上的表现

## 整体架构

```
┌───────────────────────────────────────────────────────┐
│  test-dashboard-ui (Vue)                              │
│  ┌────────────┐  ┌──────────────┐  ┌───────────────┐ │
│  │ 批量测试    │  │  Playground  │  │  现有 go test │ │
│  │ Case列表    │  │  查询输入框   │  │  flow graph   │ │
│  └──────┬─────┘  └──────┬───────┘  └───────┬───────┘ │
│         │ REST           │ REST             │ WS      │
└─────────┼────────────────┼──────────────────┼─────────┘
          │                │                  │
┌─────────┴────────────────┴──────────────────┴─────────┐
│  test-dashboard 后端 (Go)                              │
│  ┌──────────────┐ ┌────────────┐ ┌──────────────────┐ │
│  │/api/datasets  │ │/api/query  │ │ /ws (现有)       │ │
│  └───────┬──────┘ └─────┬──────┘ └──────────────────┘ │
│          │              │                              │
│  ┌───────┴──────────────┴──────────────────┐           │
│  │  TestEnv (临时 SQLite + 业务层链路)      │           │
│  │  Tokenizer → Preprocessor → Retriever   │           │
│  │  MemoryStore → GraphStore               │           │
│  └─────────────────────────────────────────┘           │
└────────────────────────────────────────────────────────┘
```

## 数据集格式

存放位置：`testing/fixtures/*.json`

```json
{
  "name": "技术知识库",
  "description": "Go/K8s/Docker 相关的技术记忆，含实体和关系",
  "memories": [
    {
      "content": "Go语言的并发模型基于 goroutine 和 channel",
      "scope": "tech",
      "kind": "fact",
      "team_id": "t1",
      "retention_tier": "permanent",
      "strength": 0.9,
      "abstract": "Go并发模型",
      "summary": "goroutine + channel"
    }
  ],
  "entities": [
    {
      "name": "Go",
      "entity_type": "tool",
      "scope": "tech",
      "description": "Go编程语言"
    }
  ],
  "relations": [
    {
      "source": "Go",
      "target": "Kubernetes",
      "relation_type": "used_by"
    }
  ],
  "test_cases": [
    {
      "name": "精确查找",
      "query": "Go goroutine",
      "expected_intent": "keyword",
      "description": "短查询应识别为 keyword 意图"
    }
  ]
}
```

字段说明：
- `memories` — 预置记忆数据，字段对应 `model.Memory` 的子集。`strength` 在 JSON 中为 float，Load 时转为 `*float64`
- `entities` — 预置实体，`name` 全局唯一（同一数据集内）
- `relations` — 实体关系，`source`/`target` 引用 entities 的 `name`（非 ID）。Load 时通过 `nameToID map[string]string` 解析为实际 entity ID
- `test_cases` — 批量测试用的预定义 query，`expected_intent` 可选（用于 pass/fail 判定）

## 后端设计

### TestEnv（`cmd/test-dashboard/testenv.go`）

封装临时测试环境的生命周期：

```go
type TestEnv struct {
    mu           sync.Mutex            // 并发保护 / Concurrency protection
    dir          string                // 临时目录
    stores       *store.Stores
    preprocessor *search.Preprocessor
    retriever    *search.Retriever
    graphManager *memory.GraphManager
    datasetName  string
    stats        DatasetStats
    testCases    []TestCaseDef         // 当前数据集的 test_cases
}

type DatasetStats struct {
    Memories  int `json:"memories"`
    Entities  int `json:"entities"`
    Relations int `json:"relations"`
    Cases     int `json:"cases"`
}
```

方法：
- `NewTestEnv()` — 创建空实例
- `Load(fixtureDir, datasetFile string) error` — 创建临时 SQLite，解析 JSON，批量写入数据，构建业务层链路
- `Query(query string, limit int) (*QueryResult, error)` — 执行预处理 + 检索，返回全链路结果
- `RunCase(tc TestCaseDef) *CaseResult` — 执行单个 test_case，对比 expected_intent
- `Close()` — 销毁临时目录和数据库连接
- `IsLoaded() bool` — 检查是否已加载数据集
- 所有公开方法加 `mu.Lock()`，HTTP handler 天然并发，需保护

### Load 详细流程

```
1. Close() 销毁旧环境（如有）
2. os.MkdirTemp 创建临时目录
3. 构造 config.Config{Storage: {SQLite: {Enabled: true, Path: tempDir/test.db,
       Search: {BM25Weights: {10, 5, 3}}, Tokenizer: {Provider: "simple"}}}}
4. store.InitStores(ctx, cfg, nil) → stores (Qdrant disabled, embedder nil)
5. 遍历 fixture.Memories:
   - strength float → *float64 指针转换
   - 通过 stores.MemoryStore.Create() 直接写入（绕过 Manager，不触发 extractor）
6. 遍历 fixture.Entities:
   - 生成 UUID，stores.GraphStore.CreateEntity()
   - 维护 nameToID map[string]string
7. 遍历 fixture.Relations:
   - source/target 通过 nameToID 解析为实际 entity ID
   - stores.GraphStore.CreateRelation()
8. 为每条 memory 关联相关实体（基于 content 包含 entity name 的简单匹配）:
   - stores.GraphStore.CreateMemoryEntity()
9. 构建业务层:
   - preprocessor = search.NewPreprocessor(stores.Tokenizer, stores.GraphStore, nil, retrievalCfg)
   - retriever = search.NewRetriever(stores.MemoryStore, nil, nil, stores.GraphStore, nil, retrievalCfg, preprocessor)
   - graphManager = memory.NewGraphManager(stores.GraphStore)
```

注意：不使用 `memory.Manager`（它会触发 extractor 等可选依赖）。直接通过 `MemoryStore.Create()` 写入，简洁可靠。

Qdrant 和 LLM 不可用，三通道中只有 FTS5 + Graph 可工作。Playground 结果中 Qdrant 通道显示为 "unavailable"。

### 错误处理

所有 REST handler 需要：
- 检查 `r.Method`，不匹配返回 `405 Method Not Allowed`
- `/api/query` 和 `/api/cases/run`：如果 `!env.IsLoaded()`，返回 `{"error": "no dataset loaded", "code": 400}`
- 查询失败（retriever 返回 error）：返回 `{"error": "...", "code": 500}`，不 panic

统一错误响应格式：
```json
{"error": "error message", "code": 400}
```

### REST API（`cmd/test-dashboard/api.go`）

| 端点 | 方法 | 功能 |
|------|------|------|
| `/api/datasets` | GET | 扫描 `testing/fixtures/*.json`，返回列表 `[{name, description, stats}]` |
| `/api/datasets/load` | POST | `{"name": "tech_knowledge"}` 加载指定数据集 |
| `/api/datasets/status` | GET | 当前已加载数据集信息，未加载返回 `null` |
| `/api/query` | POST | `{"query": "...", "limit": 10}` 执行查询 |
| `/api/cases/run` | POST | `{"name": "tech_knowledge"}` 批量执行数据集的 test_cases |

#### `/api/query` 响应格式

```json
{
  "preprocess": {
    "original_query": "Go goroutine channel",
    "semantic_query": "Go goroutine channel",
    "keywords": ["Go", "goroutine", "channel"],
    "entities": ["entity-id-1"],
    "intent": "keyword",
    "weights": { "fts": 1.5, "qdrant": 0.6, "graph": 0.96 }
  },
  "channels": {
    "fts": {
      "available": true,
      "count": 3,
      "results": [{"memory_id": "...", "content": "...", "score": 0.5, "source": "sqlite"}]
    },
    "qdrant": {
      "available": false,
      "count": 0,
      "results": []
    },
    "graph": {
      "available": true,
      "count": 1,
      "results": [{"memory_id": "...", "content": "...", "score": 0, "source": "graph"}]
    }
  },
  "merged": [
    {"memory_id": "...", "content": "...", "score": 0.032, "source": "hybrid"}
  ],
  "duration_ms": 12
}
```

#### `/api/cases/run` 响应格式

```json
{
  "dataset": "tech_knowledge",
  "results": [
    {
      "name": "精确查找",
      "query": "Go goroutine",
      "expected_intent": "keyword",
      "actual_intent": "keyword",
      "passed": true,
      "result_count": 3,
      "duration_ms": 5,
      "preprocess": { ... },
      "top_results": [{ ... }]
    }
  ],
  "summary": { "total": 5, "passed": 4, "failed": 1 },
  "duration_ms": 28
}
```

### main.go 路由注册

```go
// 现有 WebSocket 路由不变
http.HandleFunc("/ws", ...)

// 新增 REST API
fixtureDir := filepath.Join(findProjectRoot(), "testing", "fixtures")
env := NewTestEnv(fixtureDir)
http.HandleFunc("/api/datasets", env.HandleListDatasets)       // GET
http.HandleFunc("/api/datasets/load", env.HandleLoadDataset)   // POST
http.HandleFunc("/api/datasets/status", env.HandleDatasetStatus) // GET
http.HandleFunc("/api/query", env.HandleQuery)                 // POST
http.HandleFunc("/api/cases/run", env.HandleRunCases)          // POST
```

`fixtureDir` 通过 `findProjectRoot()` + `testing/fixtures` 拼接，复用已有的项目根目录查找逻辑。

## 前端设计

### 导航结构

TopBar 新增 tab 切换：

```
[ Go Tests ]  [ Playground ]
```

- **Go Tests** — 现有功能，完全不变
- **Playground** — 新视图，包含数据集管理 + 批量测试 + 交互查询

### Playground 视图布局

```
┌─────────────────────────────────────────────────────┐
│  DatasetSelector (顶部栏)                            │
│  [下拉选择数据集 ▼] [Load] [状态: 已加载 50条记忆]   │
├────────────────────┬────────────────────────────────┤
│  BatchCases (左侧)  │  QueryResult (右侧)            │
│  ┌──────────────┐  │  ┌────────────────────────────┐│
│  │ [Run All]    │  │  │ 查询输入框  [执行]          ││
│  │ ─────────    │  │  ├────────────────────────────┤│
│  │ ✓ 精确查找   │  │  │ Preprocess 结果            ││
│  │ ✓ 语义探索   │  │  │  intent: keyword           ││
│  │ ✗ 时间过滤   │  │  │  keywords: [Go, goroutine] ││
│  │ ✓ 关联查询   │  │  │  weights: FTS=1.5 ...      ││
│  │              │  │  ├────────────────────────────┤│
│  │ 点击展开详情  │  │  │ 通道结果                   ││
│  │              │  │  │  FTS(3) | Qdrant(N/A) |    ││
│  │              │  │  │  Graph(1)                  ││
│  │              │  │  ├────────────────────────────┤│
│  │              │  │  │ 融合结果 (RRF)             ││
│  │              │  │  │  1. Go并发模型... 0.032    ││
│  │              │  │  │  2. K8s部署...   0.016    ││
│  └──────────────┘  │  └────────────────────────────┘│
└────────────────────┴────────────────────────────────┘
```

### 新增前端文件

| 文件 | 职责 |
|------|------|
| `src/stores/playgroundStore.ts` | Playground 状态：数据集列表、当前加载状态、查询结果、批量结果 |
| `src/components/PlaygroundView.vue` | Playground 主视图容器（DatasetSelector + BatchCases + QueryResult） |
| `src/components/DatasetSelector.vue` | 数据集下拉选择 + Load 按钮 + 状态显示 |
| `src/components/BatchCases.vue` | 左侧批量 case 列表 + Run All 按钮 + 单个 case 结果展示 |
| `src/components/QueryResult.vue` | 右侧交互查询：输入框 + preprocess/channels/merged 三区展示 |

### Vite proxy 新增

```typescript
proxy: {
  '/ws': { target: 'ws://localhost:3001', ws: true },
  '/api': { target: 'http://localhost:3001', changeOrigin: true },  // 新增
}
```

## 数据集文件

提供两套初始数据集：

### `testing/fixtures/tech_knowledge.json`
- 约 15-20 条技术记忆（Go、K8s、Docker、微服务、数据库等）
- 约 8-10 个实体
- 约 6-8 条关系
- 约 8 个 test_cases（覆盖 5 种 intent）

### `testing/fixtures/meeting_notes.json`
- 约 10-15 条会议记忆（项目进度、决策、TODO）
- 约 5-6 个实体（人名、项目名）
- 约 4-5 条关系
- 约 5 个 test_cases

## 文件变更汇总

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `testing/fixtures/tech_knowledge.json` | 新建 | 技术知识库数据集 |
| `testing/fixtures/meeting_notes.json` | 新建 | 会议记录数据集 |
| `cmd/test-dashboard/testenv.go` | 新建 | TestEnv 临时环境管理 |
| `cmd/test-dashboard/api.go` | 新建 | REST API handlers |
| `cmd/test-dashboard/main.go` | 修改 | 注册新路由 |
| `tools/test-dashboard-ui/vite.config.ts` | 修改 | 新增 `/api` proxy |
| `tools/test-dashboard-ui/src/App.vue` | 修改 | 添加 tab 切换 |
| `tools/test-dashboard-ui/src/stores/playgroundStore.ts` | 新建 | Playground 状态 |
| `tools/test-dashboard-ui/src/components/PlaygroundView.vue` | 新建 | 主视图容器 |
| `tools/test-dashboard-ui/src/components/DatasetSelector.vue` | 新建 | 数据集选择器 |
| `tools/test-dashboard-ui/src/components/QueryResult.vue` | 新建 | 查询结果展示 |
| `tools/test-dashboard-ui/src/components/BatchCases.vue` | 新建 | 批量 case 列表 |

## 向后兼容

- 现有 Go Tests tab 功能完全不变
- 现有 WebSocket 协议不变
- test-dashboard 不加载数据集时，Playground 显示 "请先加载数据集" 提示
- 无 Qdrant 环境下，Qdrant 通道显示 "unavailable"，不影响 FTS + Graph 通道
