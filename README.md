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
  <a href="https://github.com/MemeryGit/LocalMem/actions">
    <img src="https://github.com/MemeryGit/LocalMem/actions/workflows/release.yml/badge.svg" alt="CI">
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
  <a href="#how-it-works">How It Works</a> •
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
- **Document Ingestion** — Upload → parse (Docling / Tika fallback) → chunk (Markdown-aware + recursive) → embed → store pipeline
- **MCP Server** — Model Context Protocol support (SSE transport) for seamless integration with AI coding assistants
- **CJK Full-Text Search** — Pluggable FTS5 tokenizer supporting Jieba, Simple CJK, and Noop modes
- **Autonomous Maintenance** — Heartbeat engine for decay audit, orphan cleanup, and contradiction detection

---

## How It Works

### Architecture

IClude follows a layered Go architecture (`cmd/` + `internal/` + `pkg/`):

```
cmd/server/       → HTTP API server (Gin, port 8080)
cmd/mcp/          → MCP server (SSE transport, port 8081)
internal/store/   → Storage interfaces (8) + SQLite/Qdrant implementations
internal/memory/  → Manager (CRUD + dual-write), ContextManager, GraphManager
internal/search/  → Retriever (3-mode: SQLite/Qdrant/Hybrid) + RRF fusion
internal/reflect/ → Multi-round LLM reasoning engine
internal/document/→ Document processor (upload → chunk → embed → store)
pkg/              → Reusable packages (Qdrant client, tokenizer, SQL builder)
```

### Storage Modes

Configured via `config.yaml`:

| Mode | Description |
|------|-------------|
| **SQLite only** | Structured queries + FTS5 full-text search (BM25 weighted) |
| **Qdrant only** | Vector semantic search |
| **Hybrid** | Both enabled — results merged via weighted RRF (k=60) |

Best-effort dual-write: SQLite is primary; Qdrant failure is logged but does not roll back SQLite.

### Three-Way Retrieval

The `search.Retriever` merges up to 3 channels via weighted RRF:

1. **SQLite FTS5** (BM25) — weight configurable via `retrieval.fts_weight`
2. **Qdrant vector** — weight via `retrieval.qdrant_weight`
3. **Graph association** — weight via `retrieval.graph_weight`

Formula: `score = Σ weight × 1/(k + rank + 1)` per channel. Results are then strength-weighted and token-budget-trimmed.

---

## API Endpoints

All endpoints under `/v1/`:

| Group | Description |
|-------|-------------|
| `/v1/memories` | CRUD + soft-delete/restore + reinforce + tag associations |
| `/v1/retrieve` | Three-way hybrid retrieval with weighted RRF |
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

Connect AI assistants (Claude Code, Cursor, etc.) via MCP SSE transport:

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
| `recall_memories` | Search and retrieve relevant memories |
| `save_memory` | Store a new memory |
| `reflect` | Multi-round LLM reasoning over memories |
| `ingest_conversation` | Batch ingest conversation turns |
| `timeline` | Chronological memory timeline |

**MCP Prompt:** `memory_context` — inject relevant memory context into conversations.

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

## Development

```bash
go fmt ./...                 # Format code
go vet ./...                 # Static analysis
go test ./testing/...        # Run all tests
go test -run TestName ./testing/...  # Run a single test
```

### Project Structure

```
cmd/                → Entry points (server, mcp, cli, test-dashboard)
internal/
  ├── api/          → Gin HTTP handlers, router, middleware
  ├── config/       → Viper + godotenv config
  ├── logger/       → Zap structured logging
  ├── model/        → Data models (Memory: 31 fields), DTOs, sentinel errors
  ├── store/        → Storage interfaces (8) + implementations
  ├── embed/        → Embedding adapters (OpenAI, Ollama)
  ├── memory/       → Memory manager, context, graph, lifecycle
  ├── search/       → Retriever + RRF fusion
  ├── llm/          → LLM chat abstraction (OpenAI-compatible)
  ├── reflect/      → Reflect engine (multi-round reasoning)
  ├── document/     → Document processor pipeline
  ├── heartbeat/    → Autonomous inspection engine
  └── scheduler/    → In-process goroutine scheduler
pkg/
  ├── qdrant/       → Reusable Qdrant HTTP client (stdlib only)
  ├── tokenizer/    → Pluggable FTS5 tokenizer
  ├── sqlbuilder/   → Lightweight SQL WHERE builder
  └── testreport/   → Test report HTML generator
sdks/python/        → Python SDK client
deploy/             → Docker + docker-compose configs
testing/            → All tests (mirroring source structure)
tools/              → Jieba server, test dashboard UI
```

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
