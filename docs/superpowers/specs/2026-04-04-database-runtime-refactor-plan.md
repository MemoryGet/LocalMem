# LocalMem 数据库与 Runtime 重构实施文档

**Date**: 2026-04-04  
**Status**: Draft  
**Author**: Codex + user discussion

---

## 1. 文档目的

本文档用于指导 LocalMem 后续数据库与代码重构，目标是：

1. 在**不推翻现有主结构**的前提下，补齐多工具接入所需的 session/runtime 基础设施
2. 保持 **SQLite 为主真相源**，Qdrant 为**可选派生向量层**
3. 为未来的 `Codex / Claude Code / Cursor / Cline` 统一接入提供数据库与代码落点
4. 控制改动范围，避免破坏 `MemoryStore + Manager + Retriever + MCP` 现有主链路

本文档重点覆盖两部分：

- 数据库重构改动
- 代码重构改动

---

## 2. 当前系统现状

基于当前仓库代码，现状如下：

### 2.1 已经正确的部分

- SQLite 已经承担主存储职责：
  - `memories`
  - `contexts`
  - `documents`
  - `tags`
  - `memory_tags`
  - `entities`
  - `memory_entities`
  - `entity_relations`
  - `memory_derivations`
  - `async_tasks`
- Qdrant 当前是可选向量层，不是唯一真相源
- `Manager.Create()` 当前是：
  - 先写 SQLite
  - 再 best-effort 写 Qdrant
- `memory_derivations` 已采用 junction table，方向正确
- `memories` 已具备绝大多数长期保留的业务字段

### 2.2 当前缺口

当前系统还缺少面向多工具 runtime 的显式结构：

- 缺独立 `sessions` 表
- 缺 `session finalize` 状态表
- 缺 transcript cursor 表
- 缺 idempotency 表
- Qdrant 写入还不是标准 outbox/queue 模式
- finalize / repair / transcript replay 没有单独的持久层建模

---

## 3. 重构总原则

### 3.1 必须坚持的原则

1. **SQLite 是唯一主真相源**
2. **Qdrant 只做派生索引，不做主存储**
3. **不大改 `memories` 主表**
4. **不把 session/runtime 状态塞回 `contexts.metadata` 长期承载**
5. **不把 session/finalize/idempotency 硬塞进 `MemoryStore` 主接口**

### 3.2 改动控制原则

1. 保持 `MemoryStore` 稳定
2. 用新增 store 接口承载新增职责
3. 通过新增 runtime/service 层，隔离新逻辑
4. 优先通过 migration 增量演进，不做 destructive schema rewrite

---

## 4. 现有表与字段：哪些保留不动

以下结构建议保留，不作为本轮重构对象。

### 4.1 保留的核心表

- `memories`
- `contexts`
- `documents`
- `tags`
- `memory_tags`
- `entities`
- `memory_entities`
- `entity_relations`
- `memory_derivations`
- `async_tasks`

### 4.2 `memories` 中保留不动的核心字段

以下字段已经符合目标方向，应继续保留：

- `context_id`
- `scope`
- `kind`
- `sub_kind`
- `source_type`
- `source_ref`
- `document_id`
- `retention_tier`
- `message_role`
- `turn_number`
- `content_hash`
- `strength`
- `decay_rate`
- `last_accessed_at`
- `reinforced_count`
- `memory_class`
- `consolidated_into`
- `owner_id`
- `visibility`

说明：

- 不建议本轮拆分 `memories`
- 不建议把 `derived_from` 改回 JSON 列
- 不建议引入新的“第二主表”替代 `memories`

---

## 5. 数据库必须新增的表

本轮建议新增 4 张表。

## 5.1 `sessions`

### 作用

显式表示一个宿主工具运行中的会话，而不是长期依赖 `contexts` 间接表达。

### 建议字段

| Field | Type | Notes |
|------|------|-------|
| `id` | TEXT PK | session id |
| `context_id` | TEXT | 绑定 LocalMem context |
| `user_id` | TEXT | 逻辑用户 ID |
| `tool_name` | TEXT | `codex` / `claude-code` / `cursor` / `cline` |
| `project_id` | TEXT | 稳定项目 ID |
| `project_dir` | TEXT | 启动目录 |
| `profile` | TEXT | `A/B/C/D` profile |
| `state` | TEXT | `created/bootstrapped/active/finalizing/finalized/pending_repair/abandoned` |
| `started_at` | DATETIME | 创建时间 |
| `last_seen_at` | DATETIME | 最近活跃时间 |
| `finalized_at` | DATETIME NULL | finalize 成功时间 |
| `metadata` | TEXT | JSON 扩展字段 |

### 建议索引

- `idx_sessions_context_id`
- `idx_sessions_project_state_last_seen`
- `idx_sessions_tool_started_at`
- `idx_sessions_state_last_seen`

---

## 5.2 `session_finalize_state`

### 作用

管理 finalize / ingest / summary / repair 的终态与幂等状态。

### 建议字段

| Field | Type | Notes |
|------|------|-------|
| `session_id` | TEXT PK | FK -> sessions.id |
| `ingest_version` | INTEGER | transcript ingest 版本 |
| `finalize_version` | INTEGER | finalize 版本 |
| `conversation_ingested` | INTEGER | 0/1 |
| `summary_memory_id` | TEXT | 生成的 summary memory |
| `last_error` | TEXT | 最后错误 |
| `updated_at` | DATETIME | 最近更新时间 |

### 建议索引

- `idx_session_finalize_state_updated_at`

---

## 5.3 `transcript_cursors`

### 作用

支持 transcript 增量读取、断点续读和 repair replay。

### 建议字段

| Field | Type | Notes |
|------|------|-------|
| `session_id` | TEXT | FK -> sessions.id |
| `source_path` | TEXT | transcript 文件路径或逻辑源 |
| `byte_offset` | INTEGER | 已读到的字节位置 |
| `last_turn_id` | TEXT | 最近 turn id |
| `last_read_at` | DATETIME | 最近读取时间 |

### 主键 / 索引

- 主键：`(session_id, source_path)`
- 索引：`idx_transcript_cursors_last_read_at`

---

## 5.4 `idempotency_keys`

### 作用

为 `retain / ingest / finalize` 提供强幂等控制。

### 建议字段

| Field | Type | Notes |
|------|------|-------|
| `scope` | TEXT | 例如 `retain` / `ingest` / `finalize` |
| `idem_key` | TEXT | 幂等键 |
| `resource_type` | TEXT | `memory/session/summary` |
| `resource_id` | TEXT | 绑定资源 ID |
| `created_at` | DATETIME | 创建时间 |

### 主键 / 索引

- 唯一索引：`(scope, idem_key)`
- 索引：`idx_idempotency_created_at`

---

## 6. 数据库建议补的 FK 和索引

以下建议按风险和兼容性逐步推进。

### 6.1 本轮优先补的 FK

- `sessions.context_id -> contexts.id`
- `session_finalize_state.session_id -> sessions.id`
- `transcript_cursors.session_id -> sessions.id`

### 6.2 第二阶段建议补的 FK

- `memories.context_id -> contexts.id`
- `memories.document_id -> documents.id`

说明：

- 第二阶段再补，是因为这会影响现有数据兼容和迁移复杂度
- 若历史脏数据较多，应先做数据清洗再加 FK

### 6.3 本轮优先补的索引

- `sessions(context_id)`
- `sessions(project_id, state, last_seen_at)`
- `sessions(tool_name, started_at)`
- `session_finalize_state(session_id)`
- `transcript_cursors(session_id, source_path)`
- `idempotency_keys(scope, idem_key)` unique

---

## 7. 数据库暂时不要动的部分

以下内容本轮不要动：

1. 不拆 `memories` 主表
2. 不把 `contexts` 与 `sessions` 合并
3. 不把 `derived_from` 改回 `memories` 列
4. 不将 SQLite 替换为 PostgreSQL
5. 不把 Qdrant 升级为主存储
6. 不做 Qdrant 集群化设计

---

## 8. 代码层重构总策略

### 8.1 不改动的主干接口

以下接口保持不动，避免大面积联动：

- `MemoryStore`
- `VectorStore`
- `ContextStore`
- `DocumentStore`
- `TagStore`
- `GraphStore`

对应文件：

- [interfaces.go](/root/LocalMem/internal/store/interfaces.go)

### 8.2 新增并行接口，不污染 `MemoryStore`

本轮新增 4 组小接口：

1. `SessionStore`
2. `SessionFinalizeStore`
3. `TranscriptCursorStore`
4. `IdempotencyStore`

这些接口只服务于 runtime/session/finalize，不服务 memory CRUD。

---

## 9. 需要新增的 model

建议新增以下 model 文件：

### 9.1 新增文件

- `internal/model/session.go`
- `internal/model/session_finalize.go`
- `internal/model/transcript_cursor.go`
- `internal/model/idempotency.go`

### 9.2 建议新增结构

#### `model.Session`

- `ID`
- `ContextID`
- `UserID`
- `ToolName`
- `ProjectID`
- `ProjectDir`
- `Profile`
- `State`
- `StartedAt`
- `LastSeenAt`
- `FinalizedAt`
- `Metadata`

#### `model.SessionFinalizeState`

- `SessionID`
- `IngestVersion`
- `FinalizeVersion`
- `ConversationIngested`
- `SummaryMemoryID`
- `LastError`
- `UpdatedAt`

#### `model.TranscriptCursor`

- `SessionID`
- `SourcePath`
- `ByteOffset`
- `LastTurnID`
- `LastReadAt`

#### `model.IdempotencyRecord`

- `Scope`
- `IdemKey`
- `ResourceType`
- `ResourceID`
- `CreatedAt`

---

## 10. 需要新增的 store 接口

修改文件：

- [interfaces.go](/root/LocalMem/internal/store/interfaces.go)

建议新增如下接口。

### 10.1 `SessionStore`

建议方法：

- `Create(ctx context.Context, s *model.Session) error`
- `Get(ctx context.Context, id string) (*model.Session, error)`
- `UpdateState(ctx context.Context, id, state string, lastError string) error`
- `Touch(ctx context.Context, id string, ts time.Time) error`
- `ListPendingFinalize(ctx context.Context, olderThan time.Duration, limit int) ([]*model.Session, error)`

### 10.2 `SessionFinalizeStore`

- `Get(ctx context.Context, sessionID string) (*model.SessionFinalizeState, error)`
- `Upsert(ctx context.Context, st *model.SessionFinalizeState) error`
- `MarkIngested(ctx context.Context, sessionID string, version int) error`
- `MarkFinalized(ctx context.Context, sessionID string, version int, summaryMemoryID string) error`

### 10.3 `TranscriptCursorStore`

- `Get(ctx context.Context, sessionID, sourcePath string) (*model.TranscriptCursor, error)`
- `Upsert(ctx context.Context, c *model.TranscriptCursor) error`
- `DeleteBySession(ctx context.Context, sessionID string) error`

### 10.4 `IdempotencyStore`

- `Reserve(ctx context.Context, scope, key, resourceType string) (bool, error)`
- `BindResource(ctx context.Context, scope, key, resourceID string) error`
- `Get(ctx context.Context, scope, key string) (*model.IdempotencyRecord, error)`

---

## 11. 需要新增的 SQLite store 实现

建议新增文件：

- `internal/store/sqlite_session.go`
- `internal/store/sqlite_session_finalize.go`
- `internal/store/sqlite_transcript_cursor.go`
- `internal/store/sqlite_idempotency.go`

这些文件应复用 `RawDB` / SQLite 主库，不新增独立数据库。

---

## 12. 需要修改的数据库 schema / migration 文件

### 12.1 必改文件

- [sqlite_schema.go](/root/LocalMem/internal/store/sqlite_schema.go)
- [sqlite_migration.go](/root/LocalMem/internal/store/sqlite_migration.go)
- [sqlite_migration_v16_v20.go](/root/LocalMem/internal/store/sqlite_migration_v16_v20.go)

### 12.2 需要新增的内容

在 fresh schema 中新增：

- `sessions`
- `session_finalize_state`
- `transcript_cursors`
- `idempotency_keys`

在 migration 中新增对应版本：

- V20 -> V21: add `sessions`
- V21 -> V22: add `session_finalize_state`
- V22 -> V23: add `transcript_cursors`
- V23 -> V24: add `idempotency_keys`

说明：

- 版本号只是建议，具体可根据当前 schema version 调整
- 每个 migration 应尽量只做一类结构变化

---

## 13. `store/factory` 与 bootstrapping 改动

### 13.1 修改文件

- [factory.go](/root/LocalMem/internal/store/factory.go)
- [wiring.go](/root/LocalMem/internal/bootstrap/wiring.go)

### 13.2 `Stores` 结构需要新增字段

在 `store.Stores` 中新增：

- `SessionStore`
- `SessionFinalizeStore`
- `TranscriptCursorStore`
- `IdempotencyStore`

### 13.3 `InitStores()` 需要新增逻辑

从 `RawDB` 构造：

- `NewSQLiteSessionStore(db)`
- `NewSQLiteSessionFinalizeStore(db)`
- `NewSQLiteTranscriptCursorStore(db)`
- `NewSQLiteIdempotencyStore(db)`

### 13.4 `bootstrap.Deps` 需要新增字段

建议新增：

- `SessionService`
- `FinalizeService`
- `RuntimeRepairService`

不建议把这些职责塞进 `MemManager`。

---

## 14. 新增 service / runtime 层

这是本轮最重要的代码结构变化。

### 14.1 建议新增目录

```text
internal/runtime/
internal/session/
```

二选一即可，不要两套并存。

建议优先：

```text
internal/runtime/
```

### 14.2 建议新增文件

- `internal/runtime/session_service.go`
- `internal/runtime/finalize_service.go`
- `internal/runtime/repair_service.go`
- `internal/runtime/launcher.go`
- `internal/runtime/types.go`

### 14.3 这些 service 的职责

#### `SessionService`

- 创建 session
- 绑定 `session_id -> context_id`
- 更新 session state
- heartbeat touch

#### `FinalizeService`

- 执行 `ingest_conversation`
- 执行 `finalize_session`
- 管理 summary memory
- 管理 idempotency

#### `RepairService`

- 查询 pending finalize sessions
- 读取 transcript cursor
- 重放 transcript
- 补执行 ingest/finalize

#### `Launcher`

- 用于 `iclude launch <tool>`
- 包装无原生 hook 的工具

---

## 15. `MemoryManager` 需要改动什么，不需要改动什么

### 15.1 不建议改动的部分

- 不在 `Manager` 中新增 session/finalize/transcript 的职责
- 不改 `Manager` 的主接口语义
- 不把 `SessionStore` 直接注入 `Manager`

### 15.2 可以做的小改动

仅做低侵入优化：

- 把 Qdrant upsert、excerpt backfill、session finalize 逐步迁移到 queue 驱动
- 继续保持 `Manager.Create()` 的 memory 主链路稳定

修改文件：

- [manager.go](/root/LocalMem/internal/memory/manager.go)

---

## 16. `async_tasks` / queue 的重构改动

### 16.1 当前可复用部分

当前已有：

- `async_tasks`
- `queue.Queue`
- `queue.Worker`

对应文件：

- [queue.go](/root/LocalMem/internal/queue/queue.go)
- [worker.go](/root/LocalMem/internal/queue/worker.go)
- [handler.go](/root/LocalMem/internal/queue/handler.go)

### 16.2 建议新增任务类型

- `vector_upsert`
- `excerpt_backfill`
- `conversation_ingest`
- `session_finalize`
- `session_repair`

### 16.3 代码改动点

- 扩展任务类型定义
- 增加 handler
- 将 runtime repair/finalize 接到 queue

---

## 17. MCP 层需要改动的内容

### 17.1 新增 MCP 工具

建议新增：

- `iclude_finalize_session`

可选新增：

- `iclude_get_session`
- `iclude_touch_session`

### 17.2 需要修改的文件

- [cmd/mcp/main.go](/root/LocalMem/cmd/mcp/main.go)
- `internal/mcp/tools/finalize_session.go`（新增）
- `internal/mcp/registry.go`（如需要）

### 17.3 适配器层改动

`cmd/mcp/main.go` 当前注册：

- `retain`
- `recall`
- `reflect`
- `ingest_conversation`
- `timeline`
- `scan`
- `fetch`
- `create_session`

重构后应补：

- `finalize_session`

并将其接到新增的 `FinalizeService`，而不是直接绑 `MemoryManager`。

---

## 18. CLI / Hook 层需要改动的内容

### 18.1 修改文件

- [cmd/cli/main.go](/root/LocalMem/cmd/cli/main.go)
- [hook_session_start.go](/root/LocalMem/cmd/cli/hook_session_start.go)
- [hook_capture.go](/root/LocalMem/cmd/cli/hook_capture.go)
- [hook_session_stop.go](/root/LocalMem/cmd/cli/hook_session_stop.go)

### 18.2 需要新增的能力

#### `session-start`

- 创建或绑定 `sessions` 记录
- 保存 `context_id`
- 初始化 `transcript cursor`（如有 transcript）

#### `capture`

- 重要事件 retain 时生成 idempotency key
- 写 observation 时带 `session_id`、`tool_name`、`capture_mode`

#### `session-stop`

- 不只 retain summary
- 改为调用 `finalize_session`
- finalize 失败时标记 pending repair

### 18.3 建议新增 CLI 子命令

- `iclude launch <tool>`
- `iclude repair sessions`

---

## 19. API 层需要改动的内容

当前 API 已有 session handler，但更偏传统 API 视角，不是 runtime 语义。

### 19.1 可能需要修改的文件

- [session_handler.go](/root/LocalMem/internal/api/session_handler.go)
- [router.go](/root/LocalMem/internal/api/router.go)

### 19.2 建议新增的 API

- `POST /v1/sessions`
- `GET /v1/sessions/:id`
- `POST /v1/sessions/:id/finalize`
- `POST /v1/sessions/:id/repair`

说明：

- MCP 是 AI 工具入口
- API 可以给插件、launcher、桌面桥接层使用

---

## 20. 搜索与检索层改动

### 20.1 原则

检索层不需要大改。

### 20.2 仅需确认的内容

- `scope priority` 未来需兼容 `session/*`
- 会话 finalize 后，不应破坏原有 retrieval 逻辑

可能涉及文件：

- `internal/search/retriever.go`
- `internal/search/experience_recall.go`

### 20.3 本轮不建议做的事情

- 不把检索逻辑与 session runtime 深度耦合
- 不为了 session 表引入检索层大重构

---

## 21. Qdrant 相关重构改动

### 21.1 当前状态

当前路径正确但不够稳：

- SQLite 成功后同步 best-effort upsert Qdrant

### 21.2 目标状态

改为：

1. 先写 SQLite
2. 通过 `async_tasks` 触发 `vector_upsert`
3. Qdrant 失败不影响主事务
4. 可通过 repair/reindex 重建

### 21.3 需要改动的文件

- [manager.go](/root/LocalMem/internal/memory/manager.go)
- [qdrant.go](/root/LocalMem/internal/store/qdrant.go)
- queue 相关文件

### 21.4 本轮不做

- 不做 Qdrant 集群设计
- 不做多向量 schema 扩展
- 不做 Qdrant 作为主查询真相源

---

## 22. 测试层改动

### 22.1 建议新增测试目录

```text
testing/runtime/
testing/session/
testing/compliance/
```

### 22.2 建议新增测试文件

- `testing/store/session_store_test.go`
- `testing/store/idempotency_store_test.go`
- `testing/store/transcript_cursor_store_test.go`
- `testing/runtime/finalize_service_test.go`
- `testing/runtime/repair_service_test.go`
- `testing/mcp/finalize_session_tool_test.go`
- `testing/cli/launch_test.go`

### 22.3 必测场景

- create session
- repeated finalize idempotent
- repeated ingest idempotent
- transcript cursor replay
- pending repair recovery
- project/session scope isolation

---

## 23. 推荐实施顺序

建议按以下顺序做，风险最低。

### Phase 1: Schema 增量扩展

1. 新增 `sessions`
2. 新增 `session_finalize_state`
3. 新增 `transcript_cursors`
4. 新增 `idempotency_keys`

### Phase 2: Store 与 Model

1. 新增 model
2. 新增 store interface
3. 新增 SQLite store 实现
4. 接入 `store.Stores`

### Phase 3: Runtime Service

1. 新增 `SessionService`
2. 新增 `FinalizeService`
3. 新增 `RepairService`

### Phase 4: MCP / CLI 接入

1. 新增 `iclude_finalize_session`
2. CLI stop hook 改走 finalize
3. 增加 launcher 原型

### Phase 5: Queue 化与修复能力

1. vector upsert queue 化
2. finalize queue 化
3. repair worker

### Phase 6: Compliance Tests

1. 新增 L1-L4 合规测试
2. 验证 Claude Code
3. 验证 Codex / Cline / Cursor

---

## 24. 高风险点与规避方式

### 风险 1：直接改 `MemoryStore`

后果：

- 大面积联动 `Manager` / `Retriever` / API / MCP / document pipeline

规避：

- 新增并行 store 接口，不改 `MemoryStore`

### 风险 2：把 session 继续塞在 `contexts.metadata`

后果：

- finalize、repair、idempotency 状态越来越难查和维护

规避：

- 独立 `sessions` 表

### 风险 3：Qdrant 仍走同步 best-effort

后果：

- 向量失败不可恢复
- 难以 audit

规避：

- 逐步 queue 化

### 风险 4：一次性补所有 FK

后果：

- 历史数据脏值导致迁移失败

规避：

- 先补新表 FK，再分阶段清洗旧数据

---

## 25. 本轮明确“不做”的范围

为避免重构失控，本轮明确不做：

1. PostgreSQL 迁移
2. Qdrant 集群化
3. `memories` 表拆分
4. retrieval ranking 重写
5. graph schema 重构
6. MCP 协议层大改

---

## 26. 最终重构结果应达到的状态

完成本轮后，系统应满足：

1. SQLite 仍是唯一主真相源
2. Qdrant 完全可选且可重建
3. Session lifecycle 可显式建模
4. finalize/repair 可持久追踪
5. transcript 增量读取可恢复
6. `retain / ingest / finalize` 有幂等保障
7. `MemoryStore` 主干接口保持稳定
8. Claude/Codex/Cursor/Cline 接入都有明确 runtime 落点

---

## 27. 文件改动清单总览

### 27.1 必改文件

- [internal/store/sqlite_schema.go](/root/LocalMem/internal/store/sqlite_schema.go)
- [internal/store/sqlite_migration.go](/root/LocalMem/internal/store/sqlite_migration.go)
- [internal/store/sqlite_migration_v16_v20.go](/root/LocalMem/internal/store/sqlite_migration_v16_v20.go)
- [internal/store/interfaces.go](/root/LocalMem/internal/store/interfaces.go)
- [internal/store/factory.go](/root/LocalMem/internal/store/factory.go)
- [internal/bootstrap/wiring.go](/root/LocalMem/internal/bootstrap/wiring.go)
- [cmd/mcp/main.go](/root/LocalMem/cmd/mcp/main.go)
- [cmd/cli/hook_session_start.go](/root/LocalMem/cmd/cli/hook_session_start.go)
- [cmd/cli/hook_capture.go](/root/LocalMem/cmd/cli/hook_capture.go)
- [cmd/cli/hook_session_stop.go](/root/LocalMem/cmd/cli/hook_session_stop.go)
- [internal/memory/manager.go](/root/LocalMem/internal/memory/manager.go)
- queue 相关文件

### 27.2 建议新增文件

- `internal/model/session.go`
- `internal/model/session_finalize.go`
- `internal/model/transcript_cursor.go`
- `internal/model/idempotency.go`
- `internal/store/sqlite_session.go`
- `internal/store/sqlite_session_finalize.go`
- `internal/store/sqlite_transcript_cursor.go`
- `internal/store/sqlite_idempotency.go`
- `internal/runtime/session_service.go`
- `internal/runtime/finalize_service.go`
- `internal/runtime/repair_service.go`
- `internal/runtime/launcher.go`
- `internal/mcp/tools/finalize_session.go`
- `testing/store/session_store_test.go`
- `testing/store/idempotency_store_test.go`
- `testing/runtime/finalize_service_test.go`
- `testing/runtime/repair_service_test.go`

---

## 28. 下一步建议

后续应继续补两份文档：

1. **数据库 migration 详细草案**
2. **最小改动接口草案（带 Go type 定义）**

前者用于实际执行迁移，后者用于控制代码改动范围。

