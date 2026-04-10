<h1 align="center">
  <br>
  LocalMem
  <br>
</h1>

<h4 align="center">Local-first hybrid memory system built for AI applications.</h4>

<p align="center">
  <a href="docs/i18n/README.zh.md">🇨🇳 中文</a> •
  <a href="docs/i18n/README.ja.md">🇯🇵 日本語</a> •
  <a href="docs/i18n/README.ko.md">🇰🇷 한국어</a> •
  <a href="docs/i18n/README.es.md">🇪🇸 Español</a> •
  <a href="docs/i18n/README.de.md">🇩🇪 Deutsch</a> •
  <a href="docs/i18n/README.fr.md">🇫🇷 Français</a> •
  <a href="docs/i18n/README.ru.md">🇷🇺 Русский</a> •
  <a href="docs/i18n/README.pt.md">🇵🇹 Português</a> •
  <a href="docs/i18n/README.ar.md">🇸🇦 العربية</a>
</p>

<p align="center">
  <a href="LICENSE">
    <img src="https://img.shields.io/badge/License-MIT-blue.svg" alt="License">
  </a>
  <a href="https://github.com/MemeryGit/LocalMem/releases">
    <img src="https://img.shields.io/github/v/release/MemeryGit/LocalMem?color=green" alt="Release">
  </a>
  <a href="go.mod">
    <img src="https://img.shields.io/badge/Go-1.25+-00ADD8.svg?logo=go" alt="Go Version">
  </a>
  <a href="https://goreportcard.com/report/github.com/MemeryGit/LocalMem">
    <img src="https://goreportcard.com/badge/github.com/MemeryGit/LocalMem" alt="Go Report Card">
  </a>
  <a href="https://github.com/MemeryGit/LocalMem/actions/workflows/release.yml">
    <img src="https://img.shields.io/github/actions/workflow/status/MemeryGit/LocalMem/release.yml?label=CI&logo=github" alt="CI">
  </a>
  <a href="https://discord.gg/eG87YHjU">
    <img src="https://img.shields.io/discord/1356309498498297856?color=5865F2&logo=discord&logoColor=white&label=Discord" alt="Discord">
  </a>
</p>

<br>

<table align="center">
  <tr>
    <td align="center">
      <a href="https://star-history.com/#MemeryGit/LocalMem&Date">
        <picture>
          <source media="(prefers-color-scheme: dark)" srcset="https://api.star-history.com/svg?repos=MemeryGit/LocalMem&type=Date&theme=dark" />
          <source media="(prefers-color-scheme: light)" srcset="https://api.star-history.com/svg?repos=MemeryGit/LocalMem&type=Date" />
          <img alt="Star History Chart" src="https://api.star-history.com/svg?repos=MemeryGit/LocalMem&type=Date" width="500" />
        </picture>
      </a>
    </td>
    <td align="center">
      <a href="https://github.com/MemeryGit/LocalMem/pulse">
        <img src="https://repobeats.axiom.co/api/embed/efa829afb660f748e19485075e2b6658193d5c07.svg" alt="Repobeats analytics" width="500" />
      </a>
    </td>
  </tr>
</table>

<p align="center">
  <a href="#quick-start">Quick Start</a> •
  <a href="#key-features">Key Features</a> •
  <a href="#system-architecture">System Architecture</a> •
  <a href="#retrieval-pipeline">Retrieval Pipeline</a> •
  <a href="#data-isolation--multi-tenancy">Data Isolation</a> •
  <a href="#quality--speed-optimizations">Quality & Speed</a> •
  <a href="#api-endpoints">API Endpoints</a> •
  <a href="#mcp-integration">MCP Integration</a> •
  <a href="#development">Development</a> •
  <a href="#license">License</a>
</p>

<p align="center">
  IClude combines SQLite (structured + full-text search) with Qdrant (vector semantic search) to provide three-way hybrid retrieval, multi-round LLM reasoning, knowledge graph extraction, and document ingestion — all in a single Go binary.
</p>

---

## Quick Start

```bash
# Clone the repository
git clone https://github.com/MemeryGit/LocalMem.git
cd LocalMem

# Install dependencies
go mod download

# Configure
cp config/config.yaml ./config.yaml

# Run the API server (port 8080)
go run ./cmd/server/

# Run the MCP server (port 8081, optional)
go run ./cmd/mcp/
```

### Docker Deployment

```bash
docker-compose -f deploy/docker-compose.yml up
```

### Requirements

- **Go** 1.25+
- **Qdrant** (optional, for vector search)
- **Docling / Apache Tika** (optional, for document parsing)
- **Jieba Server** (optional, for CJK full-text tokenization)

---

## Key Features

- **Hybrid Retrieval** — Three-way search combining SQLite FTS5 (BM25), Qdrant vector similarity, and knowledge graph association, merged via Reciprocal Rank Fusion (RRF, k=60)
- **Memory Lifecycle** — Retention tiers (`permanent` / `long_term` / `standard` / `short_term` / `ephemeral`) with configurable decay rates, soft delete, and reinforce mechanics
- **Multi-Round Reasoning** — Reflect Engine performs iterative LLM reasoning over retrieved memories with auto-save conclusions
- **Knowledge Graph** — Automatic entity/relation extraction from memory content via LLM, with graph-based association retrieval
- **Document Ingestion** — Upload -> parse (Docling / Tika fallback) -> chunk (Markdown-aware + recursive) -> embed -> store pipeline
- **Multi-Tenant Data Isolation** — Team + Owner + Scope + Visibility four-layer isolation prevents knowledge contamination between users
- **MCP Server** — Model Context Protocol support (stdio + SSE transport) for seamless integration with AI coding assistants
- **CJK Full-Text Search** — Pluggable FTS5 tokenizer supporting Jieba, Simple CJK, Gse, and Noop modes
- **Autonomous Maintenance** — Heartbeat engine for decay audit, orphan cleanup, and contradiction detection

---

## System Architecture

### High-Level Framework

```
                        ┌──────────────────────────────────────────────┐
                        │               Client Layer                   │
                        │  Claude Code / Cursor / SDK / REST Client    │
                        └──────────┬──────────────────┬────────────────┘
                                   │                  │
                          HTTP REST API          MCP Protocol
                          (Gin :8080)         (stdio / SSE :8081)
                                   │                  │
┌──────────────────────────────────┴──────────────────┴──────────────────────┐
│                           API Layer (internal/api/)                         │
│  ┌──────────┐  ┌──────────────┐  ┌─────────────┐  ┌────────────────────┐  │
│  │   Auth   │→ │   Identity   │→ │   Router    │→ │     Handlers       │  │
│  │Middleware │  │  Middleware   │  │  (groups)   │  │  (withIdentity)    │  │
│  └──────────┘  └──────────────┘  └─────────────┘  └────────────────────┘  │
│  API Key→TeamID  X-User-ID→OwnerID                                        │
└───────────────────────────────┬────────────────────────────────────────────┘
                                │
┌───────────────────────────────┴────────────────────────────────────────────┐
│                         Business Layer                                      │
│                                                                             │
│  ┌────────────────┐  ┌──────────────┐  ┌─────────────┐  ┌──────────────┐  │
│  │ Memory Manager │  │   Retriever  │  │   Reflect   │  │   Document   │  │
│  │ (CRUD+Dedup+   │  │  (Pipeline   │  │   Engine    │  │  Processor   │  │
│  │  DualWrite)    │  │   Executor)  │  │ (Multi-round│  │ (Upload→     │  │
│  ├────────────────┤  ├──────────────┤  │  LLM loop)  │  │  Chunk→      │  │
│  │  Extractor     │  │  6 Pipelines │  └─────────────┘  │  Embed→      │  │
│  │ (LLM entity    │  │  17 Stages   │                   │  Store)      │  │
│  │  extraction)   │  └──────────────┘                   └──────────────┘  │
│  ├────────────────┤                                                        │
│  │ GraphManager   │  ┌──────────────┐  ┌──────────────┐                   │
│  │ ContextManager │  │  Heartbeat   │  │  Scheduler   │                   │
│  │ AccessTracker  │  │ (Decay/Orphan│  │ (Cron tasks) │                   │
│  └────────────────┘  │ /Contradict) │  └──────────────┘                   │
│                      └──────────────┘                                      │
└───────────────────────────────┬────────────────────────────────────────────┘
                                │
┌───────────────────────────────┴────────────────────────────────────────────┐
│                          Store Layer (internal/store/)                       │
│                                                                             │
│  8 Interfaces:  MemoryStore | VectorStore | ContextStore | TagStore         │
│                 GraphStore  | DocumentStore | DerivationStore | QueueStore   │
│                                                                             │
│  ┌──────────────────────────────────┐    ┌──────────────────────────────┐  │
│  │         SQLite Backend           │    │       Qdrant Backend         │  │
│  │  ┌──────────┐ ┌──────────────┐  │    │  ┌──────────────────────┐   │  │
│  │  │ memories │ │ memories_fts │  │    │  │   Vector Collection  │   │  │
│  │  │ (35 cols)│ │(BM25 3-col)  │  │    │  │   (cosine, HNSW)    │   │  │
│  │  ├──────────┤ ├──────────────┤  │    │  └──────────────────────┘   │  │
│  │  │ entities │ │entity_rels   │  │    └──────────────────────────────┘  │
│  │  │ contexts │ │tags          │  │                                      │
│  │  │ documents│ │derivations   │  │    ┌──────────────────────────────┐  │
│  │  └──────────┘ └──────────────┘  │    │      Embedding Layer         │  │
│  │  WAL mode | FTS5 | mmap 256MB   │    │  OpenAI / Ollama adapters    │  │
│  └──────────────────────────────────┘    │  LRU Cache (double-check)   │  │
│                                          └──────────────────────────────┘  │
│  ┌──────────────────────────────────────────────────────────────────────┐  │
│  │                   LLM Provider (internal/llm/)                       │  │
│  │  OpenAI-compatible: DeepSeek / Ollama / OpenAI / Claude              │  │
│  │  Consumed by: Extractor, Reflect, Search Strategy, Consolidation     │  │
│  └──────────────────────────────────────────────────────────────────────┘  │
└────────────────────────────────────────────────────────────────────────────┘
```

### Startup Wiring Order

```
Config → Logger → Embedder → Stores → LLM Provider → GraphManager
  → Extractor → Manager → Retriever → ContextManager → DocProcessor
    → ReflectEngine → Heartbeat → Scheduler → Router
```

Order matters: each component depends on previously constructed ones. Feature gating via nil checks allows graceful degradation when optional components (Qdrant, LLM, etc.) are unavailable.

### Storage Modes

| Mode | Description | Use Case |
|------|-------------|----------|
| **SQLite only** | Structured queries + FTS5 full-text search (BM25 weighted) | Lightweight, zero external deps |
| **Qdrant only** | Vector semantic search (cosine, HNSW) | Pure semantic retrieval |
| **Hybrid** | Both enabled — dual-write, results merged via weighted RRF | Maximum recall, production use |

**Dual-write strategy**: SQLite is the primary store (atomic commitment). Qdrant write is best-effort — failure is logged but does not roll back SQLite. This ensures data durability while tolerating vector service downtime.

---

## Retrieval Pipeline

### Pipeline Architecture

The search system uses a **stage-based pipeline executor** with 6 built-in pipelines and 17 reusable stages. Each pipeline defines an ordered sequence of **stage groups** (parallel or sequential), with automatic **fallback chains** (max depth 3).

```
 Query
   │
   ▼
┌─────────────────────────────────────────────────────────────────┐
│              Pipeline Executor                                   │
│                                                                  │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │  Stage Groups (ordered, parallel or sequential)          │    │
│  │                                                          │    │
│  │  Group 1 (parallel):  [FTS] [Vector] [Graph]            │    │
│  │                           │      │      │               │    │
│  │                           ▼      ▼      ▼               │    │
│  │  Group 2 (sequential): [  Merge (RRF / GraphAware)  ]   │    │
│  │                                  │                       │    │
│  │  Group 3 (sequential): [    Score Filter (>=0.3)    ]   │    │
│  │                                  │                       │    │
│  │  Group 4 (sequential): [  Rerank (Graph/LLM/Overlap)]   │    │
│  └──────────────────────────────────┬──────────────────────┘    │
│                                     │                            │
│  Fallback: if 0 results ──────────► retry with fallback pipeline │
│                                                                  │
│  ┌─────────────────────────────────────────────────────────┐    │
│  │  Post Stages (shared, run exactly once)                  │    │
│  │  [Strength Weight] → [MMR Diversity] → [Core Inject] → [Trim]│
│  └─────────────────────────────────────────────────────────┘    │
└─────────────────────────────────────────────────────────────────┘
   │
   ▼
 Results (scored, deduplicated, token-budget-trimmed)
```

### 6 Built-in Pipelines

| Pipeline | Stages | Fallback | Use Case |
|----------|--------|----------|----------|
| **precision** | parallel(Graph, FTS) -> Merge(GraphAware) -> Filter(0.3) -> RerankGraph | exploration | Exact entity matches, factual Q&A |
| **exploration** | parallel(FTS, Temporal) -> Merge(RRF) -> Filter(0.05) -> RerankOverlap | *(terminal)* | Browsing, timeline-based queries |
| **semantic** | parallel(Vector, FTS) -> Merge(RRF) -> Filter(0.3) -> RerankOverlap | exploration | Meaning-based similarity search |
| **association** | Graph(depth=3) -> RerankGraph -> Filter(0.2) | precision | "What relates to X?" graph walk |
| **fast** | FTS(limit=10) -> Filter(0.05) | *(terminal)* | Autocomplete, real-time suggestions |
| **full** | parallel(Graph, FTS, Vector) -> Merge(GraphAware) -> Filter(0.3) -> RerankLLM | precision | Maximum quality, complex queries |

### 17 Reusable Stages

| Category | Stage | Description |
|----------|-------|-------------|
| **Source** | `fts` | SQLite FTS5 BM25 search (3-column weighted) |
| | `vector` | Qdrant cosine similarity with min-score threshold |
| | `graph` | Entity graph BFS traversal (configurable depth, fan-out limit 50) |
| | `temporal` | Time-range scoped retrieval |
| | `core` | Inject pinned core memories (fixed high score 2.0) |
| **Merge** | `merge` | RRF or GraphAware fusion: `score = sum(weight / (k + rank + 1))` |
| | `blend` | Shared blend helper for multi-source dedup |
| **Rerank** | `rerank_graph` | Boost by entity co-occurrence strength |
| | `rerank_llm` | LLM-based relevance judgment |
| | `rerank_overlap` | Token overlap between query and content |
| | `rerank_remote` | External reranker API call |
| **Filter** | `filter` | Score threshold gate |
| | `mmr` | Maximal Marginal Relevance for diversity |
| **Post** | `weight` | Strength decay + access frequency boost |
| | `trim` | Token budget enforcement |
| | `circuit_breaker` | Timeout / error rate protection |

### Strength Weighting Formula

Applied in post-processing to every search result:

```
effective_strength = decay_factor * access_boost

where:
  decay_factor   = strength * exp(-decay_rate * hours_since_access)
  access_boost   = 1.0 + alpha * log2(access_count + 1)    (capped at 3.0)
  alpha          = 0.15  (configurable: retrieval.access_alpha)
```

- **Permanent** tier memories bypass decay (decay_rate = 0)
- Minimum floor of 0.05 prevents old memories from vanishing entirely
- Access boost rewards frequently recalled memories (logarithmic curve prevents runaway)

---

## Data Isolation & Multi-Tenancy

### Four-Layer Isolation Model

LocalMem uses a **four-layer isolation model** to prevent knowledge contamination between users, teams, and scopes:

```
Layer 1: TeamID        (macro isolation — from API Key)
  │
  ├── Layer 2: OwnerID     (micro isolation — from X-User-ID header)
  │     │
  │     ├── Layer 3: Scope       (namespace segmentation — user/alice, project/xyz)
  │     │     │
  │     │     └── Layer 4: Visibility  (access control — private/team/public)
  │     │
  │     └── (All queries filtered by visibility rules)
  │
  └── (Team boundary: members see team-visible data only within same TeamID)
```

### Identity Resolution

| Field | Source | Purpose |
|-------|--------|---------|
| `TeamID` | Resolved from API Key via `AuthMiddleware` | Macro team boundary (constant-time comparison) |
| `OwnerID` | Extracted from `X-User-ID` header via `IdentityMiddleware` | Per-user isolation (validated: 128 chars, `[a-zA-Z0-9_@.-]`) |
| `__system__` | Reserved system identity | Internal operations (heartbeat, consolidation) — **anti-spoofing: client `__system__` is converted to `anonymous`** |

### Visibility Rules

Every read operation (Get, List, Search, Timeline, Graph) applies the same SQL visibility filter:

```sql
-- With TeamID:
(visibility = 'public'
 OR (team_id = ? AND visibility = 'team')
 OR (team_id = ? AND visibility = 'private' AND (owner_id = ? OR owner_id = '')))

-- Without TeamID (backward compatibility):
(visibility = 'public'
 OR visibility = 'team'
 OR (visibility = 'private' AND (owner_id = ? OR owner_id = '')))
```

| Visibility | Who Can See | Default For |
|------------|-------------|-------------|
| `private` | Owner only (or team members if owner_id is empty) | `user/*` scope, session data |
| `team` | All members of the same TeamID | `project/*` scope (non-observation) |
| `public` | Everyone | Shared knowledge, public docs |

### Scope-Based Segmentation

Scope format: `{namespace}/{identifier}` (e.g., `user/alice`, `team/eng`, `project/p_a1b2c3`, `session/s_xyz`).

**Critical isolation rule**: Scopes control the *namespace* of knowledge. The following scopes are **never mixed into the global knowledge graph** to prevent cross-user contamination:

| Scope Prefix | Isolation Level | Visible To | Graph Mixing |
|-------------|-----------------|------------|--------------|
| `user/{id}` | Per-user private | Owner only | **Isolated** — entities stay within user boundary |
| `session/{id}` | Per-session ephemeral | Owner only | **Isolated** — destroyed with session |
| `project/{id}` | Project-scoped | Team members | **Project-scoped** — entities shared within project only |
| `team/{id}` | Team-wide | Team members | Shared within team |
| `global` | System-wide | All (visibility dependent) | Shared globally |

### Vector Dedup Respects Isolation

Even deduplication enforces identity boundaries:

```go
// Vector dedup searches with identity for visibility filtering
results, err := vecStore.Search(ctx, embedding, identity, 1)
```

This ensures User A's memory is never considered a "duplicate" of User B's memory, even if content is identical.

### Scope Write Policies

Optional fine-grained write control per scope:

```yaml
# Only alice and bob can write to this project scope
scope: "project/secret_project"
allowed_writers: ["alice", "bob"]
```

`CanWrite()` returns true if the list is empty (unrestricted) or the caller is in the list.

---

## Quality & Speed Optimizations

### Speed Optimizations

#### 1. SQLite Performance Tuning

```
WAL mode            → Concurrent reads during writes
mmap_size=256MB     → Memory-mapped I/O for fast index scans
cache_size=32MB     → In-memory page cache
busy_timeout=5s     → Wait on lock contention instead of failing
temp_store=MEMORY   → Temp tables in RAM
Pool: MaxOpen=5, MaxIdle=2, ConnMaxLife=5min
```

#### 2. FTS5 Full-Text Search

Three-column FTS5 virtual table with BM25 weighting:

```sql
CREATE VIRTUAL TABLE memories_fts USING fts5(
  content,    -- weight 10.0 (primary)
  excerpt,    -- weight 5.0  (one-line abstract)
  summary     -- weight 3.0  (key information)
)
```

- Pluggable tokenizer: `noop` / `simple` (CJK bigram) / `jieba` (HTTP) / `gse` (native Go)
- FTS writes are **in the same transaction** as parent table writes for consistency
- Stopword & synonym filtering support via `PreprocessConfig`

#### 3. Embedding LRU Cache

```
CachedEmbedder decorator:
  ├── Per-item LRU cache (configurable size)
  ├── Double-check locking (handles eviction races)
  ├── Model-name isolation (prevents cross-model hash collision)
  ├── Batch-aware: EmbedBatch() checks cache per item
  └── Hit rate tracking (LogStats for observability)
```

Eliminates redundant API calls for frequently embedded content (dedup, update, re-index).

#### 4. Async Non-Blocking Patterns

| Pattern | Mechanism | Benefit |
|---------|-----------|---------|
| **Access Tracking** | Channel buffer (10,000 cap) + scheduled flush | Memory reads never blocked by DB writes |
| **Entity Extraction** | Enqueued after create, async goroutine | Write latency unaffected by LLM calls |
| **Excerpt Generation** | LLM summarization in background | Response returns immediately |
| **Vector Write** | Best-effort after SQLite commit | Primary write path stays fast |

#### 5. Parallel Stage Execution

Pipeline stages within a parallel group run concurrently via goroutines:

```
parallel(FTS, Vector, Graph):
  ├── goroutine 1: FTS search (SQLite)
  ├── goroutine 2: Vector search (Qdrant)
  └── goroutine 3: Graph traversal (SQLite)
  ──── WaitGroup.Wait() ────
  Merge results
```

Each goroutine gets an independent state clone. Panics are recovered and traced (stage marked as skipped).

#### 6. Graph Fan-Out Limit

Graph BFS traversal caps at **50 visited entities** to prevent exponential explosion on dense graphs. This bounds worst-case latency while covering typical knowledge subgraphs.

### Quality Optimizations

#### 1. Multi-Level Deduplication

```
Write Path:
  │
  ├── P0: Hash Dedup (SHA-256)
  │     Exact content match → reinforce existing memory
  │
  ├── P1: Vector Dedup (cosine similarity)
  │     ≥ 0.95 → near-duplicate, skip creation
  │     ≥ 0.85 → merge candidate (log, allow write)
  │     < 0.85 → unique, proceed
  │
  └── All dedup respects identity isolation
```

#### 2. Three-Level Fallback Parsing (Entity Extraction)

LLM output parsing uses progressive fallback to maximize extraction success:

```
LLM Response
  │
  ├── L1: Direct JSON unmarshal
  │     Success if valid JSON with entities/relations
  │
  ├── L2: Regex extraction
  │     Find JSON object containing "entities" key
  │     Re-attempt unmarshal on extracted fragment
  │
  └── L3: LLM Retry
        Send correction message: "respond with ONLY valid JSON"
        Parse retry response
```

#### 3. Entity Normalization

Prevents duplicate entities ("OpenAI" vs "openai" vs "Open AI"):

1. New entity name queried against existing entities in scope
2. Substring matching for candidates
3. LLM-based judgment with confidence threshold
4. Matched entity reused; unmatched creates new

#### 4. Contradiction Detection (Heartbeat Engine)

Autonomous background job that detects conflicting information:

```
For each entity:
  Get associated memories
  │
  Filter by mid-range similarity (0.5 ~ 0.95)
  │ Too high (≥0.95) = duplicate → skip
  │ Too low  (<0.5)  = unrelated → skip
  │ 0.5~0.95 = potential contradiction
  │
  └── LLM judgment → log contradictions for review
```

Config-gated: `heartbeat.enabled + contradiction_enabled`. Max 50 comparisons per run.

#### 5. Memory Consolidation

Clusters similar memories and synthesizes them into higher-quality permanent records:

```
Select candidates (time-window + random sampling)
  → Agglomerative clustering (vector similarity)
    → LLM summarization per cluster
      → New permanent-tier memory (linked via DerivedFrom)
```

#### 6. Memory Evolution (Episodic → Semantic → Procedural)

Memories evolve through classification tiers:

| Class | Description | Typical Source |
|-------|-------------|----------------|
| `episodic` | Raw observations, events | Conversation ingest, manual notes |
| `semantic` | Consolidated facts, knowledge | Consolidation output, reflect conclusions |
| `procedural` | Actionable patterns, how-to | Repeated successful procedures |

Promotion candidates are marked with `CandidateFor` field for review.

#### 7. Core Memory Injection

Critical memories (marked as `core_candidate`) are **always injected** at the top of search results with a fixed high score (2.0), ensuring essential context is never missed regardless of query relevance.

---

## API Endpoints

All endpoints under `/v1/`:

| Group | Description |
|-------|-------------|
| `/v1/memories` | CRUD + soft-delete/restore + reinforce + tag associations |
| `/v1/retrieve` | Pipeline-based hybrid retrieval with configurable strategy |
| `/v1/timeline` | Chronological memory timeline |
| `/v1/reflect` | Multi-round LLM reasoning over memories |
| `/v1/conversations` | Conversation ingest (batch) + retrieval by context |
| `/v1/contexts` | Hierarchical context tree (materialized path) |
| `/v1/tags` | Tag CRUD + memory-tag associations |
| `/v1/entities` | Knowledge graph entities + relations |
| `/v1/documents` | Document upload / process / list |
| `/v1/memories/:id/extract` | Explicit entity extraction from a memory |
| `/v1/maintenance/cleanup` | Expire / purge operations |

---

## MCP Integration

Connect AI assistants (Claude Code, Cursor, Codex, etc.) via MCP:

**stdio transport** (primary, for Claude Code / Codex):
```json
{
  "mcpServers": {
    "iclude": {
      "command": "iclude-mcp",
      "args": ["--stdio", "--config", "config.yaml"]
    }
  }
}
```

**SSE transport** (HTTP, for web clients):
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

**Available MCP Tools:**

| Tool | Description |
|------|-------------|
| `iclude_retain` | Store a new memory |
| `iclude_recall` | Search and retrieve relevant memories |
| `iclude_scan` | List memories with filters |
| `iclude_fetch` | Get a specific memory by ID |
| `iclude_reflect` | Multi-round LLM reasoning over memories |
| `iclude_timeline` | Chronological memory timeline |
| `iclude_ingest_conversation` | Batch ingest conversation turns |
| `iclude_create_session` | Initialize a new session |

**MCP Prompt:** `memory_context` — inject relevant memory context into conversations.

Session lifecycle: `initialize` handshake required. Star reminder triggers after 50 tool calls per session.

---

## Python SDK

```bash
pip install iclude
```

```python
from iclude import ICludeClient

client = ICludeClient(base_url="http://localhost:8080")
client.save_memory(content="Important context", kind="note")
results = client.retrieve(query="context")
```

---

## Configuration

### Three-Tier Config Templates

| Template | What's Included |
|----------|-----------------|
| **basic** | SQLite-only, simple tokenizer, no external dependencies |
| **standard** | + Jieba tokenizer, knowledge graph, heartbeat, LLM fallback |
| **premium** | + Qdrant vector search, MMR, document ingestion, consolidation, contradiction detection |

Web generator: open `tools/config-generator/index.html` in browser, select edition, customize, download.

### Config Priority

`.env` file -> Viper defaults -> environment variables -> `config.yaml` (searched in `.`, `./config`, `./deploy`)

### Key Config Sections

| Section | Controls |
|---------|----------|
| `storage` | SQLite / Qdrant enable, paths, tokenizer |
| `server` | Port, auth |
| `llm` | Provider (openai/claude/ollama), embedding, fallback chain |
| `extract` | Max entities/relations, type whitelist, timeout, normalize |
| `retrieval` | FTS/Qdrant/graph weights, MMR, pipeline overrides, strategy |
| `reflect` | Max rounds, token budget, timeout, auto-save |
| `scheduler` | Cleanup/flush/consolidation intervals |
| `heartbeat` | Decay audit, orphan cleanup, contradiction detection |
| `document` | Docling/Tika URLs, chunking config |
| `mcp` | Port, default identity, CORS |
| `auth` | API keys, enable/disable |

---

## Development

```bash
go fmt ./...                 # Format code
go vet ./...                 # Static analysis
go test ./testing/...        # Run all tests
go test -run TestName ./testing/...  # Run a single test
go test ./testing/report/ -v -count=1   # Generate HTML test report
```

### Project Structure

```
cmd/                  Entry points (server, mcp)
internal/
  api/                Gin HTTP handlers, router, middleware, identity injection
  config/             Viper + godotenv config (global singleton)
  logger/             Zap structured logging (package-level, no Sugar)
  model/              Data models (Memory: 35 fields), DTOs, sentinel errors
  store/              8 storage interfaces + SQLite/Qdrant implementations
  embed/              Embedding adapters (OpenAI, Ollama) + LRU cache
  memory/             Manager (CRUD + dual-write), Extractor, GraphManager,
                      ContextManager, AccessTracker, dedup, consolidation
  search/             Retriever, Pipeline Executor, 17 stages, RRF fusion
  llm/                LLM chat abstraction (OpenAI-compatible provider)
  reflect/            Reflect engine (multi-round reasoning)
  document/           Document processor pipeline (parse/chunk/embed/store)
  heartbeat/          Autonomous inspection (decay, orphan, contradiction)
  scheduler/          In-process goroutine + ticker scheduler
  mcp/                MCP server (stdio + SSE), tools, resources, prompts
pkg/
  qdrant/             Reusable Qdrant HTTP client (stdlib only)
  tokenizer/          Pluggable FTS5 tokenizer (4 providers)
  sqlbuilder/         Lightweight SQL WHERE builder
  scoring/            Strength weighting + access frequency boost
  hashutil/           Content hashing (SHA-256)
  tokenutil/          Token estimation utilities
sdks/python/          Python SDK client
deploy/               Docker + docker-compose configs
testing/              All tests (mirroring source structure)
tools/                Config generator, Jieba server, test dashboard
```

### Dependency Flow (Acyclic)

```
cmd/server → api → reflect, memory, search, document
                 → store(interfaces) → model, pkg/*
```

Infrastructure (`config`, `logger`) are global singletons. Business layer (`handler`, `manager`, `store`) uses dependency injection.

### Contributing

Contributions are welcome! Please:

1. Fork the repository
2. Create a feature branch
3. Make your changes with tests
4. Submit a Pull Request

---

## License

This project is licensed under the **MIT License**.

Copyright (c) 2026 MemeryGit. All rights reserved.

See the [LICENSE](LICENSE) file for full details.

---

<p align="center">
  <b>Built with Go</b> • <b>Powered by SQLite + Qdrant</b> • <b>MCP Ready</b>
</p>
