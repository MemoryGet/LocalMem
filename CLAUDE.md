# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

LocalMem is a local-first, hybrid storage enterprise memory system for AI applications. It combines SQLite (structured/full-text search) with Qdrant (vector semantic search). Go module name: `iclude`. Requires Go 1.25+.

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
internal/model/                → Data models (Memory 33 fields), DTOs, sentinel errors, retention tiers
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
pkg/tokenizer/                 → Pluggable FTS5 tokenizer (Simple CJK / Jieba HTTP / Gse native / Noop)
pkg/sqlbuilder/                → Lightweight WHERE/SELECT builder (replaces string concat)
pkg/testreport/                → Test report recorder + HTML generator (embed template)
sdks/python/iclude/            → Python SDK client for the LocalMem API
deploy/                        → Docker + docker-compose deployment configs
config/templates/              → 3-tier config templates (basic/standard/premium)
tools/config-generator/        → Web-based config.yaml generator (pure HTML, no deps)
integrations/claude/           → One-click install scripts for Claude Code (bash + PowerShell)
integrations/codex/            → One-click install scripts for Codex CLI (bash + PowerShell)
internal/mcp/
  ├─ server.go     → HTTP+SSE server with session lifecycle
  ├─ session.go    → Per-client session: identity, handshake, tool dispatch, star reminder
  ├─ stdio.go      → Stdio transport (NDJSON, newline-delimited JSON-RPC 2.0)
  ├─ protocol.go   → JSON-RPC types, MCP method constants, helper builders
  ├─ registry.go   → Thread-safe tool/resource/prompt handler registry
  ├─ handler.go    → Handler interfaces (ToolHandler, ResourceHandler, PromptHandler)
  ├─ tools/        → 8 tool implementations (retain, recall, scan, fetch, reflect, timeline, ingest, create_session)
  ├─ resources/    → 2 resources (recent memories, session context)
  └─ prompts/      → 1 prompt (memory_context)
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

**MCP server** (`cmd/mcp/`) is an independent binary. It exposes the same memory system over Model Context Protocol with two transports:
- **stdio** (primary, for Codex/Claude Code): NDJSON over stdin/stdout. Logs go to stderr via `logger.SetStdioMode(true)`. Launch: `iclude-mcp --stdio --config config.yaml`
- **SSE** (HTTP): `GET /sse` opens the event stream, `POST /messages` sends tool calls.

MCP tools: `iclude_retain`, `iclude_recall`, `iclude_scan`, `iclude_fetch`, `iclude_reflect`, `iclude_timeline`, `iclude_ingest_conversation`, `iclude_create_session`. Resources: `Recent Memories`, `Session Context`. Prompts: `memory_context`.

Session lifecycle: initialize handshake required (session marked ready after successful `initialize` response; `notifications/initialized` accepted but not required for compatibility with clients like Codex CLI). Star reminder triggers once after 50 tool calls per session.

Configured via `mcp` section in `config.yaml` (`port`, `api_token`, `cors_allowed_origin`, `default_team_id`, `default_owner_id`).

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

SQLite has 9 tables + 1 FTS5 virtual table. The `memories` table has 33 columns (including memory_class and derived_from for episodic/semantic/procedural evolution tracking). Migrations are versioned (V0→V1→V2→V3→...→V13) in `sqlite_migration*.go`, idempotent and transaction-safe. PRAGMAs: WAL, foreign_keys=ON, busy_timeout=5000, mmap_size=256MB. Connection pool: MaxOpen=5, MaxIdle=2, ConnMaxLifetime=5min. FTS5 writes are always in the same transaction as their parent table write (Create/Update/PurgeDeleted) to guarantee consistency.

### MCP identity flow

MCP tools receive identity from `session.identity` (set from `mcp.default_team_id` / `mcp.default_owner_id` in config). Identity is injected into context via `WithIdentity(ctx, identity)` and extracted by tools via `IdentityFromContext(ctx)`. All retrieval tools must pass both `TeamID` and `OwnerID` to `RetrieveRequest` for correct visibility filtering — omitting `OwnerID` causes private memories to be invisible.

### Config templates

Three-tier config system in `config/templates/`:
- **basic** — SQLite-only, simple tokenizer, no external dependencies
- **standard** — + Jieba tokenizer, knowledge graph, heartbeat, LLM fallback
- **premium** — + Qdrant vector search, MMR, document ingestion, consolidation, contradiction detection

Web generator: `tools/config-generator/index.html` (open in browser, select edition, customize, download).

### Config loading priority

`.env` file → Viper defaults → environment variables → `config.yaml` (searched in `.`, `./config`, `./deploy`).

### Key config sections

`storage` (sqlite/qdrant), `server` (port, auth), `llm` (openai/claude/ollama provider + embedding + fallback chain), `reflect` (max_rounds, token_budget, round_timeout, auto_save), `extract` (max_entities, max_relations, normalize_enabled, timeout), `retrieval` (graph_enabled, graph_depth, fts_weight, qdrant_weight, graph_weight, mmr, preprocess), `scheduler` (enabled, cleanup_interval, access_flush_interval, consolidation_interval), `heartbeat` (enabled, interval, contradiction_enabled, decay_audit_min_age_days), `consolidation` (enabled, min_age_days, similarity_threshold), `document` (enabled, docling/tika URLs, chunking), `mcp` (enabled, port, default_team_id, default_owner_id), `hooks` (enabled, mcp_url, skip_tools), `auth` (enabled, api_keys), `partitions` (enabled, catalog_path).

### Memory model

The `model.Memory` struct supports retention tiers (`permanent` / `long_term` / `standard` / `short_term` / `ephemeral`) with configurable decay rates. Memories have lifecycle fields: `Strength`, `DecayRate`, `DeletedAt` (soft delete), `ExpiresAt`, `ReinforcedCount`, and memory evolution fields: `MemoryClass` (episodic/semantic/procedural), `DerivedFrom` (source tracking for consolidation/reflection outputs).

### API routes

All endpoints under `/v1/`. Core groups:
- `/v1/memories` — CRUD + soft-delete/restore + reinforce + tag associations
- `/v1/retrieve`, `/v1/timeline` — search (three-way retrieval with weighted RRF, strength weighting)
- `/v1/conversations` — conversation ingest (batch) + retrieval by context
- `/v1/contexts` — hierarchical context tree (materialized path) with behavioral fields (mission/directives/disposition)
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

### 代码风格 / Code Style

- **注释**: 单行双语 `// 中文 / English`。导出符号必须 godoc + 双语；未导出可中文
- **错误消息**: 纯英文，`fmt.Errorf("context: %w", err)` 逐层包装，sentinel errors 在 `model/errors.go`
- **导入**: stdlib → internal → third-party，空行隔开
- **日志**: 统一 `zap.Field`，禁止 Sugar()，纯英文，级别 Debug/Info/Warn/Error/Fatal
- **魔法数字**: 业务数字必须 `const` 或配置项，出现 ≥2 次必须抽取

### 模块规范 / Module Convention

- 单文件上限 1000 行，迁移文件上限 500 行（按版本范围拆分）
- 模块间依赖: `api/ → reflect, memory, search, document → store(接口) → model`，禁止反向
- 基础设施（config/logger）全局单例；业务层（handler/manager/store）依赖注入
- 接口和实现在同一包 `internal/store/`，先定义接口再写实现
- 拆分大接口时保留组合接口向后兼容（`MemoryStore = Reader + Writer + ...`）
- 重构后 `wc -l` 验证文件 <1000 行

### 测试规范 / Testing Convention

- **位置**: `testing/` 目录，与源码结构对应
- **风格**: 强制表驱动测试，命名 `Test{函数名}_{场景}`
- **Mock**: `mockery` 自动生成，放 `testing/mocks/`
- **Dashboard**: 新功能须在 `testing/report/` 创建 `{feature}_test.go`，用 `testreport.NewCase()` 包装

### 编写规则 / Coding Rules

- 按业务重要程度防错降级，最多 3 层 fallback
- CJK 字符串必须用 `[]rune` 操作（长度、切片），禁止字节操作截断 UTF-8
- 异步 goroutine 必须 `defer recover()` + context 取消 + WaitGroup

### 质量关卡规则 / Quality Gate Rules

> 从评估实战和重构经验中提炼。分 5 类，每条标注来源（事故=线上问题，经验=重构发现）。

#### 一、安全与授权

1. **安全防护全路径覆盖** — 添加防护时 `grep` 接口所有方法逐个核对。修一处授权必须扫描全部同类 handler。（事故：FileStore.Save 漏 validatePath；graph_handler 修 3 个方法漏 14 个）
2. **Handler 授权依赖完整** — handler 必须持有验证资源归属所需的全部依赖；接口必须提供"获取→校验→操作"三步所需的方法。（事故：TagHandler 无 MemoryReader；GraphStore 缺 GetRelation）
3. **授权修复同步更新测试** — 改 scope 强制逻辑后测试中硬编码的 scope 值会 403。（事故：graph scope 改为 anonymous 后测试全部失败）
4. **白名单禁止通配兜底** — `octet-stream` 等"匹配一切"的项等于放行。（事故：magic bytes 白名单失效）
5. **列表接口 limit 上界** — `if limit > 200 { limit = 200 }`，全局统一。（事故：limit=10000000 触发大查询）
6. **外部 HTTP 响应限制读取大小** — `io.ReadAll` → `io.LimitReader`。（事故：OOM）

#### 二、数据一致性

7. **业务写入必须经 Manager 层** — 绕过 = 丢失 embedding/去重/抽取副作用。（事故：Processor 直调 Store.Create）
8. **先查后写无事务 = TOCTOU** — 去重必须在事务内或依赖 UNIQUE 约束错误。（事故：并发绕过 SELECT 预检查）
9. **删除父记录必须级联子记录** — 无 CASCADE 时应用层显式处理。（事故：DeleteDocument 遗留孤儿 chunk）
10. **元数据必须反映实际执行结果** — 操作完成后从结果提取，禁止预测。（事故：ParserUsed 预测返回 docling 实际用 tika）
11. **数据库迁移必须幂等** — ALTER TABLE 用 `IsColumnExistsError` 守护，可安全重跑。（事故：V10 非幂等重跑崩溃）

#### 三、Store 层规范

12. **scanDest 模式** — 所有 DB 模型（Memory/Entity/Tag/Context/Document）必须用 `scanDest` + `scanFields()` + `toModel()` 三件套，禁止手写逐字段 Scan。新增列只改一处。（经验：R2 统一后消除 15+ 处重复 Scan）
13. **错误分类集中 `store/errors.go`** — `IsColumnExistsError`/`IsUniqueConstraintError` 统一导出，禁止内联 `strings.Contains`。（经验：R3 集中后消除 4 文件不一致）
14. **迁移文件按版本拆分** — 单文件 <500 行，主文件只保留调度 + helper。（经验：R1 拆分 972→4 文件）
15. **同一计算逻辑只允许一套实现** — 相同语义的函数必须提取到 `pkg/`。（事故：两个 EstimateTokens 算法不一致）

#### 四、API 层规范

16. **handler 用 `withIdentity` 包装** — 签名 `(c *gin.Context, identity *model.Identity)`，路由注册 `withIdentity(h.Method)`。禁止内联 requireIdentity。（经验：R4 消除 47 处样板代码）

#### 五、业务层规范

17. **Manager 方法单一职责** — 公开方法只做调度，去重/嵌入/抽取委托给子方法。超 50 行时拆分。（经验：R5 抽取 dedupCheck 后 Create 减 30 行）
18. **修复代码同等评审** — 自检清单：全路径覆盖？白名单无兜底？函数无重复？写操作经 Manager？CJK 安全？（事故：修复引入 5 个新问题）
