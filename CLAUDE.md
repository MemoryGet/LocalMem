# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

IClude is a local-first, hybrid storage enterprise memory system for AI applications. It combines SQLite (structured/full-text search) with Qdrant (vector semantic search). Go module name: `iclude`. Requires Go 1.25+.

**Note:** The README.md lists PostgreSQL, Milvus, Elasticsearch, and Redis as requirements — this is aspirational/outdated. The actual implementation uses SQLite + Qdrant only.

## Build & Run Commands

```bash
go mod download              # install dependencies
go run ./cmd/server/         # run the API service (port 8080)
go run ./cmd/mcp/            # run the MCP server (port 8081, SSE transport)
./server                     # run pre-built API binary (if available)
./mcp                        # run pre-built MCP binary (if available)
go fmt ./...                 # format code
go vet ./...                 # static analysis
go test ./testing/...        # run all tests
go test ./testing/store/...  # run tests for one layer
go test -run TestLoadConfig_FileNotFound ./testing/...  # run a single test
go test ./testing/report/ -v -count=1   # generate HTML test report → testing/report/report.html

# 测试监控面板 (Test Dashboard UI)
./test-dashboard                                            # 启动后端 → http://localhost:3001
cd tools/test-dashboard-ui && npm run dev                   # 启动前端 → http://localhost:5173
# 前端依赖安装（首次）: cd tools/test-dashboard-ui && npm install

# Jieba 分词服务（中文 FTS5，仅 provider=jieba 时需要）
python tools/jieba_server.py                                # 启动 → http://localhost:8866

# Docker 部署
docker-compose -f deploy/docker-compose.yml up              # 容器化运行
```

## Architecture

Layered Go backend using `cmd/` + `internal/` + `pkg/` Go-idiomatic layout.

```
cmd/server/main.go            → Entry point (config → logger → embed → stores → managers → api)
internal/config/               → Viper + godotenv config (global singleton)
internal/logger/               → Zap structured logging (package-level functions)
internal/model/                → Data models (Memory 31 fields), DTOs, sentinel errors, retention tiers
internal/store/                → Storage interfaces (8) + all SQLite/Qdrant implementations + factory (flat)
internal/embed/                → Embedding adapters (OpenAI, Ollama)
internal/memory/               → Manager (CRUD + dual-write), ContextManager, GraphManager, lifecycle
internal/search/               → Retriever (3-mode: SQLite/Qdrant/Hybrid) + RRF fusion (k=60)
internal/llm/                  → LLM Chat abstraction (OpenAI-compatible provider, covers DeepSeek/Ollama/etc.)
internal/reflect/              → Reflect Engine (multi-round LLM reasoning over memories, 3-level fallback parsing)
internal/document/             → Document processor (upload → chunk → embed → store)
internal/heartbeat/            → Autonomous inspection engine: decay audit, orphan cleanup, contradiction detection
internal/scheduler/            → In-process goroutine+ticker scheduler with overlap prevention and graceful shutdown
internal/api/                  → Gin HTTP handlers, router, middleware, response helpers
pkg/qdrant/client.go           → Reusable Qdrant HTTP client (stdlib only)
pkg/tokenizer/                 → Pluggable FTS5 tokenizer (Simple CJK / Jieba HTTP / Noop)
pkg/sqlbuilder/                → Lightweight WHERE/SELECT builder (replaces string concat)
pkg/testreport/                → Test report recorder + HTML generator (embed template)
sdks/python/iclude/            → Python SDK client for the IClude API
deploy/                        → Docker + docker-compose deployment configs
internal/document/
  ├─ processor.go    → Document lifecycle (upload, async process, delete with cleanup)
  ├─ factory.go      → InitDocumentPipeline() factory (wires FileStore + Parsers + Chunker)
  ├─ parser.go       → Parser interface + ParseRouter (Docling → Tika fallback chain)
  ├─ docling.go      → Docling HTTP client (docling-serve REST API)
  ├─ tika.go         → Tika HTTP client (Apache Tika Server REST API)
  ├─ chunker.go      → MarkdownChunker (3-layer) + TextChunker (recursive + overlap)
  └─ file_store.go   → FileStore interface + LocalFileStore (future: SMB/NFS)
```

**Dependency flow** (acyclic): `cmd/server → api → reflect, memory, search, document → store(interfaces) → model, pkg/*`

**MCP server** (`cmd/mcp/`) is an independent binary. It exposes the same memory system over Model Context Protocol (SSE transport): `GET /sse` opens the event stream, `POST /messages` sends tool calls. Tools: `recall_memories`, `save_memory`, `reflect`, `ingest_conversation`, `timeline`. Prompts: `memory_context`. Configured via `mcp` section in `config.yaml` (`port`, `api_token`, `cors_allowed_origin`).

**LLM dependency**: `llm.Provider` is consumed by reflect, memory (Extractor), and search (graph fallback). It is initialized early in `main.go` and injected into all consumers.

> **Note:** `internal/reflect/` is a separate package (not inside `memory/`) because `search` already imports `memory` (`ApplyStrengthWeighting`). Placing reflect in `memory` would create a circular dependency since reflect needs `search.Retriever`.

### Startup wiring order (main.go)

Config → Logger → Embedder (if Qdrant) → Stores → LLM Provider → GraphManager → Extractor → Manager → Retriever → ContextManager → DocProcessor → ReflectEngine → Router. Order matters: Extractor needs LLM + GraphManager; ReflectEngine needs Retriever + Manager + LLM.

> **Note:** `internal/heartbeat/` and `internal/scheduler/` are config-gated (`heartbeat.enabled`, `scheduler.enabled`, both default `false`) and are wired into `main.go`. The scheduler starts unconditionally; heartbeat registers itself only when `heartbeat.enabled: true`.

### Feature gating via nil checks

The `store.Stores` struct aggregates all backends. Optional stores (`VectorStore`, `ContextStore`, `TagStore`, `GraphStore`, `DocumentStore`) may be nil. The router conditionally registers endpoint groups based on nil checks. Business managers (GraphManager, ContextManager, DocProcessor, ReflectEngine, Extractor) are only constructed when their backing store + dependencies are non-nil.

### Storage design

Three modes via `config.yaml` `storage` section:
1. **SQLite only** (`sqlite.enabled: true`) — structured queries + FTS5 full-text search (3-column: content/abstract/summary, BM25 weighted). Enables all sub-stores (context, tag, graph, document) sharing the same `*sql.DB`.
2. **Qdrant only** (`qdrant.enabled: true`) — vector semantic search
3. **Hybrid** (both enabled) — results merged via Reciprocal Rank Fusion (RRF)

Best-effort dual-write: SQLite is primary, Qdrant failure is logged but does not roll back SQLite.

`NewSQLiteMemoryStore(dbPath, bm25Weights, tokenizer)` accepts a pluggable `tokenizer.Tokenizer` for FTS5 pre-tokenization. The factory (`store.InitStores`) creates the tokenizer based on `config.yaml` `storage.sqlite.tokenizer.provider` (jieba / simple / noop).

### Three-way retrieval

The `search.Retriever` merges up to 3 channels via weighted RRF (`MergeWeightedRRF`, k=60):
1. **SQLite FTS5** (BM25) — weight configurable via `retrieval.fts_weight`
2. **Qdrant vector** — weight via `retrieval.qdrant_weight`
3. **Graph association** — weight via `retrieval.graph_weight`; uses LLM fallback when FTS5 finds no matching entities

Formula: `score = Σ weight × 1/(k + rank + 1)` per channel. Results are then strength-weighted and token-budget-trimmed.

### Entity extraction (Extractor)

`memory.Extractor` auto-extracts entities/relations from memory content via LLM. Integrated best-effort into `Manager.Create()` (non-blocking). Also exposed as explicit `POST /v1/memories/:id/extract`. Uses 3-level fallback parsing: JSON unmarshal → regex extract → LLM retry → raw fallback.

### Reflect Engine

Multi-round LLM reasoning over retrieved memories. Configured via `reflect` config section (`max_rounds`, `token_budget`, `round_timeout`, `auto_save`). Parsing uses same 3-level fallback as Extractor. Conclusions can auto-save as `mental_model` kind memories. Query deduplication prevents infinite loops.

### Database schema

SQLite has 9 tables + 1 FTS5 virtual table. The `memories` table has 31 columns. Migrations are versioned (V0→V1→V2→V3) in `sqlite_migration.go`, idempotent and transaction-safe. PRAGMAs: WAL, foreign_keys=ON, busy_timeout=5000, mmap_size=256MB. Connection pool: MaxOpen=5, MaxIdle=2, ConnMaxLifetime=5min. FTS5 writes are always in the same transaction as their parent table write (Create/Update/PurgeDeleted) to guarantee consistency.

### Config loading priority

`.env` file → Viper defaults → environment variables → `config.yaml` (searched in `.`, `./config`, `./deploy`).

### Key config sections

`storage` (sqlite/qdrant), `server` (port, auth), `llm` (openai/ollama provider + embedding), `reflect` (max_rounds, token_budget, round_timeout, auto_save), `extract` (max_entities, max_relations, normalize_enabled, timeout), `retrieval` (graph_enabled, graph_depth, fts_weight, qdrant_weight, graph_weight), `scheduler` (enabled, cleanup_interval, access_flush_interval, consolidation_interval), `heartbeat` (enabled, interval, contradiction_enabled, decay_audit_min_age_days).

### Memory model

The `model.Memory` struct supports retention tiers (`permanent` / `long_term` / `standard` / `short_term` / `ephemeral`) with configurable decay rates. Memories have lifecycle fields: `Strength`, `DecayRate`, `DeletedAt` (soft delete), `ExpiresAt`, and `ReinforcedCount`.

### API routes

All endpoints under `/v1/`. Core groups:
- `/v1/memories` — CRUD + soft-delete/restore + reinforce + tag associations
- `/v1/retrieve`, `/v1/timeline` — search (three-way retrieval with weighted RRF, strength weighting)
- `/v1/conversations` — conversation ingest (batch) + retrieval by context
- `/v1/contexts` — hierarchical context tree (materialized path)
- `/v1/tags` — tag CRUD + memory-tag associations
- `/v1/entities`, `/v1/entity-relations`, `/v1/memory-entities` — knowledge graph
- `/v1/documents` — document upload/process/list
- `/v1/reflect` — multi-round LLM reasoning over memories
- `/v1/memories/:id/extract` — explicit entity extraction from a memory
- `/v1/maintenance/cleanup` — expire/purge operations

### Document Ingestion Pipeline

File upload → async processing → Memory ingestion. Three-layer fallback: Docling → Tika → manual /reprocess.

**Chunking pipeline**: Structure-aware split (headings/tables/code blocks) → recursive character split (512 token, 50 overlap) → context prefix enrichment. Markdown input uses MarkdownChunker, plaintext falls back to TextChunker.

**Processing stages**: `pending → parsing → chunking → embedding → ready` (or `→ failed`). Async via goroutine + semaphore (default 3 concurrent). Config-gated: `document.enabled: true` required + docling/tika Docker sidecars.

## AI 日报 Skill

详见 `.claude/skills/daily-ai-report/SKILL.md`

| 命令 | 说明 |
|------|------|
| `/daily-report` | 完整流程：采集→验证→更新数据→生成PPT→推送飞书 |
| `/gen-report` | 快速生成 PPT（不采集） |
| `/feishu-push` | 快速推送飞书（不采集） |

```bash
python .claude/skills/daily-ai-report/scripts/generate_report.py   # 生成 PPT
python .claude/skills/daily-ai-report/scripts/send_to_feishu.py    # 发送飞书
```

## Development Rules

- Test files go in `testing/` directory, not alongside source. Name: `{module}_test.go`.
- Test subdirectories mirror the source: `testing/{api,llm,memory,reflect,report,search,store}/`.
- The project uses a multi-agent development pattern with scoped agents per layer (see `.opencode/agents/`).

### 注释规范 / Comment Convention

- **格式**: 单行双语，斜杠分隔 — `// 中文说明 / English description`
- **导出函数/类型（大写开头）**: 必须写 godoc 注释，必须双语
- **未导出函数、行内注释、TODO/FIXME**: 中文即可，不强制双语

### 错误处理 / Error Handling

- **错误消息**: 纯英文
- **包装方式**: `fmt.Errorf("context: %w", err)` 逐层包装
- **自定义错误类型**: 包级别 sentinel errors in `internal/model/errors.go`

### 接口规范 / Interface Convention

- **扁平化**: 接口和实现在同一包 `internal/store/` 中
- 存储层必须先定义接口，再写 SQLite/Qdrant 实现
- 当前接口 (8): `MemoryStore` (18 methods), `VectorStore` (6), `Embedder` (2), `ContextStore` (10), `TagStore` (8), `GraphStore` (12), `DocumentStore` (8)

### 导入顺序 / Import Order

三段分组，空行隔开：stdlib → 项目内部 → 第三方

### 依赖管理 / Dependency Management

- **基础设施（config/logger）**: 全局单例，直接调用
- **业务层（handler/manager/store）**: 依赖注入，通过构造函数传入

### 日志规范 / Logging Convention

- 统一 `zap.Field`，禁止 `zap.Sugar()` 模板字符串
- 日志语言纯英文
- 级别: `Debug`(开发) / `Info`(正常流程) / `Warn`(可恢复异常) / `Error`(不中断服务) / `Fatal`(仅启动阶段)

### 模块规范 / Module Convention

- 按业务模块拆分，每个包主文件写 `// Package xxx ...` godoc（双语）
- 单文件上限 1000 行，文件命名小写下划线
- 模块间依赖: `api/ → reflect, memory, search, document → store(接口) → model`，禁止反向依赖
- `reflect` 包独立于 `memory`，避免 memory↔search 循环依赖

### 测试规范 / Testing Convention

- **位置**: `testing/` 目录，按层级组织，与源码结构对应
- **风格**: 强制表驱动测试（table-driven）
- **Mock**: 使用 `mockery` 自动生成，放 `testing/mocks/`
- **命名**: `Test{函数名}_{场景}`，如 `TestLoadConfig_FileNotFound`
- **Dashboard 可视化**: 每个新功能必须在 `testing/report/` 下创建对应的 `{feature}_test.go`，使用 `testreport.NewCase()` 包装关键测试场景（Input/Step/Output/Done 模式），否则 test-dashboard UI 面板上不会显示用例详情。参考 `testing/report/search_test.go`。

### 编写规则 / Coding Rules

- 按业务重要程度进行防错降级处理 / Implement error prevention and graceful degradation based on business priority
- 保证高可用 / Ensure high availability
- 最多3层fallback处理 / Maximum 3 levels of fallback handling

### 质量关卡规则 / Quality Gate Rules

> 从 5 轮评估实战中提炼，每条对应一次实际评分下降事故。

#### 安全防护必须全路径覆盖 / Security checks must cover all paths

对同一接口的防护（授权、校验、限流）必须逐方法确认覆盖。添加防护时 `grep` 接口所有方法，逐个核对。
> 事故：`FileStore.Save` 缺 `validatePath`，而 `Get`/`Delete` 已有，路径遍历防护形同虚设。

#### 白名单禁止通配兜底 / No catch-all entries in allowlists

白名单中禁止包含匹配"其他所有情况"的兜底项。`application/octet-stream` 这类"未知二进制"等于放行一切。
> 事故：magic bytes 白名单含 `octet-stream`，使整个文件验证失效。

#### 同一计算逻辑只允许一套实现 / Single source of truth for computation

相同语义的计算（token 估算、相似度、哈希等）必须提取到 `pkg/` 下唯一位置，禁止两个包各写一份。
> 事故：`chunker.estimateTokens` 和 `retriever.EstimateTokens` 算法不一致，分块与检索裁剪标准不匹配。

#### 业务写入必须经过 Manager 层 / Writes must go through Manager

禁止在 Manager 之外直接调用 Store 写方法。Manager 层承载 embedding、去重、实体抽取等副作用，绕过 = 功能缺失。
> 事故：`Processor` 直接调 `memStore.Create()` 跳过了 embedding + Qdrant 写入，文档在语义检索中不可见。

#### 元数据必须反映实际执行结果 / Metadata must reflect actual execution

记录"谁做的"类元数据时，必须在操作完成后从结果中提取，禁止操作前预测。
> 事故：`ParserUsed()` 预测返回 "docling"，实际 fallback 到 Tika，元数据与事实不符。

#### 删除父记录必须级联子记录 / Parent deletion must cascade to children

有关联关系的删除必须先处理子记录。数据库层无 `ON DELETE CASCADE` 时应用层必须显式实现。
> 事故：`DeleteDocument` 只删 document，chunk memories 成孤儿。

#### CJK 字符串必须用 []rune 操作 / Use []rune for CJK string operations

长度用 `len([]rune(s))`，切片用 `string([]rune(s)[start:end])`。`len(s)` 是字节数，`s[:n]` 可能截断 UTF-8。
> 事故：`recursiveSplit` 用字节长度，中文 chunk 是预期的 3 倍大，硬切产生乱码。

#### 数据库迁移必须幂等 / Migrations must be idempotent

`ALTER TABLE ADD COLUMN` 必须有列存在性检查（`isColumnExistsError`），确保可安全重跑。
> 事故：V10 是全库唯一非幂等迁移，重跑必崩。

#### 先查后写无事务 = TOCTOU 竞态 / Read-then-write without transaction = TOCTOU

去重检查 + 写入必须在同一事务内，或直接依赖 UNIQUE 约束错误。应用层预检查 SELECT 在并发下无效，删掉它。
> 事故：`Create()` 先 `SELECT COUNT(*)` 再 `INSERT`，并发写入绕过预检查。

#### 修复代码必须接受同等评审 / Fix code gets the same review standard

修复不享受豁免。自检清单：新增防护是否全路径覆盖？白名单有无兜底项？函数是否与已有重复？写操作是否经 Manager？字符串操作是否 CJK 安全？
> 事故：R2 修复后 R3 评分反降 0.25，修复本身引入了 5 个新问题。

#### 外部 HTTP 响应体必须限制读取大小 / Limit external HTTP response body size

所有 `io.ReadAll(resp.Body)` 对外部服务必须改为 `io.LimitReader(resp.Body, maxSize)`。
> 事故：Docling/Tika 客户端无限读取，故障服务可触发 OOM。

#### 异步 goroutine 必须有 recovery 和生命周期管理 / Async goroutines need recovery + lifecycle

`go func()` 必须 `defer recover()` + 错误日志。长时间 goroutine 接入 context 取消或 WaitGroup。信号量 token 必须在 defer 中释放（含 panic 路径）。
> 事故：`ProcessAsync` 裸 goroutine，panic 崩溃整个进程。

#### 列表接口必须设 limit 上界 / List APIs must cap limit

每个接受 `limit` 参数的列表接口：`if limit > 200 { limit = 200 }`，全局统一。
> 事故：document/graph/conversation 的 limit 无上界，可传 `?limit=10000000` 触发大查询。

#### 授权修复必须扫描全部同类 handler / Auth fixes must scan ALL handlers of the same pattern

修一个 handler 的授权后，必须 `grep '_ = identity'` 和 `grep 'requireIdentity'` 扫描 **所有** handler 文件，逐个核对。同一模式的漏洞一定在多处出现。
> 事故：graph_handler 修了 3 个方法的 scope 检查，遗漏了同文件的 4 个方法 + tag_handler 的 6 个方法 + context_handler 的 4 个方法。安全评分从 8.5 跌到 7.2。

#### Handler 必须持有足够的依赖来执行授权 / Handlers must hold enough dependencies for authorization

如果 handler 需要验证资源 A 的归属，它必须持有能读取资源 A 的依赖。设计 handler struct 时就要考虑授权需要访问哪些 store/manager。
> 事故：`TagHandler` 只有 `TagStore`，无法验证 memory 归属；需要追加 `MemoryReader` 才能在 `TagMemory` 中检查 memory owner。

#### 接口必须支撑授权链路的完整性 / Interfaces must support complete authorization chains

如果 handler 需要"获取资源 → 检查归属 → 操作"三步，接口必须提供"获取"方法。缺少方法 = 授权无法实现 = 留下越权漏洞。
> 事故：`GraphStore` 缺 `GetRelation` 方法，`DeleteRelation` 无法获取关系详情做归属验证，留了 TODO 直到补全接口。

#### 授权修复后必须同步更新测试中的 scope/identity / Auth fixes must update test fixtures

授权强制 `scope = identity.OwnerID` 后，测试中硬编码的 scope 值会与测试 identity 不匹配导致 403。修授权的 PR 必须同时修测试。
> 事故：graph scope 强制为 "anonymous"（测试 identity），但测试实体用 scope="tech"，全部 403。

#### 扫描函数（scan*）有重复时必须抽取共享结构体 / Extract shared scan struct when scan functions duplicate

数据库层的 `Scan` 调用涉及 30+ 字段的逐一赋值，极易在多处出现重复。必须用 `scanDest` 结构体 + `scanFields()` + `toModel()` 模式统一，新增列只改一处。
> 事故：`scanMemory`、`scanMemoryFromRows`、`scanMemoryWithRank` 三个函数各自重复 60 行扫描逻辑，新增字段需改三处。

#### 接口拆分后组合接口必须向后兼容 / Interface splits must maintain backward compatibility via composition

拆分大接口时，必须保留原接口名作为组合接口（`MemoryStore = MemoryReader + MemoryWriter + ...`），所有现有消费者无需改动。
> 经验：MemoryStore 28 方法拆为 4 个子接口，组合接口确保零 breaking change，13 个测试包无需修改即通过。

#### 重构后验证文件行数是否符合项目规范 / Verify file sizes after refactoring

拆文件后用 `wc -l` 确认每个文件 < 1000 行（项目规范上限）。拆分不彻底等于没拆。
> 经验：sqlite.go 1470 行拆为 4 个文件后，最大文件 513 行，全部符合规范。
