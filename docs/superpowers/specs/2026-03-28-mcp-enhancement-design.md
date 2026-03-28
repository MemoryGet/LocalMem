# MCP Enhancement Design: Progressive Retrieval + Persistent Queue + LLM Fallback

**Date:** 2026-03-28
**Status:** Approved
**Scope:** MCP server enhancement round — three coordinated improvements

## Background

Analysis of [claude-mem](https://github.com/thedotmack/claude-mem) revealed several patterns worth adopting. This design implements the three most impactful improvements focused on MCP server capabilities:

1. **Progressive Retrieval** (P0) — 10x token savings via scan → fetch workflow
2. **Persistent Async Task Queue** (P1) — Reliable async operations replacing fire-and-forget goroutines
3. **LLM Multi-Provider Fallback** (P2→promoted) — Infrastructure resilience for all LLM-dependent features

## 1. Progressive Retrieval

### Problem

Current `iclude_recall` returns full Memory objects (~500-1000 tokens/result). In MCP context, Claude often retrieves 10-50 results only to use 2-3. This wastes context window budget.

### Solution: Three MCP Tools

#### `iclude_scan` — Lightweight scan, returns compact index

```
Input:  query(string, required), scope(string), limit(1-50, default 10), filters(object)
Output: [{ id, title, score, source, kind, happened_at, token_estimate }]
```

- Reuses full `Retriever.Retrieve()` pipeline (RRF, MMR, strength weighting)
- Field trimming at handler layer — only index-level fields returned
- `token_estimate` calculated via existing CJK-aware estimator per memory content
- Expected: ~50-80 tokens/result (vs ~500-1000 for full recall)

#### `iclude_fetch` — Batch fetch full content by IDs

```
Input:  ids(string[], 1-20, required), include_embedding(bool, default false)
Output: [{ memory(full object), score(if cached) }]
```

- Direct `MemoryStore.GetByID()` calls, no retrieval pipeline
- Cap at 20 IDs to prevent oversized responses
- Optional embedding inclusion (default off to save tokens)

#### `iclude_recall` — Unchanged

Remains as the "one-shot" full retrieval tool for simple use cases.

### REST API Addition

```
POST /v1/memories/batch
Body: { "ids": ["id1", "id2", ...] }
Response: { "memories": [...] }
```

Shared by MCP `iclude_fetch` and external callers.

### Recommended Claude Workflow

```
1. iclude_scan("query") → compact index with IDs (~50-80 tokens/result)
2. Review results, pick relevant IDs
3. iclude_fetch(["id1", "id2"]) → full details for selected items only
```

## 2. Persistent Async Task Queue

### Problem

Current async operations (entity extraction in `manager.Create()`, potential vector retry) use fire-and-forget goroutines. Process crash = lost tasks. No retry mechanism.

### Solution: SQLite-backed Task Queue

#### Schema

```sql
CREATE TABLE IF NOT EXISTS async_tasks (
    id           TEXT PRIMARY KEY,
    type         TEXT NOT NULL,
    payload      TEXT NOT NULL,
    status       TEXT NOT NULL DEFAULT 'pending',
    retry_count  INTEGER NOT NULL DEFAULT 0,
    max_retries  INTEGER NOT NULL DEFAULT 3,
    error_msg    TEXT,
    created_at   DATETIME NOT NULL,
    updated_at   DATETIME NOT NULL,
    scheduled_at DATETIME,
    completed_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_async_tasks_status ON async_tasks(status, scheduled_at);
```

#### Package: `internal/queue/`

**`queue.go`** — Core TaskQueue:
- `Enqueue(ctx, taskType, payload) (id, error)` — Insert pending task
- `Poll(ctx) (*Task, error)` — Atomic: SELECT + UPDATE pending→processing
- `Complete(ctx, id) error` — Mark completed
- `Fail(ctx, id, errMsg) error` — Increment retry_count; if < max_retries reset to pending, else mark failed
- `ResetStale(ctx, timeout) (int, error)` — Reset processing tasks older than timeout back to pending

**`worker.go`** — Background worker:
- Registered as scheduler job (reuses existing `internal/scheduler/`)
- Poll interval: configurable (default 10s)
- Per-task timeout protection
- Dispatches to registered `TaskHandler` by type

**`handler.go`** — Handler interface:
```go
type TaskHandler interface {
    Handle(ctx context.Context, payload json.RawMessage) error
}
```

#### Integration Points

| Current Pattern | New Pattern |
|----------------|-------------|
| `go func() { extractor.Extract(...) }()` | `queue.Enqueue("entity_extract", payload)` |
| Vector upsert failure → log only | `queue.Enqueue("vector_upsert", payload)` on failure |

#### Configuration

```yaml
queue:
  enabled: true
  poll_interval: 10s
  max_retries: 3
  stale_timeout: 5m
```

## 3. LLM Multi-Provider Fallback Chain

### Problem

Single LLM provider failure breaks entire feature chain (extraction, preprocessing, reflection, consolidation).

### Solution: FallbackProvider Decorator

#### `internal/llm/fallback.go`

```go
type FallbackProvider struct {
    providers []Provider
    names     []string
}

func (f *FallbackProvider) Chat(ctx, req) (*ChatResponse, error) {
    // Try each provider in order, return first success
    // Log warnings on individual failures
    // Return wrapped error only if ALL providers fail
}
```

#### Configuration (backward compatible)

```yaml
llm:
  openai:                          # Primary (existing, unchanged)
    api_key: "${DEEPSEEK_API_KEY}"
    base_url: "https://api.deepseek.com/v1"
    model: "deepseek-chat"
  fallback:                        # NEW: optional ordered list
    - name: "ollama-local"
      base_url: "http://localhost:11434/v1"
      api_key: ""
      model: "qwen2.5"
    - name: "backup-openai"
      base_url: "https://api.openai.com/v1"
      api_key: "${OPENAI_API_KEY}"
      model: "gpt-4o-mini"
```

#### Bootstrap Changes (`internal/bootstrap/wiring.go`)

1. Build primary provider (existing logic unchanged)
2. Iterate `cfg.LLM.Fallback` list, build additional Provider instances
3. If fallback list non-empty → wrap as `FallbackProvider`
4. If fallback list empty → use primary directly (zero overhead, fully backward compatible)

### Explicitly Not Doing

- No concurrent racing (wasteful)
- No circuit breaker / health checks (YAGNI)
- No per-task-type routing (uniform fallback chain)

## File Change Summary

### New Files

| File | Purpose |
|------|---------|
| `internal/queue/queue.go` | Task queue core (Enqueue/Poll/Complete/Fail/ResetStale) |
| `internal/queue/worker.go` | Background worker, scheduler integration |
| `internal/queue/handler.go` | TaskHandler interface + registry |
| `internal/llm/fallback.go` | FallbackProvider decorator |
| `internal/mcp/tools/scan.go` | iclude_scan MCP tool |
| `internal/mcp/tools/fetch.go` | iclude_fetch MCP tool |
| `internal/api/batch_handler.go` | POST /v1/memories/batch handler |

### Modified Files

| File | Change |
|------|--------|
| `internal/config/config.go` | Add `Queue` and `LLM.Fallback` config structs |
| `internal/store/sqlite_migration.go` | Add V4 migration for `async_tasks` table |
| `internal/bootstrap/wiring.go` | Wire queue, fallback provider, batch handler |
| `internal/memory/manager.go` | Replace goroutine with queue.Enqueue for extraction |
| `cmd/mcp/main.go` | Register scan + fetch tools |
| `internal/api/router.go` | Register batch endpoint |

### Test Files

| File | Purpose |
|------|---------|
| `testing/queue/queue_test.go` | Queue CRUD + retry logic |
| `testing/queue/worker_test.go` | Worker polling + handler dispatch |
| `testing/llm/fallback_test.go` | Fallback chain behavior |
| `testing/mcp/scan_fetch_test.go` | Scan + fetch tool integration |
| `testing/api/batch_handler_test.go` | Batch endpoint tests |
| `testing/report/mcp_enhancement_test.go` | Dashboard-visible test cases |

## Dependencies

No new external dependencies. All implementations use:
- Existing `database/sql` + SQLite
- Existing `internal/llm.Provider` interface
- Existing `internal/mcp` framework
- Existing `internal/scheduler`

## Implementation Order

1. **LLM Fallback** — infrastructure foundation, no dependencies
2. **Persistent Queue** — depends on SQLite migration only
3. **Progressive Retrieval** — depends on queue (fetch handler reuse), but can be parallelized
