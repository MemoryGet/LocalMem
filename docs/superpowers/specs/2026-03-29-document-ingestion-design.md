# Document Ingestion Pipeline Design

> 文件解析入库管线设计 / File parsing and knowledge base ingestion pipeline

**Date:** 2026-03-29
**Status:** Approved
**Branch:** feat/claude-code-integration

---

## 1. Overview

为 IClude 新增文件上传 + 自动解析入库能力。用户上传 PDF/DOCX/PPTX/XLSX/MD/HTML/TXT/图片，系统自动完成：文件存储 → 格式解析 → 智能分块 → Memory 入库（SQLite + 可选 Qdrant 双写）。

### Scope

| 包含 | 不包含（后续迭代） |
|------|------------------|
| multipart 文件上传端点 | VLM 图片描述 (Phase 2) |
| Docling + Tika 双引擎降级解析 | 语义分块 (embedding 主题变化检测) |
| 三层分块管线 (结构切分 → 递归字符 → 上下文增强) | 批量文件上传 |
| 异步处理 + status 追踪 | WebSocket/SSE 实时进度推送 |
| OCR 图片文字提取 (Docling 自带) | 独立 worker 进程 + 消息队列 |
| SHA-256 文档级去重 | 语义级近似去重 (MinHash) |
| Docker Compose sidecar 部署 | SMB/NFS 网络文件存储 |
| 手动 /reprocess 纯文本兜底 | — |
| FileStore 接口抽象 (本地磁盘实现) | — |

### Design Decisions

1. **Docling (MIT) 主力 + Tika (Apache 2.0) 兜底** — 协议安全，Docling 表格 97.9% 准确率，Tika 覆盖 1000+ 冷门格式，Tika 有官方 Go SDK
2. **Docker Compose sidecar** — 符合项目"本地优先"定位，不依赖外部 SaaS
3. **goroutine 异步 + semaphore 并发控制** — 不引入外部队列，适合中等规模（单文件 <100MB，并发 <5）
4. **三层 fallback** — Docling → Tika → 手动 /reprocess，卡在项目规范上限
5. **FileStore 接口抽象** — 当前本地磁盘，后续可插拔替换为 SMB/NFS 企业共享存储
6. **图片源文件保留** — Phase 2 VLM 需要回溯原图；文本类文件解析后删除节省磁盘

---

## 2. Architecture

```
┌─────────────────────────────────────────────────────┐
│  POST /v1/documents/upload  (multipart/form-data)   │
│  Gin Handler: 接收文件 → FileStore.Save → Document  │
└──────────────────────┬──────────────────────────────┘
                       │ goroutine (异步, semaphore 限流)
                       ▼
              ┌─────────────────┐
              │  ParseRouter    │  按 DocType 路由 + 降级
              │  (parser.go)    │
              └───┬─────────┬───┘
                  │         │
          ┌───────▼───┐ ┌───▼───────┐
          │ Docling   │ │  Tika     │  降级: Docling 失败 → Tika
          │ Client    │ │  Client   │
          │ (HTTP)    │ │ (go-tika) │
          └───────┬───┘ └───┬───────┘
                  │         │
                  ▼         ▼
              ParseResult
              ├─ Content: Markdown 或纯文本
              ├─ Format: "markdown" | "plaintext"
              └─ Metadata: 页数/标题/语言等
                       │
                       ▼
              ┌─────────────────┐
              │  Chunker        │  混合分块策略
              │  (chunker.go)   │
              │                 │
              │  Markdown → 结构感知分块 (标题/表格/代码块)
              │  纯文本  → 递归字符分块 (overlap)
              │  全部    → 上下文前缀增强
              └────────┬────────┘
                       │
                       ▼
              ┌─────────────────┐
              │  现有 Manager   │  每个 chunk → Memory
              │  .Create()      │  SQLite + Qdrant 条件双写
              │  + Embedder     │  + 自动 embedding (Qdrant 启用时)
              └─────────────────┘
```

**Dependency flow**: `api/handler → document.Processor → ParseRouter(Docling, Tika) + Chunker + FileStore → memory.Manager → store(SQLite, Qdrant)`

### Startup Wiring

```
现有: Config → Logger → Embedder → Stores → ... → DocProcessor → Router
改为: Config → Logger → Embedder → Stores → ... → InitDocumentPipeline() → Router
                                                        ↑
                                          内部构建: FileStore + DoclingClient
                                          + TikaClient + ParseRouter + Chunker
                                          + Processor (仅当 document.enabled=true)
```

`document.InitDocumentPipeline()` 工厂函数封装全部初始化逻辑，main.go 只多一行调用。

---

## 3. Interfaces

### Parser

```go
// Parser 文档解析器接口 / Document parser interface
type Parser interface {
    Parse(ctx context.Context, filePath string, docType string) (*ParseResult, error)
    Supports(docType string) bool
}

// ParseResult 解析结果 / Parse result
type ParseResult struct {
    Content  string         // Markdown 或纯文本
    Format   string         // "markdown" | "plaintext"
    Metadata map[string]any // 页数、标题、语言等
}
```

实现：`DoclingParser` (HTTP client → docling-serve)、`TikaParser` (google/go-tika)。

### ParseRouter

```go
// ParseRouter 解析路由器 / Parse router with fallback chain
type ParseRouter struct {
    primary  Parser  // Docling
    fallback Parser  // Tika
}

// Parse 解析文件 / Parse file with fallback
func (r *ParseRouter) Parse(ctx context.Context, filePath string, docType string) (*ParseResult, error)
```

逻辑：primary.Parse() → 失败 → fallback.Parse() → 失败 → 返回 error。

### Chunker

```go
// Chunker 分块器接口 / Chunker interface
type Chunker interface {
    Chunk(content string, opts ChunkOptions) []Chunk
}

// ChunkOptions 分块配置 / Chunk options
type ChunkOptions struct {
    MaxTokens        int    // 目标块大小 (token), 默认 512
    OverlapTokens    int    // 重叠区 (token), 默认 50
    ContextPrefix    bool   // 是否添加上下文前缀
    DocName          string // 文档名 (用于前缀)
    KeepTableIntact  bool   // 表格不切分
    KeepCodeIntact   bool   // 代码块不切分
}

// Chunk 分块结果 / Chunk with metadata
type Chunk struct {
    Content    string // 块内容（含上下文前缀）
    RawContent string // 原始内容（不含前缀，用于去重 hash）
    Index      int    // 块序号
    Heading    string // 标题链: "第二章 > 2.1 概述"
    ChunkType  string // "text" | "table" | "code" | "list"
    PageStart  int    // 起始页码 (如有)
    TokenCount int    // token 估算
}
```

实现：`MarkdownChunker`（三层管线）、`TextChunker`（递归字符分块 + overlap）。

### FileStore

```go
// FileStore 文件存储接口 / File storage interface
type FileStore interface {
    Save(ctx context.Context, docID string, filename string, reader io.Reader) (path string, err error)
    Get(ctx context.Context, path string) (io.ReadCloser, error)
    Delete(ctx context.Context, path string) error
}
```

实现：`LocalFileStore`（本地磁盘 `data/uploads/{doc_id}/`）。后续可替换为 SMB/NFS。

---

## 4. Chunking Pipeline

### 三层分块管线

```
解析输出 (Markdown 或纯文本)
        │
        ▼
  ┌──────────────┐
  │ Layer 1:     │  按 Markdown 结构预切
  │ 结构切分     │  标题(#~####) / 表格 / 代码块 / 列表 → 独立 Section
  │              │  每个 Section 携带 heading chain 元数据
  │              │  (纯文本输入跳过此层)
  └──────┬───────┘
         │
         ▼
  ┌──────────────┐
  │ Layer 2:     │  对超长 Section 做递归切分
  │ 递归字符切分 │  目标: 512 token (≈1500 中文字符)
  │ + overlap    │  overlap: 50 token (≈150 字符)
  │              │  切分优先级: \n\n > \n > 句号/。 > 空格
  └──────┬───────┘
         │
         ▼
  ┌──────────────┐
  │ Layer 3:     │  每个 chunk 前插上下文前缀
  │ 上下文增强   │  格式: "【{文档名} > {标题链}】\n{内容}"
  │              │  提升检索时的语义匹配度
  └──────┬───────┘
         │
         ▼
     []Chunk → Memory 入库
```

### 结构切分规则

| 元素 | 行为 |
|------|------|
| Markdown 标题 (`#`~`####`) | 作为切分边界，标题文本进入 heading chain |
| 表格 | 整表作为一个 chunk（`keep_table_intact`），不切开 |
| 代码块 | 整块保持完整（`keep_code_intact`），不切开 |
| 列表 | 同一列表尽量保持在一个 chunk 内 |
| 超长段落 | Layer 2 递归切分 |
| 纯文本输入 (Tika) | 跳过 Layer 1，直接从 Layer 2 开始 |

### Memory 入库映射

每个 Chunk 创建 Memory 时：

| Memory 字段 | 值 |
|------------|-----|
| `Content` | chunk.Content (含上下文前缀) |
| `SourceType` | "document" |
| `SourceRef` | doc.Name |
| `DocumentID` | doc.ID |
| `ChunkIndex` | chunk.Index |
| `Scope` | doc.Scope |
| `Kind` | "note" |
| `ContextID` | doc.ContextID (如有) |
| `Summary` | chunk.Heading (标题链，进入 FTS5 索引) |
| `Metadata["chunk_type"]` | "text" / "table" / "code" / "list" |
| `Metadata["page_start"]` | 页码 (如有) |

---

## 5. API Design

### Endpoints

```
POST   /v1/documents/upload          新增: multipart 文件上传
GET    /v1/documents/:id/status      新增: 轮询处理进度
POST   /v1/documents/:id/reprocess   保留: 手动纯文本兜底 (现有)
GET    /v1/documents                 不变
GET    /v1/documents/:id             不变
DELETE /v1/documents/:id             不变 (联动清理源文件 + 关联 Memory chunks)
```

### POST /v1/documents/upload

```
Content-Type: multipart/form-data

字段:
  file        (必填) 文件二进制
  name        (可选) 文档名，默认取文件名
  scope       (可选) 作用域
  context_id  (可选) 关联上下文
  metadata    (可选) JSON 字符串，附加元数据
```

**响应 (201)**:
```json
{
  "data": {
    "id": "doc_xxxx",
    "name": "架构设计.pdf",
    "doc_type": "pdf",
    "status": "pending",
    "file_size": 2048576
  }
}
```

**处理流程**:
1. 校验文件大小（上限配置化，默认 100MB）
2. 校验文件类型（白名单配置化）
3. FileStore.Save → `data/uploads/{doc_id}/{原始文件名}`
4. SHA-256 去重检查（同 scope 内相同 hash → 返回已有文档）
5. 创建 Document 记录（status=pending）
6. 启动 goroutine 异步解析
7. 立即返回 201

### GET /v1/documents/:id/status

```json
{
  "data": {
    "id": "doc_xxxx",
    "status": "chunking",
    "stage": "chunking",
    "parser": "docling",
    "chunk_count": 0,
    "error_msg": ""
  }
}
```

---

## 6. Async Processing & Error Recovery

### Status Lifecycle

```
pending → parsing → chunking → embedding → ready
                                              │
          任意阶段失败 ──────────────────→ failed
```

### 三层 Fallback

```
1. Docling 解析
   ├─ 成功 → ParseResult (markdown)
   └─ 失败 (超时/500/格式不支持)
        │
        ▼
2. Tika 解析
   ├─ 成功 → ParseResult (plaintext)
   └─ 失败
        │
        ▼
3. status=failed, error_msg 记录原因
   用户可手动 POST /v1/documents/:id/reprocess 粘贴纯文本
```

### Concurrency Control

```go
type Processor struct {
    // ...
    sem chan struct{} // semaphore, 容量 = config.document.max_concurrent (默认 3)
}
```

### Error Handling

- Docling/Tika HTTP 调用：单次超时不重试，直接降级下一层
- Memory 入库失败（单个 chunk）：跳过该 chunk，继续后续，error_msg 记录失败 chunk index 列表
- 整体超时配置化（默认 10 分钟），超时标记 failed

### File Cleanup Strategy

| 文件类型 | 解析成功后 | 删除文档时 |
|---------|-----------|-----------|
| 文本类 (PDF/DOCX/...) | 删除源文件 | 删除目录 |
| 图片 (PNG/JPG) | 保留源文件 | 删除目录 |

配置: `cleanup_after_parse: true`, `keep_images: true`

### 删除文档联动清理

`DELETE /v1/documents/:id` 时依次执行：
1. 查询所有 `document_id = id` 的 Memory，逐条删除（SQLite + Qdrant）
2. FileStore.Delete 清理源文件目录
3. 删除 Document 记录

---

## 7. Model Changes

### Document (updated)

```go
type Document struct {
    // ...现有 13 字段
    ErrorMsg string `json:"error_msg,omitempty"` // 新增: 失败原因
    Stage    string `json:"stage,omitempty"`      // 新增: 当前处理阶段
    Parser   string `json:"parser,omitempty"`     // 新增: 实际使用的解析器
}
```

### DocumentStore (updated)

```go
// 新增方法
UpdateErrorMsg(ctx context.Context, id string, msg string) error
```

---

## 8. File Inventory

### 新增文件

```
internal/document/
  ├─ factory.go       InitDocumentPipeline() 工厂函数
  ├─ parser.go        Parser 接口 + ParseResult + ParseRouter
  ├─ docling.go       Docling HTTP 客户端
  ├─ tika.go          Tika 客户端 (google/go-tika)
  ├─ chunker.go       Chunker 接口 + MarkdownChunker + TextChunker
  └─ file_store.go    FileStore 接口 + LocalFileStore
```

### 修改文件

```
internal/document/processor.go      Upload 改文件流, Process 调用 ParseRouter+Chunker
internal/api/document_handler.go    multipart 上传 + /status 端点
internal/api/router.go              新增路由
internal/model/document.go          新增 ErrorMsg/Stage/Parser 字段
internal/model/request.go           移除 CreateDocumentRequest (改 multipart)
internal/store/sqlite_document.go   新增 UpdateErrorMsg 方法 + 新字段迁移
internal/store/interfaces.go        DocumentStore 新增 UpdateErrorMsg
cmd/server/main.go                  调用 document.InitDocumentPipeline()
config.yaml                         新增 document 配置段
deploy/docker-compose.yml           新增 docling + tika sidecar
go.mod                              新增 google/go-tika 依赖
```

---

## 9. Configuration

```yaml
document:
  enabled: true
  max_concurrent: 3
  process_timeout: 10m
  max_file_size: 104857600          # 100MB
  cleanup_after_parse: true         # 解析成功后删除文本类源文件
  keep_images: true                 # 图片源文件始终保留

  allowed_types:
    - pdf
    - docx
    - pptx
    - xlsx
    - md
    - html
    - txt
    - png
    - jpg
    - jpeg

  file_store:
    provider: "local"               # local | smb (后续)
    local:
      base_dir: "./data/uploads"

  docling:
    url: "http://localhost:5001"
    timeout: 120s

  tika:
    url: "http://localhost:9998"
    timeout: 60s

  chunking:
    max_tokens: 512
    overlap_tokens: 50
    context_prefix: true
    keep_table_intact: true
    keep_code_intact: true
```

---

## 10. Docker Compose

```yaml
services:
  iclude:
    depends_on:
      - docling
      - tika
    volumes:
      - uploads:/app/data/uploads

  docling:
    image: quay.io/docling-project/docling-serve:latest
    ports:
      - "5001:5001"
    environment:
      - DOCLING_BACKEND=dlparse_v2
      - DOCLING_OCR_ENGINE=easyocr
    deploy:
      resources:
        limits:
          memory: 4G

  tika:
    image: apache/tika:latest
    ports:
      - "9998:9998"
    deploy:
      resources:
        limits:
          memory: 1G

volumes:
  uploads:
```

---

## 11. Health Check

Processor 启动时 ping Docling 和 Tika：

| 情况 | 行为 |
|------|------|
| 两个都通 | 正常启动 |
| Docling 不通 | warn 日志，仍启动（仅 Tika 可用） |
| 都不通 | warn 日志，仍启动（仅手动 /reprocess 可用） |

不因 sidecar 不可用阻塞主服务启动。
