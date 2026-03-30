<h1 align="center">
  <br>
  LocalMem
  <br>
</h1>

<h4 align="center">Локальная гибридная система памяти для ИИ-приложений.</h4>

<p align="center">
  <a href="../../README.md">🇺🇸 English</a> •
  <a href="README.zh.md">🇨🇳 中文</a> •
  <a href="README.ja.md">🇯🇵 日本語</a> •
  <a href="README.ko.md">🇰🇷 한국어</a> •
  <a href="README.es.md">🇪🇸 Español</a> •
  <a href="README.de.md">🇩🇪 Deutsch</a> •
  <a href="README.fr.md">🇫🇷 Français</a> •
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
  IClude объединяет SQLite (структурированный + полнотекстовый поиск) и Qdrant (векторный семантический поиск), обеспечивая трёхканальный гибридный поиск, многоэтапное рассуждение LLM, извлечение графа знаний и загрузку документов — всё в одном бинарном файле Go.
</p>

---

## Быстрый Старт

```bash
git clone https://github.com/MemeryGit/LocalMem.git
cd LocalMem
go mod download
cp config/config.yaml ./config.yaml
go run ./cmd/server/       # API-сервер (порт 8080)
go run ./cmd/mcp/          # MCP-сервер (порт 8081, опционально)
```

### Требования

- **Go** 1.25+
- **Qdrant** (опционально, для векторного поиска)
- **Docling / Apache Tika** (опционально, для анализа документов)

---

## Основные Возможности

- **Гибридный поиск** — Трёхканальный поиск: SQLite FTS5 (BM25) + Qdrant вектор + граф знаний, объединённые через RRF (k=60)
- **Жизненный цикл памяти** — Уровни хранения (`permanent` / `long_term` / `standard` / `short_term` / `ephemeral`) с настраиваемыми скоростями затухания
- **Многоэтапное рассуждение** — Движок Reflect для итеративного рассуждения LLM над извлечёнными воспоминаниями
- **Граф знаний** — Автоматическое извлечение сущностей/отношений через LLM
- **Загрузка документов** — Конвейер: загрузка → анализ → разбивка → эмбеддинг → сохранение
- **MCP-сервер** — Поддержка Model Context Protocol (SSE-транспорт)
- **Полнотекстовый поиск CJK** — Подключаемый токенизатор FTS5

---

## API-эндпоинты

Все под `/v1/`:

| Группа | Описание |
|--------|----------|
| `/v1/memories` | CRUD + мягкое удаление/восстановление |
| `/v1/retrieve` | Трёхканальный гибридный поиск |
| `/v1/reflect` | Многоэтапное рассуждение LLM |
| `/v1/conversations` | Пакетная загрузка разговоров |
| `/v1/entities` | Граф знаний |
| `/v1/documents` | Загрузка/обработка документов |

---

## Интеграция MCP

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

## Лицензия

**Лицензия MIT** — Copyright (c) 2026 MemeryGit.

См. [LICENSE](../../LICENSE) для подробностей.

---

<p align="center">
  <b>Создано на Go</b> • <b>На основе SQLite + Qdrant</b> • <b>Поддержка MCP</b>
</p>
