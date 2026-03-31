<h1 align="center">
  <br>
  LocalMem
  <br>
</h1>

<h4 align="center">AIアプリケーション向けのローカルファースト・ハイブリッドメモリシステム。</h4>

<p align="center">
  <a href="../../README.md">🇺🇸 English</a> •
  <a href="README.zh.md">🇨🇳 中文</a> •
  <a href="README.ko.md">🇰🇷 한국어</a> •
  <a href="README.es.md">🇪🇸 Español</a> •
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
  LocalMemはSQLite（構造化＋全文検索）とQdrant（ベクトルセマンティック検索）を組み合わせ、三方向ハイブリッド検索、マルチラウンドLLM推論、ナレッジグラフ抽出、ドキュメント取り込み機能を単一のGoバイナリで提供します。
</p>

---

## クイックスタート

```bash
# リポジトリをクローン
git clone https://github.com/MemeryGit/LocalMem.git
cd LocalMem

# 依存関係をインストール
go mod download

# 設定
cp config/config.yaml ./config.yaml

# APIサーバーを起動（ポート8080）
go run ./cmd/server/

# MCPサーバーを起動（ポート8081、オプション）
go run ./cmd/mcp/
```

### Docker デプロイ

```bash
docker-compose -f deploy/docker-compose.yml up
```

### 必要要件

- **Go** 1.25+
- **Qdrant**（オプション、ベクトル検索用）
- **Docling / Apache Tika**（オプション、ドキュメント解析用）

---

## 主な機能

- **ハイブリッド検索** — SQLite FTS5 (BM25)、Qdrantベクトル類似度、ナレッジグラフ関連付けを組み合わせた三方向検索。Reciprocal Rank Fusion (RRF, k=60) で統合
- **メモリライフサイクル** — 保持層（`permanent` / `long_term` / `standard` / `short_term` / `ephemeral`）、設定可能な減衰率、ソフト削除、強化メカニズム
- **マルチラウンド推論** — Reflect Engineが検索されたメモリに対して反復的なLLM推論を実行、結論を自動保存
- **ナレッジグラフ** — LLMによるメモリコンテンツからのエンティティ/関係自動抽出、グラフベースの関連検索
- **ドキュメント取り込み** — アップロード → 解析 (Docling / Tika フォールバック) → チャンク分割 → 埋め込み → 保存
- **MCPサーバー** — Model Context Protocol対応（SSEトランスポート）、AIコーディングアシスタントとのシームレスな統合
- **CJK全文検索** — プラグイン可能なFTS5トークナイザー（Jieba、Simple CJK、Noopモード対応）

---

## ストレージモード

`config.yaml` で設定：

| モード | 説明 |
|--------|------|
| **SQLiteのみ** | 構造化クエリ + FTS5全文検索（BM25重み付き） |
| **Qdrantのみ** | ベクトルセマンティック検索 |
| **ハイブリッド** | 両方有効 — 重み付きRRF (k=60) で結果を統合 |

---

## APIエンドポイント

すべてのエンドポイントは `/v1/` 配下：

| グループ | 説明 |
|----------|------|
| `/v1/memories` | CRUD + ソフト削除/復元 + 強化 |
| `/v1/retrieve` | 三方向ハイブリッド検索 |
| `/v1/reflect` | マルチラウンドLLM推論 |
| `/v1/conversations` | 会話バッチ取り込み |
| `/v1/entities` | ナレッジグラフ |
| `/v1/documents` | ドキュメントアップロード/処理 |

---

## MCP連携

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

**利用可能なMCPツール：** `recall_memories`、`save_memory`、`reflect`、`ingest_conversation`、`timeline`

---

## ライセンス

**MITライセンス** — Copyright (c) 2026 MemeryGit.

詳細は [LICENSE](../../LICENSE) を参照してください。

---

<p align="center">
  <b>Go で構築</b> • <b>SQLite + Qdrant 搭載</b> • <b>MCP 対応</b>
</p>
