<h1 align="center">
  <br>
  IClude
  <br>
</h1>

<h4 align="center">Sistema de memória híbrido local-first para aplicações de IA.</h4>

<p align="center">
  <a href="../../README.md">🇺🇸 English</a> •
  <a href="README.zh.md">🇨🇳 中文</a> •
  <a href="README.ja.md">🇯🇵 日本語</a> •
  <a href="README.ko.md">🇰🇷 한국어</a> •
  <a href="README.es.md">🇪🇸 Español</a> •
  <a href="README.de.md">🇩🇪 Deutsch</a> •
  <a href="README.fr.md">🇫🇷 Français</a> •
  <a href="README.ru.md">🇷🇺 Русский</a> •
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
  IClude combina SQLite (busca estruturada + texto completo) com Qdrant (busca semântica vetorial) para fornecer recuperação híbrida de três vias, raciocínio LLM multi-rodada, extração de grafos de conhecimento e ingestão de documentos — tudo em um único binário Go.
</p>

---

## Início Rápido

```bash
git clone https://github.com/MemeryGit/LocalMem.git
cd LocalMem
go mod download
cp config/config.yaml ./config.yaml
go run ./cmd/server/       # Servidor API (porta 8080)
go run ./cmd/mcp/          # Servidor MCP (porta 8081, opcional)
```

### Requisitos

- **Go** 1.25+
- **Qdrant** (opcional, para busca vetorial)
- **Docling / Apache Tika** (opcional, para análise de documentos)

---

## Funcionalidades Principais

- **Recuperação Híbrida** — Busca de três vias: SQLite FTS5 (BM25) + Qdrant vetorial + grafo de conhecimento, fusionados via RRF (k=60)
- **Ciclo de Vida da Memória** — Níveis de retenção (`permanent` / `long_term` / `standard` / `short_term` / `ephemeral`) com taxas de decaimento configuráveis
- **Raciocínio Multi-Rodada** — Motor Reflect para raciocínio LLM iterativo sobre memórias recuperadas
- **Grafo de Conhecimento** — Extração automática de entidades/relações via LLM
- **Ingestão de Documentos** — Pipeline: upload → análise → fragmentação → embedding → armazenamento
- **Servidor MCP** — Suporte ao Model Context Protocol (transporte SSE)
- **Busca CJK** — Tokenizador FTS5 extensível

---

## Endpoints da API

Todos sob `/v1/`:

| Grupo | Descrição |
|-------|-----------|
| `/v1/memories` | CRUD + exclusão suave/restauração |
| `/v1/retrieve` | Recuperação híbrida de três vias |
| `/v1/reflect` | Raciocínio LLM multi-rodada |
| `/v1/conversations` | Ingestão de conversas em lote |
| `/v1/entities` | Grafo de conhecimento |
| `/v1/documents` | Upload/processamento de documentos |

---

## Integração MCP

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

## Licença

**Licença MIT** — Copyright (c) 2026 MemeryGit.

Veja [LICENSE](../../LICENSE) para detalhes.

---

<p align="center">
  <b>Construído com Go</b> • <b>Alimentado por SQLite + Qdrant</b> • <b>Compatível com MCP</b>
</p>
