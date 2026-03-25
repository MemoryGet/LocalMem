# IClude MCP Server — Design Spec

**Date**: 2026-03-25
**Status**: Approved
**Author**: Architecture review (IClude Phase 3 — Task 3a-1)

---

## 1. Problem Statement

IClude Phase 1+2 delivers a full REST API memory backend. To integrate with AI coding tools (Claude CLI, Cursor, Windsurf, Gemini CLI) without per-tool custom adapters, IClude needs a standard MCP (Model Context Protocol) server that any MCP-compatible client can connect to via a single URL.

The MCP server must implement **hybrid context management**:
- Automatic memory injection at session start (read from IClude → inject into context window)
- Explicit tool calls for recall/retain/reflect during the conversation
- Automatic saving of conversation turns after each exchange

---

## 2. Goals

- Expose IClude capabilities as standard MCP Tools, Resources, and Prompts
- MCP 2024-11-05 SSE transport (GET /sse + POST /messages) — compatible with Claude CLI `"type": "sse"` config
- Zero new third-party dependencies — stdlib `net/http` + `encoding/json` only
- Fully decoupled from the REST API layer (`internal/api/`) — no Gin dependency
- Each MCP handler is independently testable
- Single binary: `iclude mcp` starts the MCP server on port 8081

## 3. Non-Goals

- MCP stdio transport (out of scope; SSE/HTTP covers all use cases)
- MCP 2025-03-26 Streamable HTTP transport (single POST /mcp — future upgrade path, not this task)
- Authentication / JWT for MCP sessions (config-based identity only; JWT is Phase 3c)
- Node.js / Python MCP SDK (Go implementation only)
- Embedding the MCP server into the REST API process (separate binaries)

---

## 4. Architecture

### 4.1 Process Model

Two independent binaries sharing the same `internal/` packages:

```
iclude serve   →  REST API   (port 8080, existing cmd/server/main.go)
iclude mcp     →  MCP Server (port 8081, new cmd/mcp/main.go)
```

Both binaries call `bootstrap.Init()` from the new `internal/bootstrap/wiring.go`. The wiring logic is **extracted from** `cmd/server/main.go` (which is refactored to call `bootstrap.Init()` as well — same behaviour, no functional change).

Each binary manages its own SQLite connection pool. SQLite WAL mode handles concurrent access; `busy_timeout=5000ms` handles write contention.

### 4.2 Transport: MCP SSE (2024-11-05)

```
MCP Client (Claude CLI "type":"sse" / Cursor / Windsurf / ...)
    │
    ├─ GET  /sse                   → establish SSE long-lived connection
    │       ← event: endpoint      ← server sends: data: /messages?session={id}
    │
    ├─ POST /messages?session={id} → JSON-RPC request body
    │       ← event: message       ← JSON-RPC response arrives via SSE
    │
    └─ (connection drops)          → server closes session
```

No Gin. Pure `net/http` ServeMux. `protocolVersion` reported in initialize response: `"2024-11-05"`.

### 4.3 bootstrap.Init() — Shared Wiring

`internal/bootstrap/wiring.go` exports:

```go
// Deps 所有已初始化的业务组件 / All initialized business components
type Deps struct {
    Stores         *store.Stores
    MemManager     *memory.Manager
    Retriever      *search.Retriever
    ContextManager *memory.ContextManager    // nil if ContextStore unavailable
    GraphManager   *memory.GraphManager      // nil if GraphStore unavailable
    ReflectEngine  *reflectpkg.ReflectEngine // nil if LLM unavailable
    Extractor      *memory.Extractor         // nil if LLM unavailable
    Scheduler      *scheduler.Scheduler      // always non-nil; tasks registered conditionally
    Config         config.Config
}

// Init 根据配置初始化所有组件 / Initialize all components from config
// cleanup 关闭所有资源（scheduler stop + stores.Close）/ Cleanup closes all resources
func Init(ctx context.Context, cfg config.Config) (*Deps, func(), error)
```

**MCP server wires**: MemManager, Retriever, ContextManager, GraphManager, ReflectEngine, Extractor, Scheduler.
**MCP server does NOT wire**: DocProcessor (document upload not exposed via MCP in this task), Heartbeat (background-only), Consolidator (background-only). These remain config-gated in the scheduler and are still registered when `scheduler.enabled: true`.

`cmd/server/main.go` is refactored to call `bootstrap.Init()` instead of inlining the wiring sequence. Its observable behaviour is unchanged.

### 4.4 Dependency Graph

```
cmd/mcp/main.go
    └─ internal/bootstrap        (shared wiring)
    └─ internal/mcp              (protocol, interfaces, registry, server, session)
    └─ internal/mcp/tools        (5 tool handlers)
    └─ internal/mcp/resources    (2 resource handlers)
    └─ internal/mcp/prompts      (1 prompt handler)

internal/mcp/tools|resources|prompts
    └─ internal/mcp              (interfaces, result helpers, identity context)
    └─ internal/memory           (Manager)
    └─ internal/search           (Retriever)
    └─ internal/reflect          (ReflectEngine)
    └─ internal/model            (DTOs, Identity)

internal/mcp        → internal/model  (Identity only)
internal/api        → (unchanged; zero dependency on internal/mcp)
```

No circular dependencies introduced.

---

## 5. Directory Structure

```
cmd/mcp/
    main.go                         # Entry point (~90 lines)

internal/bootstrap/
    wiring.go                       # Shared init extracted from cmd/server/main.go (~150 lines)

internal/mcp/
    protocol.go                     # JSON-RPC 2.0 types, MCP method constants, definition structs
    handler.go                      # ToolHandler / ResourceHandler / PromptHandler interfaces
    registry.go                     # Registration + dispatch for tools/resources/prompts
    server.go                       # HTTP ServeMux + SSE transport (GET /sse, POST /messages)
    session.go                      # Per-client state; owns Registry ref; dispatches JSON-RPC

internal/mcp/tools/
    retain.go                       # iclude_retain
    recall.go                       # iclude_recall
    reflect.go                      # iclude_reflect
    ingest_conversation.go          # iclude_ingest_conversation
    timeline.go                     # iclude_timeline

internal/mcp/resources/
    recent.go                       # iclude://context/recent  (identity-scoped)
    session_context.go              # iclude://context/session/{session_id}

internal/mcp/prompts/
    memory_context.go               # memory_context system prompt template

testing/mcp/
    integration_test.go             # Full handshake: initialize → tools/list → tools/call (httptest.Server)
    server_test.go                  # SSE connection, JSON-RPC framing unit tests
    tools_test.go                   # Table-driven tool handler tests (mocked deps)
    resources_test.go               # Resource handler tests (mocked Retriever)
    registry_test.go                # Registry dispatch tests
```

**20 files, ~2,200 lines estimated.**

---

## 6. MCP Components

### 6.1 Tools (5)

| Tool | Wraps | Key Args |
|------|-------|----------|
| `iclude_retain` | `memory.Manager.Create()` | content, kind, scope, tags, auto_extract, source_type |
| `iclude_recall` | `search.Retriever.Retrieve()` | query, scope, limit, filters (nested object), mmr_enabled |
| `iclude_reflect` | `reflect.ReflectEngine.Reflect()` | question, scope, max_rounds |
| `iclude_ingest_conversation` | `memory.Manager.IngestConversation()` | messages[], provider, external_id, scope, context_id |
| `iclude_timeline` | `search.Retriever.Timeline()` | scope, after (ISO8601), before (ISO8601), limit |

**`iclude_recall` filters schema** — `filters` is a nested JSON object:
```json
{
  "filters": {
    "kind": "fact",
    "tags": ["project-x"],
    "min_strength": 0.3,
    "happened_after": "2026-01-01T00:00:00Z",
    "include_expired": false
  }
}
```
Maps directly to `model.SearchFilters` struct fields.

**`iclude_ingest_conversation` args** — maps to `model.IngestConversationRequest`:
- `messages` (required): `[{"role":"user","content":"..."},{"role":"assistant","content":"..."}]`
- `provider` (optional): `"claude"` | `"openai"` | `"generic"` (default: `"generic"`)
- `external_id` (optional): external thread ID for deduplication
- `scope` (optional): namespace scope
- `context_id` (optional): attach to existing context node (reuse existing context)

Each tool:
- Implements `mcp.ToolHandler` interface
- Holds injected dependencies via constructor
- Independently testable with mocked deps
- Returns `*mcp.ToolResult` (text content block or error block)

### 6.2 Resources (2)

| URI | Returns | Identity Scoped |
|-----|---------|-----------------|
| `iclude://context/recent` | 20 most recent memories via Timeline | Yes — uses `mcp.IdentityFromContext(ctx)` |
| `iclude://context/session/{session_id}` | Memories for a specific context node | Yes — same identity scoping |

Both resources resolve identity from `ctx` via `mcp.IdentityFromContext(ctx)`. If no identity is present (session not initialized), return an empty result rather than panic.

Resources implement `mcp.ResourceHandler` with a `Match(uri string) bool` method for URI routing.

### 6.3 Prompts (1)

| Prompt | Args | Purpose |
|--------|------|---------|
| `memory_context` | query (optional), scope (optional), limit (optional, default 10) | System prompt template that injects retrieved memories. Used by clients at session start for automatic context pre-loading. |

---

## 7. Key Types and Interfaces

### Definition Structs (protocol.go)

```go
// ToolDefinition MCP tools/list 响应项 / MCP tools/list response item
type ToolDefinition struct {
    Name        string          `json:"name"`
    Description string          `json:"description"`
    InputSchema json.RawMessage `json:"inputSchema"` // full JSON Schema object
}

// ResourceDefinition MCP resources/list 响应项 / MCP resources/list response item
type ResourceDefinition struct {
    URI         string `json:"uri"`
    Name        string `json:"name"`
    Description string `json:"description,omitempty"`
    MimeType    string `json:"mimeType,omitempty"` // "application/json"
}

// PromptDefinition MCP prompts/list 响应项 / MCP prompts/list response item
type PromptDefinition struct {
    Name        string           `json:"name"`
    Description string           `json:"description,omitempty"`
    Arguments   []PromptArgument `json:"arguments,omitempty"`
}

// PromptArgument 提示模板参数定义 / Prompt template argument definition
type PromptArgument struct {
    Name        string `json:"name"`
    Description string `json:"description,omitempty"`
    Required    bool   `json:"required,omitempty"`
}

// ContentBlock 内容块（text 类型）/ Content block
type ContentBlock struct {
    Type string `json:"type"` // "text"
    Text string `json:"text,omitempty"`
}

// ToolResult 工具调用结果 / Tool call result
type ToolResult struct {
    Content []ContentBlock `json:"content"`
    IsError bool           `json:"isError,omitempty"`
}

// PromptResult 提示模板渲染结果 / Prompt template render result
type PromptResult struct {
    Description string          `json:"description,omitempty"`
    Messages    []PromptMessage `json:"messages"`
}

// PromptMessage 提示模板消息 / Prompt message
type PromptMessage struct {
    Role    string       `json:"role"` // "user" | "assistant"
    Content ContentBlock `json:"content"`
}

// Helper constructors
func TextResult(text string) *ToolResult
func ErrorResult(msg string) *ToolResult
```

### Handler Interfaces (handler.go)

```go
type ToolHandler interface {
    Definition() ToolDefinition
    Execute(ctx context.Context, arguments json.RawMessage) (*ToolResult, error)
}

type ResourceHandler interface {
    Definition() ResourceDefinition
    Match(uri string) bool
    Read(ctx context.Context, uri string) ([]ContentBlock, error)
}

type PromptHandler interface {
    Definition() PromptDefinition
    Get(ctx context.Context, arguments map[string]string) (*PromptResult, error)
}
```

### Registry (registry.go)

```go
func NewRegistry() *Registry
func (r *Registry) RegisterTool(h ToolHandler)
func (r *Registry) RegisterResource(h ResourceHandler)
func (r *Registry) RegisterPrompt(h PromptHandler)
func (r *Registry) ListTools() []ToolDefinition
func (r *Registry) ListResources() []ResourceDefinition
func (r *Registry) ListPrompts() []PromptDefinition
func (r *Registry) CallTool(ctx context.Context, name string, args json.RawMessage) (*ToolResult, error)
func (r *Registry) ReadResource(ctx context.Context, uri string) ([]ContentBlock, error)
func (r *Registry) GetPrompt(ctx context.Context, name string, args map[string]string) (*PromptResult, error)
```

### Identity Context (session.go)

```go
// identityCtxKey 私有类型防止 context key 碰撞 / Unexported type prevents context key collisions
type identityCtxKey struct{}

// WithIdentity 注入身份到 context / Inject identity into context
func WithIdentity(ctx context.Context, id *model.Identity) context.Context

// IdentityFromContext 从 context 获取身份，不存在返回 nil / Get identity from context, nil if absent
func IdentityFromContext(ctx context.Context) *model.Identity
```

The unexported struct type `identityCtxKey{}` is the Go-idiomatic approach. It prevents collisions with any other package using string keys (including `internal/api`'s `"iclude_identity"` Gin key).

### Session Responsibilities (session.go)

`Session` owns the `*Registry` reference and is responsible for:
- Storing session identity (`*model.Identity`)
- Receiving `JSONRPCRequest` from `server.go` via `HandleRequest(ctx, req)`
- Dispatching to `registry.CallTool` / `registry.ReadResource` / `registry.GetPrompt`
- Sending serialized `JSONRPCResponse` to `outCh chan []byte`
- Wrapping `ctx` with `WithIdentity()` before every dispatch call

`Server` (server.go) is responsible for:
- HTTP mux setup (`GET /sse`, `POST /messages`)
- Session creation, storage (`sync.Map`), and cleanup
- SSE write loop (reads from `session.outCh`, writes SSE frames)
- Receiving HTTP request body on `POST /messages`, parsing JSON-RPC, calling `session.HandleRequest()`

---

## 8. MCP Handshake & Session Lifecycle

### SSE Connection Sequence

1. Client sends `GET /sse` with `Accept: text/event-stream`
2. Server generates session ID (UUID v4), creates `Session` with `Registry` + config identity, stores in `sync.Map`
3. Server sends: `event: endpoint\ndata: /messages?session={id}\n\n`
4. Server enters SSE write loop: reads from `session.outCh` (cap 64), writes `event: message\ndata: {json}\n\n`, calls `Flusher.Flush()`
5. SSE heartbeat: server sends `event: ping\ndata: {}\n\n` every 30s to detect dead connections
6. On context cancellation (client disconnect): remove session from map, call `session.Close()`

### JSON-RPC Request Sequence

1. Client sends `POST /messages?session={id}` with JSON-RPC body (`Content-Type: application/json`)
2. Server looks up session by `session` query param; returns `404` if not found
3. Server calls `session.HandleRequest(ctx, &req)`
4. Session wraps `ctx` with identity, dispatches to registry, serializes response, sends to `outCh`
5. Server returns `202 Accepted` (actual response arrives via SSE stream)

### Initialize Handshake Response

```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "protocolVersion": "2024-11-05",
    "capabilities": {
      "tools":     { "listChanged": false },
      "resources": { "subscribe": false, "listChanged": false },
      "prompts":   { "listChanged": false }
    },
    "serverInfo": { "name": "iclude-mcp", "version": "1.0.0" }
  }
}
```

`protocolVersion: "2024-11-05"` matches the SSE transport used. Claude CLI `"type": "sse"` config expects this version.

---

## 9. Identity Model

MCP clients do not send Bearer tokens. Identity is resolved in two layers:

1. **Config default** (primary): `cfg.MCP.DefaultTeamID` + `cfg.MCP.DefaultOwnerID` set at session creation. Covers all single-user local deployments.
2. **Init-time override** (optional): if the `initialize` JSON-RPC request contains `params.clientInfo.metadata.team_id` and `params.clientInfo.metadata.owner_id`, the session stores these and uses them instead of the config defaults.

Identity is propagated via `context.Context` using the unexported `identityCtxKey{}` type (see Section 7). All tool/resource handlers call `mcp.IdentityFromContext(ctx)` to resolve the current user.

---

## 10. Hybrid Context Management Flow

```
Session Start
    └─ Client reads iclude://context/recent resource (identity-scoped)
    └─ Injects recent memories into system prompt

Each User Turn
    └─ Claude calls iclude_recall(query=user_message, scope=...)
    └─ Retrieved memories used as context for response

After Claude Response
    └─ Claude calls iclude_retain(content=exchange_summary,
                                  kind="conversation",
                                  source_type="conversation",
                                  auto_extract=true)
    └─ Async entity extraction runs in background

Deep Reasoning (on demand)
    └─ Claude calls iclude_reflect(question=...) for multi-hop questions
```

The `memory_context` prompt template provides the system instructions that Claude follows. Clients fetch it via `prompts/get` at session start and inject it as the system message.

---

## 11. Graceful Shutdown

Shutdown sequence on SIGINT/SIGTERM (matches `cmd/server/main.go` established pattern):

1. **Scheduler stop first**: `schedCancel()` + `sched.Wait(10s)` — ensures access-flush goroutine completes
2. **`http.Server.Shutdown(shutdownCtx)`** with 10s timeout — stops accepting new connections; waits for in-flight `POST /messages` handlers
3. **SSE goroutines exit**: detect `shutdownCtx` cancellation, exit write loops, close response writers
4. **Session cleanup**: `Server` iterates `sync.Map`, calls `session.Close()` on each (closes `outCh`, signals `done`)
5. **`stores.Close()`**: closes SQLite connection pool

In-flight tool calls propagate context cancellation via standard Go context. Tools calling LLM (reflect) respect `reflect.round_timeout` independently.

---

## 12. Config Changes

### config.yaml addition

```yaml
mcp:
  enabled: false                   # 默认关闭，手动开启 / Disabled by default, opt-in
  port: 8081
  default_team_id: "default"
  default_owner_id: "mcp-user"
```

### internal/config/config.go addition

```go
// MCPConfig MCP 服务器配置 / MCP server configuration
type MCPConfig struct {
    Enabled        bool   `mapstructure:"enabled"`
    Port           int    `mapstructure:"port"`
    DefaultTeamID  string `mapstructure:"default_team_id"`
    DefaultOwnerID string `mapstructure:"default_owner_id"`
}
```

Viper defaults: `mcp.enabled=false`, `mcp.port=8081`, `mcp.default_team_id="default"`, `mcp.default_owner_id="mcp-user"`.

The `mcp.enabled` flag has no effect on `cmd/mcp/main.go` (that binary always starts the MCP server); it is only meaningful if `cmd/server/main.go` is extended in a future task to optionally co-host the MCP server in the same process.

---

## 13. Claude CLI Integration

After starting `iclude mcp`, add to `~/.claude/settings.json`:

```json
{
  "mcpServers": {
    "iclude": {
      "type": "sse",
      "url": "http://localhost:8081/sse"
    }
  }
}
```

Claude CLI automatically discovers the 5 tools, 2 resources, and 1 prompt. The `memory_context` prompt can be injected at session start via `/prompt iclude:memory_context`.

---

## 14. Risk Register

| Risk | Likelihood | Mitigation |
|------|-----------|-----------|
| SSE connection drops silently | Medium | 30s ping heartbeat; client reconnects to `GET /sse` |
| SQLite WAL contention (REST + MCP separate processes) | Low | WAL mode + `busy_timeout=5000ms`; same patterns as existing dual-process setup |
| MCP protocol version drift | Low | `protocol.go` isolated; tool handlers unaffected by transport changes; easy to add 2025-03-26 Streamable HTTP endpoint later |
| Tool execution timeout (LLM in reflect) | Medium | Context propagation with `reflect.round_timeout`; SSE loop not blocked |
| Identity spoofing via init params | Low | Acceptable for local deployment; JWT auth added in Phase 3c |
| `bootstrap.Init()` refactor breaks REST API | Medium | `cmd/server/main.go` refactored to call `bootstrap.Init()` but observable behaviour unchanged; covered by existing integration tests |

---

## 15. Success Criteria

- [ ] Claude CLI can connect via `http://localhost:8081/sse` with `"type": "sse"` config
- [ ] All 5 tools callable and return correct results
- [ ] Both resources readable and identity-scoped
- [ ] `memory_context` prompt injects memories into Claude's context
- [ ] Graceful shutdown completes within 10s
- [ ] `testing/mcp/integration_test.go` drives full initialize → tools/list → tools/call sequence against `httptest.Server`
- [ ] All new handlers have table-driven unit tests with mocked dependencies
- [ ] Zero new third-party dependencies in `go.mod`
- [ ] `go vet ./...` passes clean
- [ ] `cmd/server/main.go` refactored to use `bootstrap.Init()` with identical observable behaviour
