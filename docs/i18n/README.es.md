<h1 align="center">
  <br>
  LocalMem
  <br>
</h1>

<h4 align="center">Sistema de memoria híbrido local-first para aplicaciones de IA.</h4>

<p align="center">
  <a href="../../README.md">🇺🇸 English</a> •
  <a href="README.zh.md">🇨🇳 中文</a> •
  <a href="README.ja.md">🇯🇵 日本語</a> •
  <a href="README.ko.md">🇰🇷 한국어</a> •
  <a href="README.de.md">🇩🇪 Deutsch</a> •
  <a href="README.fr.md">🇫🇷 Français</a> •
  <a href="README.ru.md">🇷🇺 Русский</a> •
  <a href="README.pt.md">🇵🇹 Português</a> •
  <a href="README.ar.md">🇸🇦 العربية</a>
</p>

<p align="center">
  <a href="../../LICENSE">
    <img src="https://img.shields.io/badge/License-MIT-blue.svg" alt="License">
  </a>
  <a href="https://github.com/MemeryGit/LocalMem/releases">
    <img src="https://img.shields.io/github/v/release/MemeryGit/LocalMem?color=green" alt="Release">
  </a>
  <a href="../../go.mod">
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

<p align="center">
  LocalMem combina SQLite (búsqueda estructurada + texto completo) con Qdrant (búsqueda semántica vectorial) para proporcionar recuperación híbrida de tres vías, razonamiento LLM multi-ronda, extracción de grafos de conocimiento e ingesta de documentos — todo en un único binario Go.
</p>

---

## Inicio Rápido

```bash
git clone https://github.com/MemeryGit/LocalMem.git
cd LocalMem
go mod download
cp config/config.yaml ./config.yaml
go run ./cmd/server/       # Servidor API (puerto 8080)
go run ./cmd/mcp/          # Servidor MCP (puerto 8081, opcional)
```

### Requisitos

- **Go** 1.25+
- **Qdrant** (opcional, para búsqueda vectorial)
- **Docling / Apache Tika** (opcional, para análisis de documentos)

---

## Características Principales

- **Recuperación Híbrida** — Búsqueda de tres vías: SQLite FTS5 (BM25) + Qdrant vectorial + grafo de conocimiento, fusionados mediante RRF (k=60)
- **Ciclo de Vida de Memoria** — Niveles de retención (`permanent` / `long_term` / `standard` / `short_term` / `ephemeral`) con tasas de decaimiento configurables
- **Razonamiento Multi-Ronda** — Motor Reflect para razonamiento LLM iterativo sobre memorias recuperadas
- **Grafo de Conocimiento** — Extracción automática de entidades/relaciones mediante LLM
- **Ingesta de Documentos** — Pipeline: subir → analizar → fragmentar → embeddings → almacenar
- **Servidor MCP** — Soporte Model Context Protocol (transporte SSE)
- **Búsqueda CJK** — Tokenizador FTS5 conectable (Jieba, Simple CJK, Noop)

---

## Endpoints API

Todos bajo `/v1/`:

| Grupo | Descripción |
|-------|-------------|
| `/v1/memories` | CRUD + eliminación suave/restauración + refuerzo |
| `/v1/retrieve` | Recuperación híbrida de tres vías |
| `/v1/reflect` | Razonamiento LLM multi-ronda |
| `/v1/conversations` | Ingesta de conversaciones por lotes |
| `/v1/entities` | Grafo de conocimiento |
| `/v1/documents` | Subida/procesamiento de documentos |

---

## Integración MCP

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

**Herramientas MCP disponibles:** `recall_memories`, `save_memory`, `reflect`, `ingest_conversation`, `timeline`

---

## Licencia

**Licencia MIT** — Copyright (c) 2026 MemeryGit.

Consulte [LICENSE](../../LICENSE) para más detalles.

---

<p align="center">
  <b>Construido con Go</b> • <b>Potenciado por SQLite + Qdrant</b> • <b>Compatible con MCP</b>
</p>
