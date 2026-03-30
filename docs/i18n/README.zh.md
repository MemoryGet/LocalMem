<h1 align="center">
  <br>
  LocalMem
  <br>
</h1>

<h4 align="center">为 AI 应用打造的本地优先混合记忆系统。</h4>

<p align="center">
  <a href="../../README.md">🇺🇸 English</a> •
  <a href="README.ja.md">🇯🇵 日本語</a> •
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
</p>

<p align="center">
  IClude 将 SQLite（结构化 + 全文检索）与 Qdrant（向量语义检索）相结合，提供三路混合检索、多轮 LLM 推理、知识图谱抽取和文档摄入功能 —— 全部集成在单个 Go 二进制文件中。
</p>

---

## 快速开始

```bash
# 克隆仓库
git clone https://github.com/MemeryGit/LocalMem.git
cd LocalMem

# 安装依赖
go mod download

# 配置
cp config/config.yaml ./config.yaml

# 启动 API 服务 (端口 8080)
go run ./cmd/server/

# 启动 MCP 服务 (端口 8081, 可选)
go run ./cmd/mcp/
```

### Docker 部署

```bash
docker-compose -f deploy/docker-compose.yml up
```

### 环境要求

- **Go** 1.25+
- **Qdrant**（可选，用于向量检索）
- **Docling / Apache Tika**（可选，用于文档解析）
- **Jieba 分词服务**（可选，用于中文全文分词）

---

## 核心特性

- **混合检索** — 三路搜索融合 SQLite FTS5 (BM25)、Qdrant 向量相似度和知识图谱关联，通过倒数排名融合 (RRF, k=60) 合并结果
- **记忆生命周期** — 保留层级（`permanent` / `long_term` / `standard` / `short_term` / `ephemeral`），可配置衰减率、软删除和强化机制
- **多轮推理** — 反思引擎对检索到的记忆进行迭代式 LLM 推理，结论可自动保存
- **知识图谱** — 通过 LLM 自动从记忆内容中抽取实体/关系，支持基于图的关联检索
- **文档摄入** — 上传 → 解析 (Docling / Tika 降级) → 分块 (Markdown 感知 + 递归) → 嵌入 → 存储
- **MCP 服务** — 支持模型上下文协议 (SSE 传输)，无缝集成 AI 编程助手
- **中日韩全文搜索** — 可插拔 FTS5 分词器，支持 Jieba、Simple CJK 和 Noop 模式
- **自主维护** — 心跳引擎负责衰减审计、孤儿清理和矛盾检测

---

## 工作原理

### 架构

IClude 采用分层 Go 架构（`cmd/` + `internal/` + `pkg/`）：

```
cmd/server/       → HTTP API 服务 (Gin, 端口 8080)
cmd/mcp/          → MCP 服务 (SSE 传输, 端口 8081)
internal/store/   → 存储接口 (8个) + SQLite/Qdrant 实现
internal/memory/  → 管理器 (CRUD + 双写), ContextManager, GraphManager
internal/search/  → 检索器 (三模式: SQLite/Qdrant/混合) + RRF 融合
internal/reflect/ → 多轮 LLM 推理引擎
internal/document/→ 文档处理器 (上传 → 分块 → 嵌入 → 存储)
pkg/              → 可复用包 (Qdrant 客户端, 分词器, SQL 构建器)
```

### 存储模式

通过 `config.yaml` 配置：

| 模式 | 说明 |
|------|------|
| **仅 SQLite** | 结构化查询 + FTS5 全文检索 (BM25 加权) |
| **仅 Qdrant** | 向量语义检索 |
| **混合模式** | 两者同时启用 — 结果通过加权 RRF (k=60) 合并 |

尽力双写：SQLite 为主存储；Qdrant 写入失败仅记录日志，不回滚 SQLite。

---

## API 端点

所有端点位于 `/v1/` 下：

| 分组 | 说明 |
|------|------|
| `/v1/memories` | CRUD + 软删除/恢复 + 强化 + 标签关联 |
| `/v1/retrieve` | 三路混合检索 |
| `/v1/timeline` | 时间线记忆检索 |
| `/v1/reflect` | 多轮 LLM 推理 |
| `/v1/conversations` | 对话批量导入 + 按上下文检索 |
| `/v1/contexts` | 层级上下文树（物化路径） |
| `/v1/tags` | 标签管理 + 关联 |
| `/v1/entities` | 知识图谱实体 + 关系 |
| `/v1/documents` | 文档上传 / 处理 / 列表 |

---

## MCP 集成

通过 MCP SSE 传输连接 AI 助手（Claude Code、Cursor 等）：

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

**可用 MCP 工具：** `recall_memories`、`save_memory`、`reflect`、`ingest_conversation`、`timeline`

---

## Python SDK

```bash
pip install iclude
```

```python
from iclude import ICludeClient

client = ICludeClient(base_url="http://localhost:8080")
client.save_memory(content="重要上下文", kind="note")
results = client.retrieve(query="上下文")
```

---

## 开发

```bash
go fmt ./...                 # 格式化代码
go vet ./...                 # 静态分析
go test ./testing/...        # 运行所有测试
go test -run TestName ./testing/...  # 运行单个测试
```

---

## 许可证

本项目基于 **MIT 许可证** 开源。

Copyright (c) 2026 MemeryGit.

详见 [LICENSE](../../LICENSE) 文件。

---

<p align="center">
  <b>使用 Go 构建</b> • <b>基于 SQLite + Qdrant</b> • <b>MCP 就绪</b>
</p>
