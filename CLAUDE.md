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
