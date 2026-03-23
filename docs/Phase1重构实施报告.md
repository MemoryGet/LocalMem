# IClude Phase 1 — 重构实施报告

> **完成日期**：2026-03-18
> **范围**：`internal/` 全层 + `cmd/server` + `testing/`
> **分两阶段**：Phase 1a（目录重组）已完成 → Phase 1b（数据库分层）本次完成

---

## 一、Phase 1 整体目标

将 IClude 从原始的扁平式代码组织和单表存储，逐步升级为 **Go 惯用分层架构** + **多表分层存储体系**。

| 阶段 | 目标 | 状态 |
|------|------|------|
| Phase 1a | 目录重组：`business/` → `internal/`，接口扁平化，去除冗余抽象 | ✅ 已完成 |
| Phase 1b | 数据库分层：单表 → 8 表，新增 Context/Tag/Graph/Document/Lifecycle | ✅ 已完成 |

---

## 二、Phase 1a — 目录重组（已完成）

### 目录结构变更

```
旧结构                              新结构
business/config/                →   internal/config/
business/models/                →   internal/model/
business/repository/{sqlite,qdrant}/ → internal/store/（扁平化）
business/logic/memory/          →   internal/memory/
business/logic/retriever/       →   internal/search/
business/handlers/              →   internal/api/
components/clients/qdrant/      →   pkg/qdrant/
components/clients/embedding/   →   internal/embed/
services/api/main.go            →   cmd/server/main.go
```

### 关键简化

| 维度 | 旧 | 新 |
|------|----|----|
| 存储包结构 | `repository/` + `repository/sqlite/` + `repository/qdrant/`（3 层） | `store/`（1 层，接口+实现同包） |
| 工厂模式 | `InitFunc` 函数指针 | `InitStores()` 直接构造 |
| Qdrant 客户端 | `components/clients/qdrant/` | `pkg/qdrant/`（可外部引用） |

---

## 三、Phase 1b — 数据库分层（本次完成）

### 3.1 重构目标

在 Phase 1a 的代码架构基础上，新增分层存储体系：

- **Contexts** — 层级容器（树形结构，物化路径）
- **Tags** — 多对多标签系统
- **Entities + Relations** — 知识图谱
- **Documents** — 文档知识库（上传 → 分块 → 记忆化）
- **Lifecycle** — 记忆强度衰减 / 软删除 / 过期机制
- **Timeline** — 时间线索引

**约束**：向后兼容，增量迁移，旧 API 请求不受影响。

---

### 3.2 数据模型变更

#### memories 表：10 列 → 31 列

```
原有 10 列（不变）
├── id, content, metadata, team_id
├── embedding_id, parent_id, is_latest, access_count
└── created_at, updated_at

+ 7 列 — 分层扩展（struct 字段已有，本次落表）
├── uri                  iclude://{scope}/{path}#{id}
├── context_id           FK → contexts.id
├── kind                 note / fact / skill / profile
├── sub_kind             entity / event / pattern / preference
├── scope                顶级命名空间
├── abstract             一句话摘要
└── summary              核心信息

+ 5 列 — 时间线与来源
├── happened_at          事件发生时间
├── source_type          manual / conversation / document / api
├── source_ref           来源引用标识
├── document_id          FK → documents.id
└── chunk_index          文档分块序号

+ 6 列 — 生命周期
├── deleted_at           软删除时间
├── strength             记忆强度 0~1，默认 1.0
├── decay_rate           衰减速率，默认 0.01
├── last_accessed_at     最近访问时间
├── reinforced_count     强化次数
└── expires_at           过期时间

+ 3 列 — V3 对话与保留策略（V2→V3 迁移新增）
├── retention_tier       保留层级：permanent/long_term/standard/short_term/ephemeral，默认 'standard'
├── message_role         对话角色：user/assistant/system/tool，默认 ''
└── turn_number          对话轮次序号，默认 0
```

#### 新增 7 张表

| 表名 | 用途 | 关键约束 |
|------|------|---------|
| `schema_version` | 迁移版本追踪 | PK(version) |
| `contexts` | 层级容器 | UNIQUE(path) |
| `tags` | 标签 | UNIQUE(name, scope) |
| `memory_tags` | 记忆 ↔ 标签 | PK(memory_id, tag_id) |
| `entities` | 知识图谱实体 | UNIQUE(name, entity_type, scope) |
| `entity_relations` | 实体关系 | UNIQUE(source, target, type) |
| `memory_entities` | 记忆 ↔ 实体 | PK(memory_id, entity_id) |
| `documents` | 文档知识库 | content_hash 去重 |

#### FTS5 升级

| 版本 | 列 | 同步方式 |
|------|------|---------|
| V1 | `content` | 触发器自动同步 |
| V2 | `content, abstract, summary` | external content 手动同步 |

**BM25 加权搜索**（V2 起生效）：使用 `bm25(memories_fts, 10, 5, 3)` 替代原始 `f.rank` 排序。权重分配：`content=10, abstract=5, summary=3`，搜索结果按加权 BM25 分数排序，同时返回分数供业务层使用。`SearchTextFiltered` 方法支持按 scope/context_id/kind/source_type/happened_at/strength/retention_tier/message_role 动态过滤。

#### 新增索引（13 个）

```sql
-- V2 索引（10 个）
idx_memories_scope           ON memories(scope)
idx_memories_context_id      ON memories(context_id)    WHERE context_id != ''
idx_memories_kind            ON memories(kind)           WHERE kind != ''
idx_memories_deleted_at      ON memories(deleted_at)     WHERE deleted_at IS NOT NULL
idx_memories_happened_at     ON memories(happened_at)    WHERE happened_at IS NOT NULL
idx_memories_expires_at      ON memories(expires_at)     WHERE expires_at IS NOT NULL
idx_contexts_path            ON contexts(path)
idx_contexts_parent_id       ON contexts(parent_id)
idx_documents_status         ON documents(status)        WHERE status IN ('pending','processing')
idx_documents_content_hash   ON documents(content_hash)  WHERE content_hash != ''

-- V3 索引（3 个）
idx_memories_retention_tier  ON memories(retention_tier)
idx_memories_message_role    ON memories(message_role)   WHERE message_role != ''
idx_memories_context_turn    ON memories(context_id, turn_number) WHERE context_id != '' AND turn_number > 0
```

---

### 3.3 迁移框架

```
sqlite_migration.go
├── getCurrentVersion()        读取 schema_version 表
├── Migrate()                  入口，按版本号顺序执行
├── migrateV0ToV1()            全新库：建 memories + FTS5 + 触发器
├── migrateV1ToV2()            分层扩展：
│   ├── ALTER TABLE × 18 列（逐列，忽略 duplicate column）
│   ├── CREATE TABLE × 7 张新表
│   ├── DROP TRIGGER × 3（旧 FTS5 触发器）
│   ├── DROP + RECREATE FTS5（3 列 external content）
│   ├── 回填 FTS5 数据
│   ├── CREATE INDEX × 10
│   └── UPDATE 回填默认值（scope='default', strength=1.0）
└── migrateV2ToV3()            对话与保留策略：
    ├── ALTER TABLE + 3 列（retention_tier, message_role, turn_number）
    ├── CREATE INDEX × 3（retention_tier, message_role 部分索引, context+turn 复合索引）
    └── UPDATE 回填（decay_rate=0 → retention_tier='permanent'，其余 → 'standard'）
```

**特性**：幂等、事务安全、旧库平滑升级。

---

### 3.4 存储接口总览

| 接口 | 方法数 | 说明 |
|------|--------|------|
| `MemoryStore` | 18 | 原 8 + 新增 10（DB/ListByContext/GetByURI/SearchTextFiltered/ListTimeline/SoftDelete/Restore/Reinforce/ListExpired/ListWeak） |
| `VectorStore` | 6 | 原 5 + SearchFiltered |
| `Embedder` | 2 | 不变 |
| `ContextStore` | 10 | **新增** — 树形 CRUD + Move + MemoryCount |
| `TagStore` | 8 | **新增** — Tag CRUD + 记忆关联 |
| `GraphStore` | 12 | **新增** — Entity/Relation/MemoryEntity 全套 |
| `DocumentStore` | 8 | **新增** — Document CRUD + 哈希去重 + 状态管理 |

---

### 3.5 业务层架构

```
internal/memory/
├── manager.go             CRUD 增强：新字段、标签关联、Context 计数、软删除
├── context_manager.go     Context 树操作 + 循环引用检测
├── graph_manager.go       Entity/Relation/MemoryEntity 编排
└── lifecycle.go           强度衰减模型 + 搜索结果加权

internal/document/
└── processor.go           文档上传 → 段落分块(1000字) → 批量创建记忆

internal/search/
├── retriever.go           +SearchFilters 支持 + 强度加权 + Timeline
└── rrf.go                 RRF 融合（不变）
```

---

### 3.6 记忆生命周期模型

**强度衰减**

```
effective_strength = strength × e^(-decay_rate × hours_since_access)
```

- 默认 `strength = 1.0`，`decay_rate = 0.01`
- 每次访问自动更新 `last_accessed_at`
- `Reinforce()` 操作：`strength += 0.1 × (1 - strength)`

**搜索加权**

检索结果经 RRF 融合后自动应用：`result.Score *= effective_strength`，同时过滤 `expires_at < now` 的过期记忆。

**软删除**

- `DELETE /v1/memories/:id` → 设置 `deleted_at`，不物理删除
- 所有查询默认 `WHERE deleted_at IS NULL`
- `POST /v1/memories/:id/restore` → 清除 `deleted_at`

---

### 3.7 依赖流（无环）

```
cmd/server/main.go
 ├── internal/config
 ├── internal/logger
 ├── internal/store          ← InitStores() 构造全部 store
 ├── internal/embed          ← NewEmbedder()
 ├── internal/memory         ← Manager / ContextManager / GraphManager
 ├── internal/document       ← Processor
 ├── internal/search         ← Retriever
 └── internal/api            ← SetupRouter(RouterDeps)

api  →  memory, search, document, store.TagStore, model
memory →  store(接口), model
search →  store(接口), memory.lifecycle, model
document →  store(接口), model
store  →  model, pkg/qdrant
```

---

## 四、API 端点总览

### 原有端点（保持兼容，部分增强）

| 方法 | 路径 | 增强 |
|------|------|------|
| GET | `/health` | — |
| POST | `/v1/memories` | +可选字段：context_id, kind, scope, tags 等 |
| GET | `/v1/memories` | +过滤：scope, context_id, kind, tags, happened_after/before |
| GET | `/v1/memories/:id` | — |
| PUT | `/v1/memories/:id` | +可选字段同上 |
| DELETE | `/v1/memories/:id` | 改为软删除 |
| POST | `/v1/retrieve` | +filters, detail_level |

### 新增端点

**记忆扩展**

| 方法 | 路径 | 说明 |
|------|------|------|
| DELETE | `/v1/memories/:id/soft` | 显式软删除 |
| POST | `/v1/memories/:id/restore` | 恢复软删除 |
| POST | `/v1/memories/:id/reinforce` | 强化记忆（strength += 0.1 × (1 - strength)） |
| GET | `/v1/memories/:id/tags` | 获取记忆标签 |
| POST | `/v1/memories/:id/tags` | 打标签 |
| DELETE | `/v1/memories/:id/tags/:tag_id` | 移除标签 |

**对话摄取**

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/v1/conversations` | 摄取对话（创建 context + 多条记忆，支持 message_role/turn_number） |
| GET | `/v1/conversations/:context_id` | 获取对话记忆（按 turn_number 排序，支持分页） |

**运维**

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/v1/maintenance/cleanup` | 清理过期记忆（soft-delete expires_at < now 的记忆） |

**时间线**

| 方法 | 路径 | 说明 |
|------|------|------|
| GET | `/v1/timeline` | scope / after / before / limit |

**Context 容器**

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/v1/contexts` | 创建 |
| GET | `/v1/contexts/:id` | 获取 |
| PUT | `/v1/contexts/:id` | 更新 |
| DELETE | `/v1/contexts/:id` | 删除 |
| GET | `/v1/contexts/:id/children` | 子节点 |
| GET | `/v1/contexts/:id/tree` | 子树 |
| POST | `/v1/contexts/:id/move` | 移动 |

**标签**

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/v1/tags` | 创建 |
| GET | `/v1/tags` | 列表（可按 scope 过滤） |
| DELETE | `/v1/tags/:id` | 删除 |

**知识图谱**

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/v1/entities` | 创建实体 |
| GET | `/v1/entities` | 列表（scope / type 过滤） |
| GET | `/v1/entities/:id` | 获取 |
| PUT | `/v1/entities/:id` | 更新 |
| DELETE | `/v1/entities/:id` | 删除 |
| GET | `/v1/entities/:id/relations` | 关系列表 |
| GET | `/v1/entities/:id/memories` | 关联记忆 |
| POST | `/v1/entity-relations` | 创建关系 |
| DELETE | `/v1/entity-relations/:id` | 删除关系 |
| POST | `/v1/memory-entities` | 关联记忆与实体 |
| DELETE | `/v1/memory-entities` | 解除关联 |

**文档**

| 方法 | 路径 | 说明 |
|------|------|------|
| POST | `/v1/documents` | 上传 |
| GET | `/v1/documents` | 列表 |
| GET | `/v1/documents/:id` | 获取 |
| DELETE | `/v1/documents/:id` | 删除 |
| POST | `/v1/documents/:id/reprocess` | 重新处理 |

---

## 五、文件清单

### Phase 1b 修改文件（12 个）

| 文件 | 变更说明 |
|------|---------|
| `internal/model/memory.go` | +11 字段（lifecycle / timeline / document） |
| `internal/model/request.go` | 4 → 14 DTO + SearchFilters + TimelineRequest |
| `internal/model/errors.go` | +8 sentinel errors |
| `internal/store/interfaces.go` | 3 → 8 接口，方法数 16 → 64 |
| `internal/store/sqlite.go` | 全面重写：28 列 + FTS5 external content + 10 新方法 |
| `internal/store/qdrant.go` | +SearchFiltered |
| `internal/store/factory.go` | +4 store 字段，自动创建子 store |
| `internal/memory/manager.go` | +标签 / Context 处理 + 软删除 |
| `internal/search/retriever.go` | +Filters + 强度加权 + Timeline |
| `internal/api/memory_handler.go` | +SoftDelete / Restore + 过滤参数 |
| `internal/api/search_handler.go` | +Timeline |
| `internal/api/router.go` | RouterDeps + 全部新路由 |
| `internal/api/response.go` | +8 错误映射 |
| `cmd/server/main.go` | 接入所有新 Manager / Processor |

### Phase 1b 新建文件（14 个）

| 文件 | 说明 |
|------|------|
| `internal/model/context.go` | Context 模型 |
| `internal/model/graph.go` | Entity / EntityRelation / MemoryEntity / Tag |
| `internal/model/document.go` | Document 模型 |
| `internal/store/sqlite_migration.go` | V0 → V1 → V2 迁移框架 |
| `internal/store/sqlite_context.go` | ContextStore 实现 |
| `internal/store/sqlite_tags.go` | TagStore 实现 |
| `internal/store/sqlite_graph.go` | GraphStore 实现 |
| `internal/store/sqlite_document.go` | DocumentStore 实现 |
| `internal/memory/context_manager.go` | Context 业务逻辑 |
| `internal/memory/graph_manager.go` | Graph 业务逻辑 |
| `internal/memory/lifecycle.go` | 强度衰减 + 搜索加权 |
| `internal/document/processor.go` | 文档处理器 |
| `internal/api/context_handler.go` | Context HTTP 端点 |
| `internal/api/tag_handler.go` | Tag HTTP 端点 |
| `internal/api/graph_handler.go` | Graph HTTP 端点 |
| `internal/api/document_handler.go` | Document HTTP 端点 |
| `internal/api/conversation_handler.go` | 对话摄取 HTTP 端点 |

### 测试文件（10 个）

| 文件 | 覆盖内容 |
|------|---------|
| `testing/store/sqlite_test.go` | SQLite CRUD + FTS5（Phase 1a，已适配新 schema） |
| `testing/store/sqlite_migration_test.go` | 迁移幂等 + V1 → V2 升级 |
| `testing/store/sqlite_context_test.go` | Context 树操作 |
| `testing/store/sqlite_tags_test.go` | Tag CRUD + 记忆关联 |
| `testing/store/sqlite_graph_test.go` | Entity / Relation / MemoryEntity |
| `testing/store/sqlite_document_test.go` | Document CRUD + 哈希去重 |
| `testing/memory/manager_test.go` | Manager 业务逻辑（Phase 1a，已适配） |
| `testing/memory/lifecycle_test.go` | 衰减计算 + 加权过滤 |
| `testing/search/rrf_test.go` | RRF 融合（Phase 1a，不变） |
| `testing/api/handler_test.go` | HTTP 集成测试（Phase 1a，已适配） |

---

## 六、最终目录结构

```
iclude/
├── cmd/server/main.go                     入口
├── internal/
│   ├── config/config.go                   Viper 配置
│   ├── logger/logger.go                   Zap 结构化日志
│   ├── model/                             数据模型层
│   │   ├── memory.go                      Memory(31字段) + SearchResult
│   │   ├── context.go                     Context
│   │   ├── graph.go                       Entity / EntityRelation / MemoryEntity / Tag
│   │   ├── document.go                    Document
│   │   ├── request.go                     14 个 DTO + SearchFilters
│   │   └── errors.go                      13 个 sentinel errors
│   ├── store/                             存储层
│   │   ├── interfaces.go                  8 个接口，64 个方法
│   │   ├── sqlite.go                      MemoryStore 实现（28列）
│   │   ├── sqlite_migration.go            V0→V1→V2→V3 迁移
│   │   ├── sqlite_context.go              ContextStore 实现
│   │   ├── sqlite_tags.go                 TagStore 实现
│   │   ├── sqlite_graph.go                GraphStore 实现
│   │   ├── sqlite_document.go             DocumentStore 实现
│   │   ├── qdrant.go                      VectorStore 实现
│   │   └── factory.go                     InitStores()
│   ├── embed/                             Embedding 适配器
│   │   ├── embedder.go                    工厂
│   │   ├── openai.go                      OpenAI
│   │   └── ollama.go                      Ollama
│   ├── memory/                            业务逻辑层
│   │   ├── manager.go                     CRUD + 双写 + 标签 + 软删除
│   │   ├── context_manager.go             Context 树管理
│   │   ├── graph_manager.go               知识图谱管理
│   │   └── lifecycle.go                   强度衰减 + 搜索加权
│   ├── document/                          文档处理
│   │   └── processor.go                   上传 → 分块 → 记忆化
│   ├── search/                            检索层
│   │   ├── retriever.go                   单轮检索 + Filters + Timeline
│   │   └── rrf.go                         RRF 融合
│   └── api/                               HTTP 层
│       ├── router.go                      路由（RouterDeps）
│       ├── memory_handler.go              记忆 CRUD + 软删除/恢复
│       ├── search_handler.go              检索 + Timeline
│       ├── context_handler.go             Context 端点
│       ├── tag_handler.go                 标签端点
│       ├── graph_handler.go               图谱端点
│       ├── document_handler.go            文档端点
│       ├── conversation_handler.go       对话摄取端点
│       ├── response.go                    统一响应 + 13 错误映射
│       └── middleware.go                  CORS + 请求日志
├── pkg/qdrant/client.go                   可复用 Qdrant HTTP 客户端
├── testing/                               测试（10 个文件）
│   ├── store/                             5 个文件
│   ├── memory/                            2 个文件
│   ├── search/                            1 个文件
│   └── api/                               1 个文件
├── sdks/python/iclude/                    Python SDK
├── deploy/                                Docker 部署
├── config.yaml
├── CLAUDE.md
└── go.mod
```

---

## 七、验证结果

```bash
$ go vet ./...            # ✅ 无警告
$ go build ./...          # ✅ 编译通过
$ go test ./testing/...   # ✅ 全部通过

ok  iclude/testing/api      1.282s
ok  iclude/testing/memory   1.391s
ok  iclude/testing/search   0.426s
ok  iclude/testing/store    3.712s
```

---

## 八、向后兼容保证

| 场景 | 行为 |
|------|------|
| 旧 API 请求（无新字段） | 正常工作，新字段取默认值 |
| 已有数据库文件 | `Init()` 自动迁移 V1 → V2，数据保留 |
| FTS5 全文检索 | 自动重建 3 列索引，原有内容迁移 |
| `scope` 为空的旧记忆 | 回填为 `"default"` |
| `strength` 为 0 的旧记忆 | 回填为 `1.0` |
| Qdrant 未启用 | 所有新功能仍可用（仅 SQLite） |

---

## 九、数据库架构评估

### 9.1 表关系总览（9 张表 + 1 FTS5 虚拟表）

```
schema_version          独立版本追踪表
    │
memories ──────────────── memories_fts (FTS5 external content)
    │   PK: id
    │   31 列，含 retention_tier/message_role/turn_number
    │
    ├── context_id ─────→ contexts (逻辑 FK，无物理约束)
    │                      PK: id, UNIQUE(path), 物化路径树
    │
    ├── document_id ────→ documents (逻辑 FK，无物理约束)
    │                      PK: id, content_hash 去重
    │
    ├── memory_tags ────→ tags
    │   PK: (memory_id, tag_id)   PK: id, UNIQUE(name, scope)
    │
    └── memory_entities ─→ entities
        PK: (memory_id, entity_id)  PK: id, UNIQUE(name, entity_type, scope)
                                         │
                                    entity_relations
                                    PK: id, UNIQUE(source_id, target_id, relation_type)
```

### 9.2 各表详情

| 表名 | 列数 | 核心约束 | 索引数 | 说明 |
|------|------|---------|--------|------|
| `memories` | 31 | PK(id) | 11 (含 V1 team_id) | 核心表，软删除 + 生命周期 |
| `memories_fts` | 3 | external content | — | FTS5 全文索引，BM25 加权 |
| `schema_version` | 2 | PK(version) | 0 | 迁移版本追踪 |
| `contexts` | 13 | PK(id), UNIQUE(path) | 2 | 物化路径树，memory_count 反规范化 |
| `tags` | 4 | PK(id), UNIQUE(name,scope) | 0 | 标签定义 |
| `memory_tags` | 3 | PK(memory_id,tag_id) | 0 | 多对多关联，无 FK 约束 |
| `entities` | 8 | PK(id), UNIQUE(name,entity_type,scope) | 0 | 知识图谱实体 |
| `entity_relations` | 7 | PK(id), UNIQUE(source,target,type) | 0 | 实体关系 |
| `memory_entities` | 4 | PK(memory_id,entity_id) | 0 | 多对多关联，无 FK 约束 |
| `documents` | 13 | PK(id) | 2 | 文档知识库，content_hash 去重 |

### 9.3 SQLite PRAGMA 配置

在 `NewSQLiteMemoryStore()` 初始化时设置：

```sql
PRAGMA journal_mode=WAL;          -- 写前日志，支持并发读写
PRAGMA foreign_keys=ON;           -- 启用外键约束
PRAGMA busy_timeout=5000;         -- 锁等待 5 秒，避免 SQLITE_BUSY
PRAGMA mmap_size=268435456;       -- 内存映射 256MB，提升大文件读取性能
```

### 9.4 存储设计模式总结

| 模式 | 说明 | 应用位置 |
|------|------|---------|
| **软删除** | `deleted_at IS NULL` 过滤，`DELETE` 仅设时间戳 | memories |
| **物化路径** | `path` 字段存完整路径，`depth` 反规范化 | contexts |
| **External content FTS5** | FTS5 不存原始数据，通过 rowid 关联 memories 表 | memories_fts |
| **最佳努力双写** | SQLite 为主，Qdrant 写入失败仅日志不回滚 | manager.Create/Update |
| **JSON 元数据** | `metadata TEXT` 存 JSON，灵活扩展 | memories/contexts/entities/documents |
| **反规范化计数** | `memory_count` 直接存储，避免 COUNT 查询 | contexts |
| **幂等迁移** | `ALTER TABLE` 忽略 duplicate column，`CREATE TABLE IF NOT EXISTS` | sqlite_migration.go |
| **部分索引** | WHERE 条件索引，减少索引大小 | 6 个部分索引 |
| **复合索引** | 多列索引用于常见查询路径 | context_turn 复合索引 |

---

## 十、数据库测试指南

### 10.1 运行命令

```bash
# 全量测试
go test ./testing/... -v -count=1

# 按层级测试
go test ./testing/store/...   -v -count=1    # 存储层（41 个测试）
go test ./testing/memory/...  -v -count=1    # 业务层（11 个测试）
go test ./testing/search/...  -v -count=1    # 检索层（2 个测试）
go test ./testing/api/...     -v -count=1    # HTTP 层（4 个测试）

# 运行单个测试
go test -run TestMigrate_V2ToV3       ./testing/store/...
go test -run TestSearchText_BM25      ./testing/store/...
go test -run TestCalculateEffective   ./testing/memory/...
```

### 10.2 测试函数清单（58 个）

**存储层 — `testing/store/`（41 个测试）**

| 文件 | 测试函数 |
|------|---------|
| `sqlite_test.go` (12) | `TestSQLiteMemoryStore_Create`, `_Get`, `_Update`, `_Delete`, `_List`, `_SearchText`, `TestCreate_WithRetentionTier`, `TestCreate_DefaultRetentionTier`, `TestCleanupExpired`, `TestPurgeDeleted`, `TestListByContextOrdered`, `TestSearchText_BM25ColumnWeights` |
| `sqlite_migration_test.go` (4) | `TestMigrate_FreshDB`, `TestMigrate_Idempotent`, `TestMigrate_V1ToV2`, `TestMigrate_V2ToV3` |
| `sqlite_context_test.go` (8) | `TestContextStore_CreateAndGet`, `_GetByPath`, `_ParentChild`, `_ListSubtree`, `_Move`, `_MemoryCount`, `_NotFound` + 子测试 |
| `sqlite_tags_test.go` (5) | `TestTagStore_CreateAndGet`, `_DuplicateName`, `_ListTags`, `_TagMemory`, `_DeleteTag` |
| `sqlite_graph_test.go` (5) | `TestGraphStore_EntityCRUD`, `_DuplicateEntity`, `_ListEntities`, `_Relations`, `_MemoryEntity` |
| `sqlite_document_test.go` (7) | `TestDocumentStore_CreateAndGet`, `_DuplicateHash`, `_ListByStatus`, `_UpdateStatus`, `_NotFound`, `_GetByHash`, `_Delete` |

**业务层 — `testing/memory/`（11 个测试）**

| 文件 | 测试函数 |
|------|---------|
| `manager_test.go` (5) | `TestManager_Create`, `_Get`, `_Update`, `_Delete`, `_List` |
| `lifecycle_test.go` (6) | `TestCalculateEffectiveStrength`, `TestCalculateEffectiveStrength_PermanentTier`, `TestResolveTierDefaults_AllTiers`, `TestValidateRetentionTier_Valid`, `TestValidateRetentionTier_Invalid`, `TestApplyStrengthWeighting` |

**检索层 — `testing/search/`（2 个测试）**

| 文件 | 测试函数 |
|------|---------|
| `rrf_test.go` (2) | `TestMergeRRF`, `TestMergeRRFWithK_CustomK` |

**HTTP 层 — `testing/api/`（4 个测试）**

| 文件 | 测试函数 |
|------|---------|
| `handler_test.go` (4) | `TestHealthEndpoint`, `TestMemoryCRUD`, `TestCreateMemory_InvalidInput`, `TestRetrieve` |

### 10.3 手动 API 测试（curl 示例）

```bash
# 健康检查
curl http://localhost:8080/health

# 创建记忆（含新字段）
curl -X POST http://localhost:8080/v1/memories \
  -H 'Content-Type: application/json' \
  -d '{
    "content": "Go 1.25 新增了迭代器语法",
    "scope": "tech",
    "kind": "fact",
    "retention_tier": "long_term",
    "tags": ["golang", "language"]
  }'

# 摄取对话
curl -X POST http://localhost:8080/v1/conversations \
  -H 'Content-Type: application/json' \
  -d '{
    "context_path": "/chats/2026-03-18",
    "scope": "default",
    "messages": [
      {"role": "user", "content": "什么是 RRF 融合？"},
      {"role": "assistant", "content": "Reciprocal Rank Fusion 是一种将多个排序列表合并的算法..."}
    ]
  }'

# BM25 全文检索
curl -X POST http://localhost:8080/v1/retrieve \
  -H 'Content-Type: application/json' \
  -d '{"query": "Go 迭代器", "filters": {"scope": "tech"}, "limit": 10}'

# 强化记忆
curl -X POST http://localhost:8080/v1/memories/{id}/reinforce

# 清理过期记忆
curl -X POST http://localhost:8080/v1/maintenance/cleanup

# 时间线查询
curl "http://localhost:8080/v1/timeline?scope=tech&after=2026-03-01T00:00:00Z&limit=20"

# 获取对话记录
curl "http://localhost:8080/v1/conversations/{context_id}?limit=50"
```

### 10.4 数据库直接检查

```bash
# 查看迁移版本
sqlite3 data/iclude.db "SELECT * FROM schema_version;"

# 查看记忆总数
sqlite3 data/iclude.db "SELECT COUNT(*) FROM memories WHERE deleted_at IS NULL;"

# 验证 FTS5 正常
sqlite3 data/iclude.db "SELECT id, bm25(memories_fts, 10, 5, 3) AS score FROM memories m JOIN memories_fts f ON m.rowid = f.rowid WHERE memories_fts MATCH '关键词' LIMIT 5;"

# 查看表结构
sqlite3 data/iclude.db ".schema memories"

# 查看所有索引
sqlite3 data/iclude.db ".indexes memories"

# 检查 retention_tier 分布
sqlite3 data/iclude.db "SELECT retention_tier, COUNT(*) FROM memories GROUP BY retention_tier;"

# 查看 PRAGMA 设置
sqlite3 data/iclude.db "PRAGMA journal_mode; PRAGMA foreign_keys;"
```

---

## 十一、优化建议

### 高优先级

| # | 问题 | 现状 | 建议 |
|---|------|------|------|
| 1 | **FTS5 中文分词** | 使用 SQLite 默认 unicode61 分词器，中文按字拆分，检索效果差 | 接入 jieba HTTP 微服务作为外部分词器，或使用 simple tokenizer + 预分词管道 |
| 2 | **对话摄取无事务包装** | `IngestConversation` 逐条调用 `manager.Create()`，每条独立事务 | 包装在单事务中（`db.BeginTx`），批量插入提升 10-50x 性能 |
| 3 | **memory_tags/memory_entities 无外键约束** | 删除 memory 后关联表中残留孤儿记录 | 添加 `ON DELETE CASCADE` 或在 `SoftDelete`/`PurgeDeleted` 中级联清理 |

### 中优先级

| # | 问题 | 现状 | 建议 |
|---|------|------|------|
| 4 | **连接池未配置** | 未调用 `db.SetMaxOpenConns()` / `SetMaxIdleConns()`，使用 Go 默认值 | 设置合理上限（如 MaxOpen=25, MaxIdle=5, ConnMaxLifetime=5min） |
| 5 | **Qdrant payload 索引** | 未显式创建 payload index，大数据量时过滤扫描全量 | 为 scope/context_id/kind 等常用过滤字段创建 payload index |
| 6 | **SearchTextFiltered SQL 拼接** | 动态构建 WHERE 子句，字符串拼接可读性差 | 引入轻量 query builder（如 squirrel）或结构化条件构建器 |

### 低优先级

| # | 问题 | 现状 | 建议 |
|---|------|------|------|
| 7 | **FTS5 `bm25()` 重复调用** | SELECT 和 ORDER BY 各调一次 `bm25()`，理论上计算两次 | SQLite 内部可能优化，但可用子查询确保只计算一次 |
| 8 | **文档分块策略** | 固定 1000 字符 + 段落边界分块，不考虑语义完整性 | 后续按段落/语义分块，或根据文档类型自适应分块大小 |

---

## 十二、后续规划

### 已完成（Phase 1 追加）

- [x] V2→V3 迁移（retention_tier / message_role / turn_number）
- [x] BM25 加权搜索（bm25(memories_fts, 10, 5, 3) 替代 f.rank）
- [x] 对话摄取功能（conversations 端点）
- [x] 记忆强化端点（POST /v1/memories/:id/reinforce）
- [x] 过期清理端点（POST /v1/maintenance/cleanup）
- [x] retention_tier 生命周期支持（permanent 层不衰减）
- [x] FTS5 中文分词器（pkg/tokenizer — Simple/Jieba/Noop 三种实现，可拔插）
- [x] SearchTextFiltered SQL 构建器（pkg/sqlbuilder 替代字符串拼接）
- [x] 连接池配置（MaxOpen=25, MaxIdle=5, ConnMaxLifetime=5min）
- [x] 测试报告系统（pkg/testreport — 自动生成 HTML 可视化测试报告）

---

## 十三、Hindsight 竞品对比与借鉴规划

> 对比项目：[vectorize-io/hindsight](https://github.com/vectorize-io/hindsight)（仿生记忆系统，LongMemEval SOTA 91.4%）
> 对比日期：2026-03-19

### 13.1 核心差距总结

| 能力 | Hindsight | IClude 现状 | 差距等级 |
|------|-----------|-------------|---------|
| **Reflect 反思机制** | 对已有记忆做多步推理，自动生成新洞察 | 无 | 🔴 高 |
| **Retain 自动实体抽取** | 写入时 LLM 自动提取实体/关系/时间 | 手动 API 创建实体 | 🔴 高 |
| **图谱参与检索** | recall 时遍历实体图谱做关联查询 | GraphStore 仅 CRUD，不参与检索 | 🟡 中 |
| **Cross-encoder 重排** | RRF 后 cross-encoder 精排 | 仅 RRF，无重排 | 🟡 中 |
| **Token 感知裁剪** | 按 4096 token 预算裁剪结果 | 仅按条数 limit | 🟡 中 |
| **记忆自动演化** | 原始事实 → 经验 → 心智模型三层自动演化 | kind 是静态标签，不自动演化 | 🟡 中 |
| **Memory Bank 配置** | bank 可配 mission/directives/disposition 影响反思 | Context 无行为配置 | 🟢 低 |

### 13.2 IClude 独有优势（无需改动）

| 能力 | IClude | Hindsight |
|------|--------|-----------|
| 层级上下文树（物化路径） | ✅ | ❌ 扁平 bank |
| 文档处理管道（上传→分块→记忆化） | ✅ | ❌ |
| 多对多标签系统 | ✅ | ❌ |
| 完整对话摄取（turn_number） | ✅ | ❌ 仅逐条 retain |
| 软删除 + 恢复 | ✅ | ❌ |
| 本地优先（SQLite 零运维） | ✅ | ❌ 需 PostgreSQL |
| 5 级保留策略 + 指数衰减 | ✅ | ❌ |

### 13.3 借鉴落地计划

以下借鉴项已纳入 Phase 2~3 规划（详见产品概述文档阶段规划）：

| # | 借鉴项 | 优先级 | 目标 Phase | 实现思路 |
|---|--------|--------|-----------|---------|
| B1 | **Reflect 反思机制** | 🔴 P0 | Phase 2 | `internal/memory/reflect.go` — 召回相关记忆→调用外部 LLM 分析→生成 kind=mental_model 新记忆，与规划中"多轮思考型检索"合并设计 |
| B2 | **Retain 自动实体抽取** | 🔴 P0 | Phase 2 | Create 记忆时可选 `auto_extract: true`→调用 LLM 抽取实体/关系→自动写入 GraphStore |
| B3 | **图谱参与检索（三路 RRF）** | 🟡 P1 | Phase 2 | Retriever 增加第三路：查询→实体识别→图谱关联记忆→与 FTS5+Qdrant 三路 RRF 融合 |
| B4 | **Cross-encoder 重排** | 🟡 P1 | Phase 3 | 新增 `Reranker` 接口，RRF 后可选调用外部 LLM 或 cross-encoder 模型精排 |
| B5 | **Token 感知裁剪** | 🟡 P1 | Phase 2 | Retrieve 响应增加 `max_tokens` 参数，按 token 预算截断结果 |
| B6 | **记忆自动演化** | 🟡 P2 | Phase 3 | Reflect 后自动将洞察写回为 kind=mental_model，形成"事实→经验→心智模型"三层演化链 |
| B7 | **Memory Bank 行为配置** | 🟢 P2 | Phase 3 | Context metadata 增加 `mission`/`directives`/`disposition` 结构化字段，Reflect 时读取 |

### Phase 2 预留

- [ ] **Reflect 反思机制**（B1 — 多轮思考型检索 + 记忆反思融合设计）
- [ ] **Retain 自动实体抽取**（B2 — Create 时可选 auto_extract）
- [ ] **图谱参与检索**（B3 — 三路 RRF：FTS5 + Qdrant + Graph）
- [ ] **Token 感知裁剪**（B5 — max_tokens 参数）
- [ ] 对话摄取事务批量写入
- [ ] 文档 NER 自动实体提取（与 B2 合并）
- [ ] Context 树的权限控制
- [ ] 批量 Embedding 异步处理
- [ ] 记忆版本链（parent_id 版本追踪）
- [ ] detail_level 字段裁剪（abstract_only / summary / full）
- [ ] 多租户支持
- [ ] Qdrant payload index 优化

### Phase 3 预留

- [ ] **Cross-encoder 重排**（B4 — Reranker 接口）
- [ ] **记忆自动演化**（B6 — 三层记忆演化链）
- [ ] **Memory Bank 行为配置**（B7 — Context 行为配置）
- [ ] MCP Server 集成（让 Claude 等模型直接调用 IClude 记忆）
