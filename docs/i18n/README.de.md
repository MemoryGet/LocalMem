<h1 align="center">
  <br>
  LocalMem
  <br>
</h1>

<h4 align="center">Local-First Hybrid-Speichersystem für KI-Anwendungen.</h4>

<p align="center">
  <a href="../../README.md">🇺🇸 English</a> •
  <a href="README.zh.md">🇨🇳 中文</a> •
  <a href="README.ja.md">🇯🇵 日本語</a> •
  <a href="README.ko.md">🇰🇷 한국어</a> •
  <a href="README.es.md">🇪🇸 Español</a> •
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
</p>

<p align="center">
  IClude kombiniert SQLite (strukturierte + Volltextsuche) mit Qdrant (vektorbasierte semantische Suche) und bietet Drei-Wege-Hybridsuche, mehrstufiges LLM-Reasoning, Wissensgraph-Extraktion und Dokumentenaufnahme — alles in einer einzigen Go-Binary.
</p>

---

## Schnellstart

```bash
git clone https://github.com/MemeryGit/LocalMem.git
cd LocalMem
go mod download
cp config/config.yaml ./config.yaml
go run ./cmd/server/       # API-Server (Port 8080)
go run ./cmd/mcp/          # MCP-Server (Port 8081, optional)
```

### Voraussetzungen

- **Go** 1.25+
- **Qdrant** (optional, für Vektorsuche)
- **Docling / Apache Tika** (optional, für Dokumentenanalyse)

---

## Hauptfunktionen

- **Hybridsuche** — Drei-Wege-Suche: SQLite FTS5 (BM25) + Qdrant-Vektor + Wissensgraph, fusioniert über RRF (k=60)
- **Speicher-Lebenszyklus** — Aufbewahrungsstufen (`permanent` / `long_term` / `standard` / `short_term` / `ephemeral`) mit konfigurierbaren Verfallsraten
- **Mehrstufiges Reasoning** — Reflect Engine für iteratives LLM-Reasoning über abgerufene Erinnerungen
- **Wissensgraph** — Automatische Entitäts-/Beziehungsextraktion via LLM
- **Dokumentenaufnahme** — Pipeline: Upload → Analyse → Chunking → Embedding → Speicherung
- **MCP-Server** — Model Context Protocol Unterstützung (SSE-Transport)
- **CJK-Volltextsuche** — Erweiterbarer FTS5-Tokenizer

---

## API-Endpunkte

Alle unter `/v1/`:

| Gruppe | Beschreibung |
|--------|-------------|
| `/v1/memories` | CRUD + Soft-Delete/Wiederherstellung |
| `/v1/retrieve` | Drei-Wege-Hybridsuche |
| `/v1/reflect` | Mehrstufiges LLM-Reasoning |
| `/v1/conversations` | Batch-Konversationsaufnahme |
| `/v1/entities` | Wissensgraph |
| `/v1/documents` | Dokument-Upload/-Verarbeitung |

---

## MCP-Integration

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

---

## Lizenz

**MIT-Lizenz** — Copyright (c) 2026 MemeryGit.

Siehe [LICENSE](../../LICENSE) für Details.

---

<p align="center">
  <b>Gebaut mit Go</b> • <b>Betrieben von SQLite + Qdrant</b> • <b>MCP-fähig</b>
</p>
