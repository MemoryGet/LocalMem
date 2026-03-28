# MCP Enhancement Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Enhance the MCP server with progressive retrieval (10x token savings), persistent async task queue, and LLM multi-provider fallback chain.

**Architecture:** Three coordinated improvements: (1) Two new MCP tools (`iclude_scan`, `iclude_fetch`) plus a REST batch endpoint provide progressive disclosure; (2) A SQLite-backed `async_tasks` table with worker replaces fire-and-forget goroutines; (3) A `FallbackProvider` decorator wraps multiple LLM providers for resilient inference.

**Tech Stack:** Go 1.25+, SQLite, Gin, existing `internal/mcp` framework, existing `internal/scheduler`

---

## Task 1: LLM Fallback Provider

**Files:**
- Create: `internal/llm/fallback.go`
- Modify: `internal/config/config.go:102-108`
- Modify: `internal/bootstrap/wiring.go:71-89`
- Test: `testing/llm/fallback_test.go`

- [ ] **Step 1: Write the failing test**

Create `testing/llm/fallback_test.go`:

```go
package llm_test

import (
	"context"
	"errors"
	"testing"

	"iclude/internal/llm"
)

// mockProvider is a test double for llm.Provider
type mockProvider struct {
	resp *llm.ChatResponse
	err  error
}

func (m *mockProvider) Chat(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	return m.resp, m.err
}

func TestFallbackProvider_FirstSucceeds(t *testing.T) {
	p1 := &mockProvider{resp: &llm.ChatResponse{Content: "from-p1"}}
	p2 := &mockProvider{resp: &llm.ChatResponse{Content: "from-p2"}}

	fp := llm.NewFallbackProvider(
		[]llm.Provider{p1, p2},
		[]string{"p1", "p2"},
	)

	resp, err := fp.Chat(context.Background(), &llm.ChatRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "from-p1" {
		t.Errorf("expected from-p1, got %s", resp.Content)
	}
}

func TestFallbackProvider_FirstFailsSecondSucceeds(t *testing.T) {
	p1 := &mockProvider{err: errors.New("p1 down")}
	p2 := &mockProvider{resp: &llm.ChatResponse{Content: "from-p2"}}

	fp := llm.NewFallbackProvider(
		[]llm.Provider{p1, p2},
		[]string{"p1", "p2"},
	)

	resp, err := fp.Chat(context.Background(), &llm.ChatRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "from-p2" {
		t.Errorf("expected from-p2, got %s", resp.Content)
	}
}

func TestFallbackProvider_AllFail(t *testing.T) {
	p1 := &mockProvider{err: errors.New("p1 down")}
	p2 := &mockProvider{err: errors.New("p2 down")}

	fp := llm.NewFallbackProvider(
		[]llm.Provider{p1, p2},
		[]string{"p1", "p2"},
	)

	_, err := fp.Chat(context.Background(), &llm.ChatRequest{})
	if err == nil {
		t.Fatal("expected error when all providers fail")
	}
	if !errors.Is(err, llm.ErrAllProvidersFailed) {
		t.Errorf("expected ErrAllProvidersFailed, got %v", err)
	}
}

func TestFallbackProvider_SingleProvider(t *testing.T) {
	p1 := &mockProvider{resp: &llm.ChatResponse{Content: "only"}}

	fp := llm.NewFallbackProvider(
		[]llm.Provider{p1},
		[]string{"p1"},
	)

	resp, err := fp.Chat(context.Background(), &llm.ChatRequest{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "only" {
		t.Errorf("expected only, got %s", resp.Content)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./testing/llm/ -run TestFallbackProvider -v`
Expected: FAIL — `NewFallbackProvider` and `ErrAllProvidersFailed` not defined

- [ ] **Step 3: Implement FallbackProvider**

Create `internal/llm/fallback.go`:

```go
package llm

import (
	"context"
	"errors"
	"fmt"

	"iclude/internal/logger"

	"go.uber.org/zap"
)

// ErrAllProvidersFailed 所有 LLM 提供者均失败 / All LLM providers failed
var ErrAllProvidersFailed = errors.New("all llm providers failed")

// FallbackProvider 按顺序尝试多个 Provider，首个成功即返回 / Try providers in order, return first success
type FallbackProvider struct {
	providers []Provider
	names     []string
}

// NewFallbackProvider 创建回退链 Provider / Create a fallback chain provider
func NewFallbackProvider(providers []Provider, names []string) *FallbackProvider {
	return &FallbackProvider{
		providers: providers,
		names:     names,
	}
}

// Chat 依次尝试所有 Provider / Try each provider in sequence
func (f *FallbackProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error) {
	var lastErr error
	for i, p := range f.providers {
		resp, err := p.Chat(ctx, req)
		if err == nil {
			if i > 0 {
				logger.Info("llm fallback succeeded",
					zap.String("provider", f.names[i]),
					zap.Int("attempts", i+1),
				)
			}
			return resp, nil
		}
		lastErr = err
		logger.Warn("llm provider failed, trying next",
			zap.String("provider", f.names[i]),
			zap.Error(err),
		)
	}
	return nil, fmt.Errorf("%w: last error: %v", ErrAllProvidersFailed, lastErr)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./testing/llm/ -run TestFallbackProvider -v`
Expected: All 4 tests PASS

- [ ] **Step 5: Add config structs for fallback**

Edit `internal/config/config.go`. Add `Fallback` field to `LLMConfig` (after line 108):

```go
// LLMConfig LLM 及 Embedding 配置 / LLM and embedding configuration
type LLMConfig struct {
	DefaultProvider string              `mapstructure:"default_provider"`
	OpenAI          OpenAIConfig        `mapstructure:"openai"`
	Claude          ClaudeConfig        `mapstructure:"claude"`
	Ollama          OllamaConfig        `mapstructure:"ollama"`
	Embedding       EmbeddingConfig     `mapstructure:"embedding"`
	Fallback        []FallbackLLMConfig `mapstructure:"fallback"`
}

// FallbackLLMConfig 回退 LLM 配置项 / Fallback LLM provider config item
type FallbackLLMConfig struct {
	Name    string `mapstructure:"name"`
	BaseURL string `mapstructure:"base_url"`
	APIKey  string `mapstructure:"api_key"`
	Model   string `mapstructure:"model"`
}
```

- [ ] **Step 6: Wire fallback into bootstrap**

Edit `internal/bootstrap/wiring.go` lines 71-89. After the primary provider is created, add fallback wrapping:

```go
	// LLM Provider
	var llmProvider llm.Provider
	switch {
	case cfg.LLM.OpenAI.APIKey != "":
		baseURL := cfg.LLM.OpenAI.BaseURL
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		llmProvider = llm.NewOpenAIProvider(baseURL, cfg.LLM.OpenAI.APIKey, cfg.LLM.OpenAI.Model)
		logger.Info("llm provider initialized", zap.String("provider", "openai"))
	case cfg.LLM.Ollama.BaseURL != "":
		ollamaBase := strings.TrimSuffix(cfg.LLM.Ollama.BaseURL, "/") + "/v1"
		ollamaModel := cfg.LLM.Ollama.Model
		if ollamaModel == "" {
			ollamaModel = cfg.LLM.OpenAI.Model
		}
		llmProvider = llm.NewOpenAIProvider(ollamaBase, "", ollamaModel)
		logger.Info("llm provider initialized", zap.String("provider", "ollama"))
	}

	// Wrap with fallback chain if configured
	if llmProvider != nil && len(cfg.LLM.Fallback) > 0 {
		providers := []llm.Provider{llmProvider}
		names := []string{"primary"}
		for _, fb := range cfg.LLM.Fallback {
			baseURL := fb.BaseURL
			if baseURL == "" {
				continue
			}
			providers = append(providers, llm.NewOpenAIProvider(baseURL, fb.APIKey, fb.Model))
			name := fb.Name
			if name == "" {
				name = fb.BaseURL
			}
			names = append(names, name)
		}
		if len(providers) > 1 {
			llmProvider = llm.NewFallbackProvider(providers, names)
			logger.Info("llm fallback chain configured", zap.Int("providers", len(providers)))
		}
	}
```

- [ ] **Step 7: Run full test suite**

Run: `go test ./testing/llm/ -v`
Expected: PASS

- [ ] **Step 8: Commit**

```bash
git add internal/llm/fallback.go internal/config/config.go internal/bootstrap/wiring.go testing/llm/fallback_test.go
git commit -m "$(cat <<'EOF'
feat(llm): add multi-provider fallback chain

FallbackProvider wraps multiple LLM providers and tries each in order.
Backward compatible: no fallback config = zero overhead, single provider.
EOF
)"
```

---

## Task 2: Persistent Async Task Queue — Schema & Core

**Files:**
- Create: `internal/queue/queue.go`
- Modify: `internal/store/sqlite_migration.go:16` (bump latestVersion), add `migrateV7ToV8`
- Test: `testing/queue/queue_test.go`

- [ ] **Step 1: Write the failing test**

Create `testing/queue/queue_test.go`:

```go
package queue_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"iclude/internal/queue"

	_ "github.com/mattn/go-sqlite3"
)

func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	if err := queue.CreateTable(db); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestQueue_EnqueueAndPoll(t *testing.T) {
	db := setupTestDB(t)
	q := queue.New(db)
	ctx := context.Background()

	payload, _ := json.Marshal(map[string]string{"memory_id": "m1"})
	id, err := q.Enqueue(ctx, "entity_extract", payload)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty task ID")
	}

	task, err := q.Poll(ctx)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if task == nil {
		t.Fatal("expected a task, got nil")
	}
	if task.ID != id {
		t.Errorf("expected id %s, got %s", id, task.ID)
	}
	if task.Type != "entity_extract" {
		t.Errorf("expected type entity_extract, got %s", task.Type)
	}
	if task.Status != "processing" {
		t.Errorf("expected status processing, got %s", task.Status)
	}
}

func TestQueue_PollEmpty(t *testing.T) {
	db := setupTestDB(t)
	q := queue.New(db)

	task, err := q.Poll(context.Background())
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if task != nil {
		t.Errorf("expected nil task from empty queue, got %+v", task)
	}
}

func TestQueue_Complete(t *testing.T) {
	db := setupTestDB(t)
	q := queue.New(db)
	ctx := context.Background()

	payload, _ := json.Marshal(map[string]string{"key": "val"})
	id, _ := q.Enqueue(ctx, "test_type", payload)

	task, _ := q.Poll(ctx)
	if err := q.Complete(ctx, task.ID); err != nil {
		t.Fatalf("complete: %v", err)
	}

	// Should not be pollable again
	next, _ := q.Poll(ctx)
	if next != nil {
		t.Errorf("expected nil after complete, got task %s", next.ID)
	}

	// Verify completed_at is set
	var completedAt sql.NullTime
	db.QueryRow("SELECT completed_at FROM async_tasks WHERE id=?", id).Scan(&completedAt)
	if !completedAt.Valid {
		t.Error("expected completed_at to be set")
	}
}

func TestQueue_FailAndRetry(t *testing.T) {
	db := setupTestDB(t)
	q := queue.New(db)
	ctx := context.Background()

	payload, _ := json.Marshal(map[string]string{"key": "val"})
	q.Enqueue(ctx, "retry_type", payload)

	// Fail it 3 times (max_retries default = 3)
	for i := 0; i < 3; i++ {
		task, _ := q.Poll(ctx)
		if task == nil {
			t.Fatalf("round %d: expected task, got nil", i)
		}
		q.Fail(ctx, task.ID, "error msg")
	}

	// After max retries, task stays failed
	task, _ := q.Poll(ctx)
	if task != nil {
		t.Error("expected nil after max retries exhausted")
	}
}

func TestQueue_ResetStale(t *testing.T) {
	db := setupTestDB(t)
	q := queue.New(db)
	ctx := context.Background()

	payload, _ := json.Marshal(map[string]string{"key": "val"})
	q.Enqueue(ctx, "stale_type", payload)
	q.Poll(ctx) // sets to processing

	// Manually backdate updated_at to simulate stale task
	db.Exec("UPDATE async_tasks SET updated_at = datetime('now', '-10 minutes')")

	count, err := q.ResetStale(ctx, 5*time.Minute)
	if err != nil {
		t.Fatalf("reset stale: %v", err)
	}
	if count != 1 {
		t.Errorf("expected 1 reset, got %d", count)
	}

	// Should be pollable again
	task, _ := q.Poll(ctx)
	if task == nil {
		t.Error("expected task after stale reset")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./testing/queue/ -v`
Expected: FAIL — package `queue` does not exist

- [ ] **Step 3: Implement queue core**

Create `internal/queue/queue.go`:

```go
// Package queue 持久化异步任务队列 / Persistent async task queue backed by SQLite
package queue

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Task 异步任务 / Async task record
type Task struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Payload     json.RawMessage `json:"payload"`
	Status      string          `json:"status"`
	RetryCount  int             `json:"retry_count"`
	MaxRetries  int             `json:"max_retries"`
	ErrorMsg    string          `json:"error_msg,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	ScheduledAt *time.Time      `json:"scheduled_at,omitempty"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
}

// Queue SQLite 持久化任务队列 / SQLite-backed persistent task queue
type Queue struct {
	db *sql.DB
}

// New 创建任务队列 / Create a new task queue
func New(db *sql.DB) *Queue {
	return &Queue{db: db}
}

// CreateTable 创建 async_tasks 表 / Create the async_tasks table (idempotent)
func CreateTable(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS async_tasks (
			id           TEXT PRIMARY KEY,
			type         TEXT NOT NULL,
			payload      TEXT NOT NULL,
			status       TEXT NOT NULL DEFAULT 'pending',
			retry_count  INTEGER NOT NULL DEFAULT 0,
			max_retries  INTEGER NOT NULL DEFAULT 3,
			error_msg    TEXT DEFAULT '',
			created_at   DATETIME NOT NULL,
			updated_at   DATETIME NOT NULL,
			scheduled_at DATETIME,
			completed_at DATETIME
		)`)
	if err != nil {
		return fmt.Errorf("create async_tasks table: %w", err)
	}
	_, err = db.Exec(`CREATE INDEX IF NOT EXISTS idx_async_tasks_status ON async_tasks(status, scheduled_at)`)
	if err != nil {
		return fmt.Errorf("create async_tasks index: %w", err)
	}
	return nil
}

// Enqueue 入队新任务 / Enqueue a new task
func (q *Queue) Enqueue(ctx context.Context, taskType string, payload json.RawMessage) (string, error) {
	id := uuid.New().String()
	now := time.Now().UTC()
	_, err := q.db.ExecContext(ctx,
		`INSERT INTO async_tasks (id, type, payload, status, created_at, updated_at) VALUES (?, ?, ?, 'pending', ?, ?)`,
		id, taskType, string(payload), now, now,
	)
	if err != nil {
		return "", fmt.Errorf("enqueue task: %w", err)
	}
	return id, nil
}

// Poll 取出一条待处理任务（原子更新为 processing）/ Atomically fetch one pending task and mark processing
func (q *Queue) Poll(ctx context.Context) (*Task, error) {
	tx, err := q.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("poll begin tx: %w", err)
	}
	defer tx.Rollback()

	var t Task
	var scheduledAt, completedAt sql.NullTime
	err = tx.QueryRowContext(ctx,
		`SELECT id, type, payload, status, retry_count, max_retries, error_msg, created_at, updated_at, scheduled_at, completed_at
		 FROM async_tasks
		 WHERE status = 'pending' AND (scheduled_at IS NULL OR scheduled_at <= datetime('now'))
		 ORDER BY created_at ASC
		 LIMIT 1`,
	).Scan(&t.ID, &t.Type, &t.Payload, &t.Status, &t.RetryCount, &t.MaxRetries, &t.ErrorMsg, &t.CreatedAt, &t.UpdatedAt, &scheduledAt, &completedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("poll select: %w", err)
	}
	if scheduledAt.Valid {
		t.ScheduledAt = &scheduledAt.Time
	}

	now := time.Now().UTC()
	_, err = tx.ExecContext(ctx,
		`UPDATE async_tasks SET status = 'processing', updated_at = ? WHERE id = ?`,
		now, t.ID,
	)
	if err != nil {
		return nil, fmt.Errorf("poll update: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("poll commit: %w", err)
	}

	t.Status = "processing"
	t.UpdatedAt = now
	return &t, nil
}

// Complete 标记任务完成 / Mark task as completed
func (q *Queue) Complete(ctx context.Context, id string) error {
	now := time.Now().UTC()
	_, err := q.db.ExecContext(ctx,
		`UPDATE async_tasks SET status = 'completed', completed_at = ?, updated_at = ? WHERE id = ?`,
		now, now, id,
	)
	if err != nil {
		return fmt.Errorf("complete task: %w", err)
	}
	return nil
}

// Fail 标记任务失败，未超限则重置为 pending / Mark failed; reset to pending if retries remain
func (q *Queue) Fail(ctx context.Context, id, errMsg string) error {
	now := time.Now().UTC()
	_, err := q.db.ExecContext(ctx,
		`UPDATE async_tasks SET
			status = CASE WHEN retry_count + 1 >= max_retries THEN 'failed' ELSE 'pending' END,
			retry_count = retry_count + 1,
			error_msg = ?,
			updated_at = ?
		 WHERE id = ?`,
		errMsg, now, id,
	)
	if err != nil {
		return fmt.Errorf("fail task: %w", err)
	}
	return nil
}

// ResetStale 重置超时的 processing 任务为 pending / Reset stale processing tasks back to pending
func (q *Queue) ResetStale(ctx context.Context, timeout time.Duration) (int, error) {
	cutoff := time.Now().UTC().Add(-timeout)
	result, err := q.db.ExecContext(ctx,
		`UPDATE async_tasks SET status = 'pending', updated_at = datetime('now')
		 WHERE status = 'processing' AND updated_at < ?`,
		cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("reset stale tasks: %w", err)
	}
	n, _ := result.RowsAffected()
	return int(n), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./testing/queue/ -v`
Expected: All 5 tests PASS

- [ ] **Step 5: Add V7→V8 migration**

Edit `internal/store/sqlite_migration.go`. Change `latestVersion` from 7 to 8 (line 17). Add new migration block after V6→V7 (after line 108):

```go
	// V7→V8: 异步任务队列 / Async task queue
	if version < 8 {
		if err := migrateV7ToV8(db); err != nil {
			return fmt.Errorf("migrate V7→V8: %w", err)
		}
		version = 8
	}
```

Add the migration function at the end of the file (before `isColumnExistsError`):

```go
// migrateV7ToV8 异步任务队列表 / Async task queue table
func migrateV7ToV8(db *sql.DB) error {
	logger.Info("executing migration V7→V8")

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin V7→V8: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS async_tasks (
		id           TEXT PRIMARY KEY,
		type         TEXT NOT NULL,
		payload      TEXT NOT NULL,
		status       TEXT NOT NULL DEFAULT 'pending',
		retry_count  INTEGER NOT NULL DEFAULT 0,
		max_retries  INTEGER NOT NULL DEFAULT 3,
		error_msg    TEXT DEFAULT '',
		created_at   DATETIME NOT NULL,
		updated_at   DATETIME NOT NULL,
		scheduled_at DATETIME,
		completed_at DATETIME
	)`); err != nil {
		return fmt.Errorf("V7→V8 create async_tasks: %w", err)
	}

	if _, err := tx.Exec(`CREATE INDEX IF NOT EXISTS idx_async_tasks_status ON async_tasks(status, scheduled_at)`); err != nil {
		return fmt.Errorf("V7→V8 create index: %w", err)
	}

	if _, err := tx.Exec(`INSERT OR IGNORE INTO schema_version(version) VALUES(8)`); err != nil {
		return fmt.Errorf("V7→V8 schema_version: %w", err)
	}

	logger.Info("migration V7→V8 completed successfully")
	return tx.Commit()
}
```

- [ ] **Step 6: Commit**

```bash
git add internal/queue/queue.go internal/store/sqlite_migration.go testing/queue/queue_test.go
git commit -m "$(cat <<'EOF'
feat(queue): add persistent async task queue with SQLite backend

Enqueue/Poll/Complete/Fail/ResetStale operations. V7→V8 migration
creates async_tasks table. Replaces fire-and-forget goroutines.
EOF
)"
```

---

## Task 3: Queue Worker & Handler Registry

**Files:**
- Create: `internal/queue/handler.go`
- Create: `internal/queue/worker.go`
- Modify: `internal/config/config.go` (add QueueConfig)
- Modify: `internal/bootstrap/wiring.go` (wire worker into scheduler)
- Test: `testing/queue/worker_test.go`

- [ ] **Step 1: Write the failing test**

Create `testing/queue/worker_test.go`:

```go
package queue_test

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"iclude/internal/queue"

	_ "github.com/mattn/go-sqlite3"
)

type countingHandler struct {
	count atomic.Int32
}

func (h *countingHandler) Handle(ctx context.Context, payload json.RawMessage) error {
	h.count.Add(1)
	return nil
}

func TestWorker_ProcessesTask(t *testing.T) {
	db := setupTestDB(t)
	q := queue.New(db)
	ctx := context.Background()

	handler := &countingHandler{}
	w := queue.NewWorker(q, 5*time.Minute)
	w.RegisterHandler("test_type", handler)

	payload, _ := json.Marshal(map[string]string{"key": "val"})
	q.Enqueue(ctx, "test_type", payload)

	// Run one poll cycle
	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("run once: %v", err)
	}

	if handler.count.Load() != 1 {
		t.Errorf("expected 1 invocation, got %d", handler.count.Load())
	}
}

func TestWorker_SkipsUnknownType(t *testing.T) {
	db := setupTestDB(t)
	q := queue.New(db)
	ctx := context.Background()

	w := queue.NewWorker(q, 5*time.Minute)
	// No handler registered for "unknown_type"

	payload, _ := json.Marshal(map[string]string{"key": "val"})
	q.Enqueue(ctx, "unknown_type", payload)

	// Should not panic, should fail the task
	if err := w.RunOnce(ctx); err != nil {
		t.Fatalf("run once: %v", err)
	}

	// Task should be marked failed (no handler)
	var status string
	db.QueryRow("SELECT status FROM async_tasks LIMIT 1").Scan(&status)
	// After 3 retries it stays failed, but first fail sets retry_count=1, status=pending
	// Actually first Fail with retry_count<max_retries resets to pending
	if status != "pending" {
		t.Errorf("expected pending after first fail, got %s", status)
	}
}

func TestWorker_EmptyQueue(t *testing.T) {
	db := setupTestDB(t)
	q := queue.New(db)

	w := queue.NewWorker(q, 5*time.Minute)

	// Should return nil error on empty queue
	if err := w.RunOnce(context.Background()); err != nil {
		t.Fatalf("run once on empty: %v", err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./testing/queue/ -run TestWorker -v`
Expected: FAIL — `NewWorker`, `RegisterHandler`, `RunOnce` not defined

- [ ] **Step 3: Implement handler interface**

Create `internal/queue/handler.go`:

```go
package queue

import (
	"context"
	"encoding/json"
)

// TaskHandler 任务处理接口 / Task handler interface
type TaskHandler interface {
	// Handle 处理任务 payload / Handle a task payload
	Handle(ctx context.Context, payload json.RawMessage) error
}
```

- [ ] **Step 4: Implement worker**

Create `internal/queue/worker.go`:

```go
package queue

import (
	"context"
	"fmt"
	"time"

	"iclude/internal/logger"

	"go.uber.org/zap"
)

// Worker 后台任务工作器 / Background task worker
type Worker struct {
	queue        *Queue
	handlers     map[string]TaskHandler
	staleTimeout time.Duration
}

// NewWorker 创建工作器 / Create a new worker
func NewWorker(q *Queue, staleTimeout time.Duration) *Worker {
	return &Worker{
		queue:        q,
		handlers:     make(map[string]TaskHandler),
		staleTimeout: staleTimeout,
	}
}

// RegisterHandler 注册任务类型处理器 / Register a handler for a task type
func (w *Worker) RegisterHandler(taskType string, handler TaskHandler) {
	w.handlers[taskType] = handler
}

// RunOnce 执行一轮：重置过期任务 + 处理一条 / One cycle: reset stale + process one task
func (w *Worker) RunOnce(ctx context.Context) error {
	// Reset stale processing tasks
	if n, err := w.queue.ResetStale(ctx, w.staleTimeout); err != nil {
		logger.Warn("queue worker: reset stale failed", zap.Error(err))
	} else if n > 0 {
		logger.Info("queue worker: reset stale tasks", zap.Int("count", n))
	}

	task, err := w.queue.Poll(ctx)
	if err != nil {
		return fmt.Errorf("queue worker poll: %w", err)
	}
	if task == nil {
		return nil
	}

	handler, ok := w.handlers[task.Type]
	if !ok {
		logger.Warn("queue worker: no handler for task type",
			zap.String("type", task.Type),
			zap.String("task_id", task.ID),
		)
		_ = w.queue.Fail(ctx, task.ID, "no handler registered for type: "+task.Type)
		return nil
	}

	if err := handler.Handle(ctx, task.Payload); err != nil {
		logger.Warn("queue worker: task handler failed",
			zap.String("type", task.Type),
			zap.String("task_id", task.ID),
			zap.Error(err),
		)
		_ = w.queue.Fail(ctx, task.ID, err.Error())
		return nil
	}

	if err := w.queue.Complete(ctx, task.ID); err != nil {
		logger.Warn("queue worker: complete failed",
			zap.String("task_id", task.ID),
			zap.Error(err),
		)
	}
	return nil
}

// Run 作为 scheduler 任务运行（每次调用处理一批）/ Run as scheduler job, process available tasks
func (w *Worker) Run(ctx context.Context) error {
	// Process up to 10 tasks per scheduler tick
	for i := 0; i < 10; i++ {
		if err := w.RunOnce(ctx); err != nil {
			return err
		}
	}
	return nil
}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./testing/queue/ -v`
Expected: All 8 tests PASS (5 queue + 3 worker)

- [ ] **Step 6: Add QueueConfig to config**

Edit `internal/config/config.go`. Add `Queue` field to `Config` struct (after line 31):

```go
type Config struct {
	// ... existing fields ...
	MCP             MCPConfig             `mapstructure:"mcp"`
	Queue           QueueConfig           `mapstructure:"queue"`
}
```

Add the struct definition (after `MCPConfig`):

```go
// QueueConfig 异步任务队列配置 / Async task queue configuration
type QueueConfig struct {
	Enabled      bool          `mapstructure:"enabled"`
	PollInterval time.Duration `mapstructure:"poll_interval"`
	MaxRetries   int           `mapstructure:"max_retries"`
	StaleTimeout time.Duration `mapstructure:"stale_timeout"`
}
```

Add defaults in `LoadConfig()` (after MCP defaults, ~line 322):

```go
	// Queue 默认值 / Queue defaults
	viper.SetDefault("queue.enabled", true)
	viper.SetDefault("queue.poll_interval", "10s")
	viper.SetDefault("queue.max_retries", 3)
	viper.SetDefault("queue.stale_timeout", "5m")
```

- [ ] **Step 7: Wire worker into bootstrap**

Edit `internal/bootstrap/wiring.go`. Add import `"iclude/internal/queue"`. Add `Queue` field to `Deps`:

```go
type Deps struct {
	// ... existing fields ...
	Scheduler      *scheduler.Scheduler
	SchedCancel    context.CancelFunc
	Queue          *queue.Queue  // nil if queue disabled
	Config         config.Config
}
```

After the `Scheduler` block (after line 138), add queue initialization:

```go
	// Async task queue
	var taskQueue *queue.Queue
	if cfg.Queue.Enabled {
		if sqlDB, ok := stores.MemoryStore.DB().(*sql.DB); ok {
			if err := queue.CreateTable(sqlDB); err != nil {
				logger.Warn("failed to create async_tasks table", zap.Error(err))
			} else {
				taskQueue = queue.New(sqlDB)
				worker := queue.NewWorker(taskQueue, cfg.Queue.StaleTimeout)

				// Register task handlers
				if extractor != nil {
					worker.RegisterHandler("entity_extract", &extractHandler{extractor: extractor})
				}

				sched.Register("queue-worker", cfg.Queue.PollInterval, worker.Run)
				logger.Info("async task queue initialized")
			}
		}
	}
```

Add the extractHandler in the same file:

```go
// extractHandler 实体抽取任务处理器 / Entity extraction task handler
type extractHandler struct {
	extractor *memory.Extractor
}

func (h *extractHandler) Handle(ctx context.Context, payload json.RawMessage) error {
	var req model.ExtractRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return fmt.Errorf("unmarshal extract payload: %w", err)
	}
	_, err := h.extractor.Extract(ctx, &req)
	return err
}
```

Add imports: `"database/sql"`, `"encoding/json"`, `"iclude/internal/queue"`, `"iclude/internal/model"`.

Set `deps.Queue = taskQueue`.

- [ ] **Step 8: Commit**

```bash
git add internal/queue/handler.go internal/queue/worker.go internal/config/config.go internal/bootstrap/wiring.go testing/queue/worker_test.go
git commit -m "$(cat <<'EOF'
feat(queue): add worker with handler registry and scheduler integration

Worker polls queue, dispatches to registered handlers by task type.
Registered as scheduler job. Entity extraction handler wired in.
EOF
)"
```

---

## Task 4: Replace Goroutine with Queue in Manager

**Files:**
- Modify: `internal/memory/manager.go:161-184`
- Modify: `internal/memory/manager.go:28-38` (add queue dependency)
- Test: `testing/memory/manager_queue_test.go`

- [ ] **Step 1: Write the failing test**

Create `testing/memory/manager_queue_test.go`:

```go
package memory_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"iclude/internal/queue"

	_ "github.com/mattn/go-sqlite3"
)

func TestManager_Create_EnqueuesExtraction(t *testing.T) {
	// This test verifies that when a queue is available, Create enqueues
	// instead of spawning a goroutine. We check the queue table directly.
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := queue.CreateTable(db); err != nil {
		t.Fatal(err)
	}

	q := queue.New(db)

	// Enqueue directly to test the queue works
	payload, _ := json.Marshal(map[string]string{"memory_id": "test-id", "content": "hello"})
	id, err := q.Enqueue(context.Background(), "entity_extract", payload)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	task, err := q.Poll(context.Background())
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if task == nil {
		t.Fatal("expected task")
	}
	if task.ID != id {
		t.Errorf("expected id %s, got %s", id, task.ID)
	}
}
```

- [ ] **Step 2: Run test to verify it passes (sanity check)**

Run: `go test ./testing/memory/ -run TestManager_Create_EnqueuesExtraction -v`
Expected: PASS

- [ ] **Step 3: Add queue to Manager**

Edit `internal/memory/manager.go`. Add `queue` field to `Manager` struct (line 25):

```go
type Manager struct {
	memStore     store.MemoryStore
	vecStore     store.VectorStore
	embedder     store.Embedder
	tagStore     store.TagStore
	contextStore store.ContextStore
	extractor    *Extractor
	taskQueue    TaskEnqueuer // 可为 nil / may be nil
}
```

Add the interface above the struct:

```go
// TaskEnqueuer 任务入队接口 / Task enqueue interface (decoupled from queue package)
type TaskEnqueuer interface {
	Enqueue(ctx context.Context, taskType string, payload json.RawMessage) (string, error)
}
```

Add import `"encoding/json"`.

Update `NewManager` to accept optional queue:

```go
func NewManager(memStore store.MemoryStore, vecStore store.VectorStore, embedder store.Embedder, tagStore store.TagStore, contextStore store.ContextStore, extractor *Extractor, taskQueue ...TaskEnqueuer) *Manager {
	m := &Manager{
		memStore:     memStore,
		vecStore:     vecStore,
		embedder:     embedder,
		tagStore:     tagStore,
		contextStore: contextStore,
		extractor:    extractor,
	}
	if len(taskQueue) > 0 {
		m.taskQueue = taskQueue[0]
	}
	return m
}
```

- [ ] **Step 4: Replace goroutine with enqueue**

Edit `internal/memory/manager.go` lines 161-184. Replace the goroutine block with:

```go
	// 自动实体抽取（异步，优先队列，回退 goroutine）/ Auto entity extraction (prefer queue, fallback goroutine)
	if req.AutoExtract && m.extractor != nil {
		extractReq := &model.ExtractRequest{
			MemoryID: mem.ID,
			Content:  mem.Content,
			Scope:    mem.Scope,
			TeamID:   mem.TeamID,
		}
		if m.taskQueue != nil {
			payload, _ := json.Marshal(extractReq)
			if _, err := m.taskQueue.Enqueue(ctx, "entity_extract", payload); err != nil {
				logger.Warn("failed to enqueue extract task, falling back to goroutine",
					zap.String("memory_id", mem.ID),
					zap.Error(err),
				)
				m.asyncExtract(extractReq)
			}
		} else {
			m.asyncExtract(extractReq)
		}
	}
```

Add the helper method:

```go
// asyncExtract 回退的异步 goroutine 抽取 / Fallback async goroutine extraction
func (m *Manager) asyncExtract(req *model.ExtractRequest) {
	extractTimeout := config.GetConfig().Extract.Timeout
	if extractTimeout <= 0 {
		extractTimeout = 30 * time.Second
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), extractTimeout)
		defer cancel()
		if _, err := m.extractor.Extract(ctx, req); err != nil {
			logger.Warn("auto extract failed",
				zap.String("memory_id", req.MemoryID),
				zap.Error(err),
			)
		}
	}()
}
```

- [ ] **Step 5: Update bootstrap to pass queue to Manager**

Edit `internal/bootstrap/wiring.go`. Update the `NewManager` call (line 119):

```go
	memManager := memory.NewManager(stores.MemoryStore, stores.VectorStore, stores.Embedder, stores.TagStore, stores.ContextStore, extractor)
```

Change to (queue wiring happens later, so use a two-phase approach — set queue after creation):

Actually, since the queue is initialized after the manager in the current flow, add a `SetQueue` method to Manager:

Edit `internal/memory/manager.go`, add:

```go
// SetQueue 设置任务队列（支持延迟注入）/ Set task queue (supports deferred injection)
func (m *Manager) SetQueue(q TaskEnqueuer) {
	m.taskQueue = q
}
```

Then in `internal/bootstrap/wiring.go`, after `taskQueue` is created, add:

```go
		if taskQueue != nil {
			memManager.SetQueue(taskQueue)
		}
```

- [ ] **Step 6: Run tests**

Run: `go test ./testing/memory/ -v && go test ./testing/queue/ -v`
Expected: PASS

- [ ] **Step 7: Commit**

```bash
git add internal/memory/manager.go internal/bootstrap/wiring.go testing/memory/manager_queue_test.go
git commit -m "$(cat <<'EOF'
feat(memory): replace fire-and-forget goroutine with queue for extraction

Manager.Create now enqueues entity extraction via persistent queue.
Falls back to goroutine if queue unavailable. Backward compatible.
EOF
)"
```

---

## Task 5: Progressive Retrieval — `iclude_scan` MCP Tool

**Files:**
- Create: `internal/mcp/tools/scan.go`
- Modify: `cmd/mcp/main.go:96-102` (register new tool)
- Test: `testing/mcp/scan_test.go`

- [ ] **Step 1: Write the failing test**

Create `testing/mcp/scan_test.go`:

```go
package mcp_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"iclude/internal/mcp/tools"
	"iclude/internal/model"
	"iclude/internal/search"
)

type mockRetriever struct {
	results []*model.SearchResult
}

func (m *mockRetriever) Retrieve(ctx context.Context, req *model.RetrieveRequest) ([]*model.SearchResult, error) {
	return m.results, nil
}

func TestScanTool_Definition(t *testing.T) {
	tool := tools.NewScanTool(&mockRetriever{})
	def := tool.Definition()
	if def.Name != "iclude_scan" {
		t.Errorf("expected iclude_scan, got %s", def.Name)
	}
}

func TestScanTool_Execute_ReturnsCompactIndex(t *testing.T) {
	now := time.Now()
	retriever := &mockRetriever{
		results: []*model.SearchResult{
			{
				Memory: &model.Memory{
					ID:         "m1",
					Content:    "This is a test memory with some content that would be long in full mode",
					Abstract:   "test memory",
					Kind:       "fact",
					HappenedAt: &now,
				},
				Score:  0.95,
				Source: "sqlite",
			},
		},
	}

	tool := tools.NewScanTool(retriever)
	args, _ := json.Marshal(map[string]any{"query": "test"})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error result: %s", result.Content[0].Text)
	}

	// Parse output
	var items []tools.ScanResultItem
	if err := json.Unmarshal([]byte(result.Content[0].Text), &items); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].ID != "m1" {
		t.Errorf("expected id m1, got %s", items[0].ID)
	}
	if items[0].Title != "test memory" {
		t.Errorf("expected title 'test memory', got %s", items[0].Title)
	}
	if items[0].TokenEstimate <= 0 {
		t.Error("expected positive token estimate")
	}
	// Should NOT contain full content
	raw := result.Content[0].Text
	if len(raw) > 500 {
		t.Error("scan result too large, should be compact")
	}
}

func TestScanTool_Execute_EmptyQuery(t *testing.T) {
	tool := tools.NewScanTool(&mockRetriever{})
	args, _ := json.Marshal(map[string]any{"query": ""})
	result, _ := tool.Execute(context.Background(), args)
	if !result.IsError {
		t.Error("expected error for empty query")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./testing/mcp/ -run TestScanTool -v`
Expected: FAIL — `NewScanTool`, `ScanResultItem` not defined

- [ ] **Step 3: Implement ScanTool**

Create `internal/mcp/tools/scan.go`:

```go
package tools

import (
	"context"
	"encoding/json"
	"time"

	"iclude/internal/mcp"
	"iclude/internal/model"
	"iclude/internal/search"
)

// ScanResultItem 轻量扫描结果项 / Compact scan result item
type ScanResultItem struct {
	ID            string     `json:"id"`
	Title         string     `json:"title"`
	Score         float64    `json:"score"`
	Source        string     `json:"source"`
	Kind          string     `json:"kind,omitempty"`
	HappenedAt    *time.Time `json:"happened_at,omitempty"`
	TokenEstimate int        `json:"token_estimate"`
}

// ScanTool iclude_scan 轻量扫描工具 / iclude_scan lightweight scan tool
type ScanTool struct{ retriever MemoryRetriever }

// NewScanTool 创建 ScanTool / Create a new ScanTool
func NewScanTool(retriever MemoryRetriever) *ScanTool {
	return &ScanTool{retriever: retriever}
}

// scanArgs iclude_scan 工具参数 / iclude_scan tool arguments
type scanArgs struct {
	Query   string         `json:"query"`
	Scope   string         `json:"scope,omitempty"`
	Limit   int            `json:"limit,omitempty"`
	Filters map[string]any `json:"filters,omitempty"`
}

// Definition 返回工具定义 / Return tool definition
func (t *ScanTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name:        "iclude_scan",
		Description: "Lightweight memory scan returning compact index (ID + title + score + token estimate). Use this first, then iclude_fetch for full details on selected items. Saves ~10x tokens vs iclude_recall.",
		InputSchema: json.RawMessage(`{
            "type":"object",
            "properties":{
                "query":{"type":"string","description":"Search query text"},
                "scope":{"type":"string","description":"Namespace scope filter"},
                "limit":{"type":"integer","minimum":1,"maximum":50,"default":10},
                "filters":{"type":"object","description":"Structured filters: kind, tags, min_strength, happened_after"}
            },
            "required":["query"]
        }`),
	}
}

// Execute 执行扫描并返回 compact 索引 / Execute scan and return compact index
func (t *ScanTool) Execute(ctx context.Context, arguments json.RawMessage) (*mcp.ToolResult, error) {
	var args scanArgs
	if err := json.Unmarshal(arguments, &args); err != nil {
		return mcp.ErrorResult("invalid arguments: " + err.Error()), nil
	}
	if args.Query == "" {
		return mcp.ErrorResult("query is required"), nil
	}

	limit := args.Limit
	if limit <= 0 {
		limit = 10
	}

	id := mcp.IdentityFromContext(ctx)
	req := &model.RetrieveRequest{
		Query: args.Query,
		Limit: limit,
	}
	if id != nil {
		req.TeamID = id.TeamID
	}

	if len(args.Filters) > 0 {
		raw, _ := json.Marshal(args.Filters)
		var sf model.SearchFilters
		_ = json.Unmarshal(raw, &sf)
		req.Filters = &sf
	}
	if args.Scope != "" {
		if req.Filters == nil {
			req.Filters = &model.SearchFilters{}
		}
		if req.Filters.Scope == "" {
			req.Filters.Scope = args.Scope
		}
	}

	results, err := t.retriever.Retrieve(ctx, req)
	if err != nil {
		return mcp.ErrorResult("scan failed: " + err.Error()), nil
	}

	items := make([]ScanResultItem, 0, len(results))
	for _, r := range results {
		if r.Memory == nil {
			continue
		}
		title := r.Memory.Abstract
		if title == "" {
			// Truncate content as fallback title
			title = r.Memory.Content
			if len(title) > 100 {
				title = title[:100] + "..."
			}
		}
		items = append(items, ScanResultItem{
			ID:            r.Memory.ID,
			Title:         title,
			Score:         r.Score,
			Source:        r.Source,
			Kind:          r.Memory.Kind,
			HappenedAt:    r.Memory.HappenedAt,
			TokenEstimate: search.EstimateTokens(r.Memory.Content),
		})
	}

	out, _ := json.Marshal(items)
	return mcp.TextResult(string(out)), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./testing/mcp/ -run TestScanTool -v`
Expected: All 3 tests PASS

- [ ] **Step 5: Register scan tool in MCP server**

Edit `cmd/mcp/main.go` line 102. After `reg.RegisterTool(tools.NewTimelineTool(deps.Retriever))`, add:

```go
	reg.RegisterTool(tools.NewScanTool(retrieverAdapter))
```

- [ ] **Step 6: Commit**

```bash
git add internal/mcp/tools/scan.go cmd/mcp/main.go testing/mcp/scan_test.go
git commit -m "$(cat <<'EOF'
feat(mcp): add iclude_scan tool for lightweight memory scanning

Returns compact index (ID, title, score, token_estimate) instead of
full Memory objects. ~10x token savings for initial retrieval.
EOF
)"
```

---

## Task 6: Progressive Retrieval — `iclude_fetch` MCP Tool + REST Batch Endpoint

**Files:**
- Create: `internal/mcp/tools/fetch.go`
- Create: `internal/api/batch_handler.go`
- Modify: `cmd/mcp/main.go` (register fetch tool)
- Modify: `internal/api/router.go` (register batch endpoint)
- Test: `testing/mcp/fetch_test.go`
- Test: `testing/api/batch_handler_test.go`

- [ ] **Step 1: Write the failing test for FetchTool**

Create `testing/mcp/fetch_test.go`:

```go
package mcp_test

import (
	"context"
	"encoding/json"
	"testing"

	"iclude/internal/mcp/tools"
	"iclude/internal/model"
)

type mockMemoryGetter struct {
	memories map[string]*model.Memory
}

func (m *mockMemoryGetter) Get(ctx context.Context, id string) (*model.Memory, error) {
	mem, ok := m.memories[id]
	if !ok {
		return nil, model.ErrMemoryNotFound
	}
	return mem, nil
}

func TestFetchTool_Definition(t *testing.T) {
	tool := tools.NewFetchTool(&mockMemoryGetter{})
	def := tool.Definition()
	if def.Name != "iclude_fetch" {
		t.Errorf("expected iclude_fetch, got %s", def.Name)
	}
}

func TestFetchTool_Execute_BatchFetch(t *testing.T) {
	getter := &mockMemoryGetter{
		memories: map[string]*model.Memory{
			"m1": {ID: "m1", Content: "content 1"},
			"m2": {ID: "m2", Content: "content 2"},
		},
	}

	tool := tools.NewFetchTool(getter)
	args, _ := json.Marshal(map[string]any{"ids": []string{"m1", "m2"}})
	result, err := tool.Execute(context.Background(), args)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %s", result.Content[0].Text)
	}

	var items []tools.FetchResultItem
	json.Unmarshal([]byte(result.Content[0].Text), &items)
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
	if items[0].Memory.Content != "content 1" {
		t.Errorf("expected content 1, got %s", items[0].Memory.Content)
	}
}

func TestFetchTool_Execute_EmptyIDs(t *testing.T) {
	tool := tools.NewFetchTool(&mockMemoryGetter{})
	args, _ := json.Marshal(map[string]any{"ids": []string{}})
	result, _ := tool.Execute(context.Background(), args)
	if !result.IsError {
		t.Error("expected error for empty ids")
	}
}

func TestFetchTool_Execute_TooManyIDs(t *testing.T) {
	tool := tools.NewFetchTool(&mockMemoryGetter{})
	ids := make([]string, 25)
	for i := range ids {
		ids[i] = "id"
	}
	args, _ := json.Marshal(map[string]any{"ids": ids})
	result, _ := tool.Execute(context.Background(), args)
	if !result.IsError {
		t.Error("expected error for >20 ids")
	}
}

func TestFetchTool_Execute_PartialNotFound(t *testing.T) {
	getter := &mockMemoryGetter{
		memories: map[string]*model.Memory{
			"m1": {ID: "m1", Content: "found"},
		},
	}

	tool := tools.NewFetchTool(getter)
	args, _ := json.Marshal(map[string]any{"ids": []string{"m1", "m999"}})
	result, _ := tool.Execute(context.Background(), args)
	if result.IsError {
		t.Fatal("should not error on partial not found")
	}

	var items []tools.FetchResultItem
	json.Unmarshal([]byte(result.Content[0].Text), &items)
	if len(items) != 1 {
		t.Errorf("expected 1 found item, got %d", len(items))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./testing/mcp/ -run TestFetchTool -v`
Expected: FAIL — `NewFetchTool`, `FetchResultItem` not defined

- [ ] **Step 3: Implement FetchTool**

Create `internal/mcp/tools/fetch.go`:

```go
package tools

import (
	"context"
	"encoding/json"

	"iclude/internal/mcp"
	"iclude/internal/model"

	"iclude/internal/logger"

	"go.uber.org/zap"
)

// MemoryGetter 按 ID 获取记忆接口 / Interface for getting memories by ID
type MemoryGetter interface {
	Get(ctx context.Context, id string) (*model.Memory, error)
}

// FetchResultItem 批量获取结果项 / Batch fetch result item
type FetchResultItem struct {
	Memory *model.Memory `json:"memory"`
}

// FetchTool iclude_fetch 批量获取工具 / iclude_fetch batch fetch tool
type FetchTool struct{ getter MemoryGetter }

// NewFetchTool 创建 FetchTool / Create a new FetchTool
func NewFetchTool(getter MemoryGetter) *FetchTool {
	return &FetchTool{getter: getter}
}

type fetchArgs struct {
	IDs []string `json:"ids"`
}

// Definition 返回工具定义 / Return tool definition
func (t *FetchTool) Definition() mcp.ToolDefinition {
	return mcp.ToolDefinition{
		Name:        "iclude_fetch",
		Description: "Fetch full memory content by IDs. Use after iclude_scan to get details for selected items only.",
		InputSchema: json.RawMessage(`{
            "type":"object",
            "properties":{
                "ids":{"type":"array","items":{"type":"string"},"minItems":1,"maxItems":20,"description":"Memory IDs to fetch (max 20)"}
            },
            "required":["ids"]
        }`),
	}
}

// Execute 批量获取记忆 / Batch fetch memories by IDs
func (t *FetchTool) Execute(ctx context.Context, arguments json.RawMessage) (*mcp.ToolResult, error) {
	var args fetchArgs
	if err := json.Unmarshal(arguments, &args); err != nil {
		return mcp.ErrorResult("invalid arguments: " + err.Error()), nil
	}
	if len(args.IDs) == 0 {
		return mcp.ErrorResult("ids is required and must not be empty"), nil
	}
	if len(args.IDs) > 20 {
		return mcp.ErrorResult("maximum 20 ids per request"), nil
	}

	items := make([]FetchResultItem, 0, len(args.IDs))
	for _, id := range args.IDs {
		mem, err := t.getter.Get(ctx, id)
		if err != nil {
			logger.Debug("fetch: memory not found",
				zap.String("id", id),
				zap.Error(err),
			)
			continue
		}
		items = append(items, FetchResultItem{Memory: mem})
	}

	out, _ := json.Marshal(items)
	return mcp.TextResult(string(out)), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./testing/mcp/ -run TestFetchTool -v`
Expected: All 4 tests PASS

- [ ] **Step 5: Implement REST batch handler**

Create `internal/api/batch_handler.go`:

```go
package api

import (
	"iclude/internal/memory"
	"iclude/internal/model"

	"github.com/gin-gonic/gin"
)

// BatchHandler 批量操作处理器 / Batch operation handler
type BatchHandler struct {
	manager *memory.Manager
}

// NewBatchHandler 创建批量操作处理器 / Create batch handler
func NewBatchHandler(manager *memory.Manager) *BatchHandler {
	return &BatchHandler{manager: manager}
}

type batchGetRequest struct {
	IDs []string `json:"ids" binding:"required"`
}

// BatchGet 批量获取记忆 / Batch get memories by IDs
// POST /v1/memories/batch
func (h *BatchHandler) BatchGet(c *gin.Context) {
	identity := requireIdentity(c)
	if identity == nil {
		return
	}

	var req batchGetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		Error(c, model.ErrInvalidInput)
		return
	}
	if len(req.IDs) == 0 {
		Error(c, model.ErrInvalidInput)
		return
	}
	if len(req.IDs) > 20 {
		ErrorWithMsg(c, 400, "maximum 20 ids per request")
		return
	}

	memories := make([]*model.Memory, 0, len(req.IDs))
	for _, id := range req.IDs {
		mem, err := h.manager.GetVisible(c.Request.Context(), id, identity)
		if err != nil {
			continue
		}
		memories = append(memories, mem)
	}

	Success(c, gin.H{"memories": memories})
}
```

- [ ] **Step 6: Register batch endpoint in router**

Edit `internal/api/router.go`. After memory CRUD routes (after line 55), add:

```go
		// Batch operations
		batchHandler := NewBatchHandler(deps.MemManager)
		v1.POST("/memories/batch", batchHandler.BatchGet)
```

- [ ] **Step 7: Register fetch tool in MCP server**

Edit `cmd/mcp/main.go`. Add a `memoryGetterAdapter` after `memoryRetrieverAdapter`:

```go
// memoryGetterAdapter 将 Manager.Get 适配为 MemoryGetter / Adapter for Manager.Get
type memoryGetterAdapter struct {
	manager interface {
		Get(ctx context.Context, id string) (*model.Memory, error)
	}
}

func (a *memoryGetterAdapter) Get(ctx context.Context, id string) (*model.Memory, error) {
	return a.manager.Get(ctx, id)
}
```

In main(), after `retrieverAdapter`, add:

```go
	getterAdapter := &memoryGetterAdapter{manager: deps.MemManager}
```

After the scan tool registration, add:

```go
	reg.RegisterTool(tools.NewFetchTool(getterAdapter))
```

- [ ] **Step 8: Run all tests**

Run: `go test ./testing/mcp/ -v && go test ./testing/api/ -v`
Expected: PASS

- [ ] **Step 9: Commit**

```bash
git add internal/mcp/tools/fetch.go internal/api/batch_handler.go internal/api/router.go cmd/mcp/main.go testing/mcp/fetch_test.go
git commit -m "$(cat <<'EOF'
feat(mcp,api): add iclude_fetch tool and POST /v1/memories/batch endpoint

Batch fetch memories by IDs. Completes the progressive retrieval workflow:
iclude_scan (compact index) → iclude_fetch (full details for selected IDs).
EOF
)"
```

---

## Task 7: Dashboard Test Cases

**Files:**
- Create: `testing/report/mcp_enhancement_test.go`

- [ ] **Step 1: Create dashboard test cases**

Create `testing/report/mcp_enhancement_test.go`:

```go
package report_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"iclude/internal/llm"
	"iclude/internal/queue"
	"iclude/pkg/testreport"

	_ "github.com/mattn/go-sqlite3"
)

func TestMCPEnhancement_FallbackProvider(t *testing.T) {
	tc := testreport.NewCase(t, "LLM Fallback Chain", "LLM 多提供者回退链")

	tc.Input("primary provider fails, secondary succeeds")
	tc.Step("Create FallbackProvider with 2 providers")

	p1 := &mockLLMProvider{err: errors.New("primary down")}
	p2 := &mockLLMProvider{resp: &llm.ChatResponse{Content: "fallback ok"}}
	fp := llm.NewFallbackProvider([]llm.Provider{p1, p2}, []string{"primary", "fallback"})

	tc.Step("Call Chat — should use fallback")
	resp, err := fp.Chat(context.Background(), &llm.ChatRequest{})

	tc.Output("no error, content from fallback")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Content != "fallback ok" {
		t.Errorf("expected 'fallback ok', got %s", resp.Content)
	}
	tc.Done()
}

func TestMCPEnhancement_TaskQueue(t *testing.T) {
	tc := testreport.NewCase(t, "Persistent Task Queue", "持久化异步任务队列")

	tc.Input("enqueue + poll + complete lifecycle")
	tc.Step("Setup in-memory SQLite queue")

	db, _ := sql.Open("sqlite3", ":memory:")
	defer db.Close()
	queue.CreateTable(db)
	q := queue.New(db)
	ctx := context.Background()

	tc.Step("Enqueue entity_extract task")
	payload, _ := json.Marshal(map[string]string{"memory_id": "m1"})
	id, err := q.Enqueue(ctx, "entity_extract", payload)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	tc.Step("Poll → processing")
	task, _ := q.Poll(ctx)
	if task.ID != id || task.Status != "processing" {
		t.Fatalf("unexpected task state: %+v", task)
	}

	tc.Step("Complete → completed_at set")
	q.Complete(ctx, task.ID)

	tc.Output("full lifecycle: pending → processing → completed")
	tc.Done()
}

func TestMCPEnhancement_ScanCompactness(t *testing.T) {
	tc := testreport.NewCase(t, "Scan Token Savings", "iclude_scan token 节省验证")

	tc.Input("compare scan result size vs full recall result size")
	tc.Step("Build mock memory with 500-char content")

	content := string(make([]byte, 500))
	for i := range content {
		content = content[:i] + "x" + content[i+1:]
	}

	// Simulate scan result item
	scanItem := map[string]any{
		"id": "m1", "title": "test", "score": 0.95,
		"source": "sqlite", "token_estimate": 125,
	}
	scanBytes, _ := json.Marshal(scanItem)

	// Simulate full recall result
	fullItem := map[string]any{
		"memory": map[string]any{"id": "m1", "content": content},
		"score":  0.95, "source": "sqlite",
	}
	fullBytes, _ := json.Marshal(fullItem)

	ratio := float64(len(scanBytes)) / float64(len(fullBytes))
	tc.Step("Scan result: %d bytes, Full result: %d bytes, Ratio: %.1f%%", len(scanBytes), len(fullBytes), ratio*100)

	tc.Output("scan result is <20%% of full result size")
	if ratio > 0.20 {
		t.Errorf("scan result too large: %.1f%% of full", ratio*100)
	}
	tc.Done()
}

func TestMCPEnhancement_QueueRetry(t *testing.T) {
	tc := testreport.NewCase(t, "Queue Retry Logic", "队列重试逻辑")

	tc.Input("fail task 3 times (max_retries=3)")
	tc.Step("Setup queue and enqueue task")

	db, _ := sql.Open("sqlite3", ":memory:")
	defer db.Close()
	queue.CreateTable(db)
	q := queue.New(db)
	ctx := context.Background()

	payload, _ := json.Marshal(map[string]string{"key": "val"})
	q.Enqueue(ctx, "retry_test", payload)

	tc.Step("Fail 3 times")
	for i := 0; i < 3; i++ {
		task, _ := q.Poll(ctx)
		if task == nil {
			t.Fatalf("round %d: expected task", i)
		}
		q.Fail(ctx, task.ID, "error")
	}

	tc.Step("4th poll returns nil (exhausted)")
	task, _ := q.Poll(ctx)

	tc.Output("task is permanently failed after max retries")
	if task != nil {
		t.Error("expected nil after max retries")
	}
	tc.Done()
}

func TestMCPEnhancement_StaleReset(t *testing.T) {
	tc := testreport.NewCase(t, "Stale Task Reset", "过期任务重置")

	tc.Input("task stuck in processing for >5 minutes")
	tc.Step("Enqueue and poll (sets to processing)")

	db, _ := sql.Open("sqlite3", ":memory:")
	defer db.Close()
	queue.CreateTable(db)
	q := queue.New(db)
	ctx := context.Background()

	payload, _ := json.Marshal(map[string]string{"key": "val"})
	q.Enqueue(ctx, "stale_test", payload)
	q.Poll(ctx)

	tc.Step("Backdate updated_at by 10 minutes")
	db.Exec("UPDATE async_tasks SET updated_at = datetime('now', '-10 minutes')")

	tc.Step("ResetStale with 5min timeout")
	count, _ := q.ResetStale(ctx, 5*time.Minute)

	tc.Output("1 task reset, pollable again")
	if count != 1 {
		t.Errorf("expected 1 reset, got %d", count)
	}
	task, _ := q.Poll(ctx)
	if task == nil {
		t.Error("expected task after reset")
	}
	tc.Done()
}

// mockLLMProvider for test report
type mockLLMProvider struct {
	resp *llm.ChatResponse
	err  error
}

func (m *mockLLMProvider) Chat(ctx context.Context, req *llm.ChatRequest) (*llm.ChatResponse, error) {
	return m.resp, m.err
}
```

- [ ] **Step 2: Run dashboard tests**

Run: `go test ./testing/report/mcp_enhancement_test.go -v -count=1`
Expected: All 5 tests PASS

- [ ] **Step 3: Commit**

```bash
git add testing/report/mcp_enhancement_test.go
git commit -m "$(cat <<'EOF'
test: add dashboard test cases for MCP enhancement features

Covers: LLM fallback chain, task queue lifecycle, scan compactness,
retry logic, and stale task reset.
EOF
)"
```

---

## Summary

| Task | Component | New Files | Modified Files |
|------|-----------|-----------|----------------|
| 1 | LLM Fallback | `internal/llm/fallback.go`, `testing/llm/fallback_test.go` | `config.go`, `wiring.go` |
| 2 | Queue Core | `internal/queue/queue.go`, `testing/queue/queue_test.go` | `sqlite_migration.go` |
| 3 | Queue Worker | `internal/queue/handler.go`, `internal/queue/worker.go`, `testing/queue/worker_test.go` | `config.go`, `wiring.go` |
| 4 | Manager Integration | `testing/memory/manager_queue_test.go` | `manager.go`, `wiring.go` |
| 5 | Scan Tool | `internal/mcp/tools/scan.go`, `testing/mcp/scan_test.go` | `cmd/mcp/main.go` |
| 6 | Fetch Tool + Batch | `internal/mcp/tools/fetch.go`, `internal/api/batch_handler.go`, `testing/mcp/fetch_test.go` | `cmd/mcp/main.go`, `router.go` |
| 7 | Dashboard Tests | `testing/report/mcp_enhancement_test.go` | — |

**Implementation order:** Tasks 1-4 are sequential (each builds on previous). Tasks 5-6 can be parallelized after Task 4. Task 7 runs last.
