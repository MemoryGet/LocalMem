<h1 align="center">
  <br>
  IClude
  <br>
</h1>

<h4 align="center">Système de mémoire hybride local-first pour applications IA.</h4>

<p align="center">
  <a href="../../README.md">🇺🇸 English</a> •
  <a href="README.zh.md">🇨🇳 中文</a> •
  <a href="README.ja.md">🇯🇵 日本語</a> •
  <a href="README.ko.md">🇰🇷 한국어</a> •
  <a href="README.es.md">🇪🇸 Español</a> •
  <a href="README.de.md">🇩🇪 Deutsch</a> •
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
  IClude combine SQLite (recherche structurée + texte intégral) et Qdrant (recherche sémantique vectorielle) pour offrir une recherche hybride à trois voies, un raisonnement LLM multi-tours, l'extraction de graphes de connaissances et l'ingestion de documents — le tout dans un seul binaire Go.
</p>

---

## Démarrage Rapide

```bash
git clone https://github.com/MemeryGit/LocalMem.git
cd LocalMem
go mod download
cp config/config.yaml ./config.yaml
go run ./cmd/server/       # Serveur API (port 8080)
go run ./cmd/mcp/          # Serveur MCP (port 8081, optionnel)
```

### Prérequis

- **Go** 1.25+
- **Qdrant** (optionnel, pour la recherche vectorielle)
- **Docling / Apache Tika** (optionnel, pour l'analyse de documents)

---

## Fonctionnalités Principales

- **Recherche Hybride** — Recherche à trois voies : SQLite FTS5 (BM25) + Qdrant vectoriel + graphe de connaissances, fusionnés via RRF (k=60)
- **Cycle de Vie Mémoire** — Niveaux de rétention (`permanent` / `long_term` / `standard` / `short_term` / `ephemeral`) avec taux de décroissance configurables
- **Raisonnement Multi-Tours** — Moteur Reflect pour le raisonnement LLM itératif sur les mémoires récupérées
- **Graphe de Connaissances** — Extraction automatique d'entités/relations via LLM
- **Ingestion de Documents** — Pipeline : téléchargement → analyse → découpage → embedding → stockage
- **Serveur MCP** — Support du Model Context Protocol (transport SSE)
- **Recherche CJK** — Tokeniseur FTS5 extensible

---

## Points de Terminaison API

Tous sous `/v1/` :

| Groupe | Description |
|--------|-------------|
| `/v1/memories` | CRUD + suppression douce/restauration |
| `/v1/retrieve` | Recherche hybride à trois voies |
| `/v1/reflect` | Raisonnement LLM multi-tours |
| `/v1/conversations` | Ingestion de conversations par lots |
| `/v1/entities` | Graphe de connaissances |
| `/v1/documents` | Téléchargement/traitement de documents |

---

## Intégration MCP

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

## Licence

**Licence MIT** — Copyright (c) 2026 MemeryGit.

Voir [LICENSE](../../LICENSE) pour les détails.

---

<p align="center">
  <b>Construit avec Go</b> • <b>Propulsé par SQLite + Qdrant</b> • <b>Compatible MCP</b>
</p>
