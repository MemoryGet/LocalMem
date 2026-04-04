# LocalMem 数据库 Migration 详细草案

**Date**: 2026-04-04  
**Status**: Draft  
**Author**: Codex + user discussion

---

## 1. 文档目的

本文档是 [数据库与 Runtime 重构实施文档](./2026-04-04-database-runtime-refactor-plan.md) 的配套 migration 草案，目标是：

1. 按当前 SQLite migration 风格，设计可落地的增量迁移步骤
2. 避免一次大迁移带来的风险
3. 明确 fresh schema 与 incremental migration 的同步要求
4. 为后续代码实现提供精确的迁移顺序和 SQL 结构

---

## 2. 当前基础

截至当前仓库状态：

- 最新 schema 版本为 `17`
- `createFreshSchema()` 直接创建 V17 终态
- 增量迁移已覆盖 `V0 -> V17`

对应文件：

- [sqlite_migration.go](/root/LocalMem/internal/store/sqlite_migration.go)
- [sqlite_migration_v16_v20.go](/root/LocalMem/internal/store/sqlite_migration_v16_v20.go)
- [sqlite_schema.go](/root/LocalMem/internal/store/sqlite_schema.go)

---

## 3. 本轮目标版本规划

建议将本轮数据库扩展拆为以下 4 个 schema 版本：

| Migration | 目的 |
|----------|------|
| `V17 -> V18` | 新增 `sessions` |
| `V18 -> V19` | 新增 `session_finalize_state` |
| `V19 -> V20` | 新增 `transcript_cursors` |
| `V20 -> V21` | 新增 `idempotency_keys` |

### 为什么要拆成 4 步

1. 每步只引入一类新结构，易于排错
2. 回归测试时更容易定位失败原因
3. 方便后续若中途调整某一表结构，不会影响整个版本链

---

## 4. `latestVersion` 需要的改动

修改文件：

- [sqlite_migration.go](/root/LocalMem/internal/store/sqlite_migration.go)

当前：

- `latestVersion = 17`

重构后应改为：

- `latestVersion = 21`

同时在 `Migrate()` 中追加：

- `if version < 18 { migrateV17ToV18 }`
- `if version < 19 { migrateV18ToV19 }`
- `if version < 20 { migrateV19ToV20 }`
- `if version < 21 { migrateV20ToV21 }`

---

## 5. Fresh Schema 需要同步补齐的内容

修改文件：

- [sqlite_schema.go](/root/LocalMem/internal/store/sqlite_schema.go)

`createFreshSchema()` 不能只做旧 V17 结构，必须直接创建新终态。

### 5.1 需要新增的表

在 fresh schema 中新增：

- `sessions`
- `session_finalize_state`
- `transcript_cursors`
- `idempotency_keys`

### 5.2 需要新增的索引

必须在 fresh schema 中同步新增：

- `idx_sessions_context_id`
- `idx_sessions_project_state_last_seen`
- `idx_sessions_tool_started_at`
- `idx_sessions_state_last_seen`
- `idx_session_finalize_state_updated_at`
- `idx_transcript_cursors_last_read_at`
- `idx_idempotency_created_at`
- `idx_idempotency_scope_key_unique`

### 5.3 schema_version 记录

fresh schema 创建完成后记录版本：

- `INSERT INTO schema_version (version) VALUES (21)`

并将日志说明从：

- `fresh schema V17 created successfully`

改为：

- `fresh schema V21 created successfully`

---

## 6. V17 -> V18：新增 `sessions`

### 6.1 目的

引入显式 session 实体，承载宿主会话生命周期。

### 6.2 需要修改的文件

- [sqlite_migration_v16_v20.go](/root/LocalMem/internal/store/sqlite_migration_v16_v20.go)

如果你希望命名与文件内容一致，也可以新增：

- `internal/store/sqlite_migration_v20_v24.go`

但从当前项目风格看，继续在 `sqlite_migration_v16_v20.go` 或扩展现有迁移文件也是可以接受的。

### 6.3 建议 SQL

```sql
CREATE TABLE IF NOT EXISTS sessions (
    id            TEXT PRIMARY KEY,
    context_id    TEXT NOT NULL DEFAULT '',
    user_id       TEXT NOT NULL DEFAULT '',
    tool_name     TEXT NOT NULL DEFAULT '',
    project_id    TEXT NOT NULL DEFAULT '',
    project_dir   TEXT NOT NULL DEFAULT '',
    profile       TEXT NOT NULL DEFAULT '',
    state         TEXT NOT NULL DEFAULT 'created',
    started_at    DATETIME NOT NULL,
    last_seen_at  DATETIME NOT NULL,
    finalized_at  DATETIME,
    metadata      TEXT
);
```

### 6.4 建议索引

```sql
CREATE INDEX IF NOT EXISTS idx_sessions_context_id
ON sessions(context_id) WHERE context_id != '';

CREATE INDEX IF NOT EXISTS idx_sessions_project_state_last_seen
ON sessions(project_id, state, last_seen_at);

CREATE INDEX IF NOT EXISTS idx_sessions_tool_started_at
ON sessions(tool_name, started_at);

CREATE INDEX IF NOT EXISTS idx_sessions_state_last_seen
ON sessions(state, last_seen_at);
```

### 6.5 可选 FK

首版建议先不在增量迁移中强制加 FK 到 `contexts`，原因：

- 历史数据可能已有不一致
- SQLite 对 `ALTER TABLE` 补 FK 不够灵活

建议：

- fresh schema 中可直接定义 FK
- 旧库增量迁移先不补，后续单独做数据清洗 + rebuild migration

### 6.6 schema_version 更新

```sql
INSERT OR REPLACE INTO schema_version(version, applied_at)
VALUES (18, datetime('now'));
```

---

## 7. V18 -> V19：新增 `session_finalize_state`

### 7.1 目的

为 session ingest / finalize / summary / repair 建立终态控制点。

### 7.2 建议 SQL

```sql
CREATE TABLE IF NOT EXISTS session_finalize_state (
    session_id             TEXT PRIMARY KEY,
    ingest_version         INTEGER NOT NULL DEFAULT 0,
    finalize_version       INTEGER NOT NULL DEFAULT 0,
    conversation_ingested  INTEGER NOT NULL DEFAULT 0,
    summary_memory_id      TEXT NOT NULL DEFAULT '',
    last_error             TEXT NOT NULL DEFAULT '',
    updated_at             DATETIME NOT NULL DEFAULT (datetime('now'))
);
```

### 7.3 建议索引

```sql
CREATE INDEX IF NOT EXISTS idx_session_finalize_state_updated_at
ON session_finalize_state(updated_at);
```

### 7.4 可选 FK

同样建议：

- fresh schema 中定义 `REFERENCES sessions(id) ON DELETE CASCADE`
- 增量迁移先不强上

### 7.5 schema_version 更新

```sql
INSERT OR REPLACE INTO schema_version(version, applied_at)
VALUES (19, datetime('now'));
```

---

## 8. V19 -> V20：新增 `transcript_cursors`

### 8.1 目的

为 transcript 增量读取和 crash recovery 提供断点信息。

### 8.2 建议 SQL

```sql
CREATE TABLE IF NOT EXISTS transcript_cursors (
    session_id    TEXT NOT NULL,
    source_path   TEXT NOT NULL,
    byte_offset   INTEGER NOT NULL DEFAULT 0,
    last_turn_id  TEXT NOT NULL DEFAULT '',
    last_read_at  DATETIME NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (session_id, source_path)
);
```

### 8.3 建议索引

```sql
CREATE INDEX IF NOT EXISTS idx_transcript_cursors_last_read_at
ON transcript_cursors(last_read_at);
```

### 8.4 schema_version 更新

```sql
INSERT OR REPLACE INTO schema_version(version, applied_at)
VALUES (20, datetime('now'));
```

---

## 9. V20 -> V21：新增 `idempotency_keys`

### 9.1 目的

为 retain / ingest / finalize 建立统一幂等保障。

### 9.2 建议 SQL

```sql
CREATE TABLE IF NOT EXISTS idempotency_keys (
    scope         TEXT NOT NULL,
    idem_key      TEXT NOT NULL,
    resource_type TEXT NOT NULL DEFAULT '',
    resource_id   TEXT NOT NULL DEFAULT '',
    created_at    DATETIME NOT NULL DEFAULT (datetime('now'))
);
```

### 9.3 建议索引

```sql
CREATE UNIQUE INDEX IF NOT EXISTS idx_idempotency_scope_key_unique
ON idempotency_keys(scope, idem_key);

CREATE INDEX IF NOT EXISTS idx_idempotency_created_at
ON idempotency_keys(created_at);
```

### 9.4 schema_version 更新

```sql
INSERT OR REPLACE INTO schema_version(version, applied_at)
VALUES (21, datetime('now'));
```

---

## 10. fresh schema 中建议如何定义 FK

对于新库，建议直接在 fresh schema 里定义 FK，而不是后补。

### 10.1 `sessions`

建议：

```sql
context_id TEXT NOT NULL DEFAULT '' REFERENCES contexts(id)
```

如果担心空字符串与 FK 冲突，可以改成：

- 默认值为 `NULL`
- 代码层对应改用 `sql.NullString` 或逻辑空值处理

但考虑你现有风格大量使用空字符串，本轮可以先维持 `DEFAULT ''`，并**不在 fresh schema 第一版强加该 FK**。  
这是一种实用妥协。

### 10.2 `session_finalize_state`

建议：

```sql
session_id TEXT PRIMARY KEY REFERENCES sessions(id) ON DELETE CASCADE
```

### 10.3 `transcript_cursors`

建议：

```sql
session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE
```

### 10.4 `idempotency_keys`

无需 FK。

---

## 11. migration 函数建议长什么样

建议在迁移文件中新增以下函数：

- `migrateV17ToV18(db *sql.DB) error`
- `migrateV18ToV19(db *sql.DB) error`
- `migrateV19ToV20(db *sql.DB) error`
- `migrateV20ToV21(db *sql.DB) error`

风格上建议沿用现有模式：

1. `logger.Info("executing migration ...")`
2. `tx, err := db.Begin()`
3. `defer tx.Rollback()`
4. 执行建表与索引 SQL
5. 插入 `schema_version`
6. `tx.Commit()`
7. `logger.Info("migration ... completed")`

---

## 12. 兼容性与数据迁移说明

### 12.1 本轮无需搬迁旧业务数据

本轮新增的 4 张表都是新增运行时结构，不需要迁移 `memories` 历史内容。

因此：

- 不需要全表扫描旧 `memories`
- 不需要回填 `contexts`
- 不需要重写 FTS

### 12.2 可选回填项

如果你后续想把旧的 session context 补成显式 `sessions`，可以另做后台任务：

- 读取 `contexts.context_type = session`
- 从 `metadata` 中提取旧 session_id / project_dir
- 回填到 `sessions`

但这不应成为本轮 migration 的阻塞项。

---

## 13. 与 `async_tasks` 的关系

本轮 migration 不改 `async_tasks` 表结构。

原因：

- 现有 `async_tasks` 已够承载新增任务类型
- 本轮重点是补 session/runtime 主数据

后续如果需要更强任务幂等或分组能力，再考虑：

- 增加 `task_key`
- 增加 `group_id`
- 增加 `session_id`

但不应和本轮绑在一起。

---

## 14. 需要同步修改的日志与注释

修改后还要同步修正：

### 14.1 注释

- `createFreshSchema` 中 “V16 final state” / “35 columns” 等表述
- `latestVersion` 相关注释
- migration 文件标题注释

### 14.2 日志

- `fresh schema created`
- `migration completed`

确保日志版本号一致，不要出现 schema 已到 21 但日志还写 17 的情况。

---

## 15. 推荐实现顺序

### 第一步

改：

- [sqlite_migration.go](/root/LocalMem/internal/store/sqlite_migration.go)

内容：

- `latestVersion = 21`
- 加入 `V17 -> V21` 的调用链

### 第二步

改：

- [sqlite_schema.go](/root/LocalMem/internal/store/sqlite_schema.go)

内容：

- 新 fresh schema 直接创建 4 张新表和索引
- `schema_version` 改为 21

### 第三步

改：

- `sqlite_migration_v16_v20.go` 或新增后续 migration 文件

内容：

- 实现 4 个 migration 函数

### 第四步

补测试：

- 新库 fresh schema 测试
- 从 V17 增量迁移到 V21 的测试
- migration 幂等测试

---

## 16. 推荐新增测试

建议新增：

- `testing/store/migration_v17_v21_test.go`
- `testing/store/fresh_schema_v21_test.go`

必测场景：

1. 空库初始化直接得到 V21
2. V17 旧库增量迁移到 V21 成功
3. 新增 4 张表存在
4. 新增索引存在
5. 重复调用 `Migrate()` 幂等

---

## 17. 实施后的预期状态

完成本轮 migration 后，数据库层应具备：

1. 显式 session 实体
2. finalize / ingest 状态记录
3. transcript 增量读取游标
4. 幂等键持久层
5. fresh schema 与 migration version 保持一致

这将为下一步代码层重构提供稳定地基。

---

## 18. 下一步建议

本 migration 草案完成后，下一份最该补的是：

**最小改动接口草案（Go types + services + wiring）**

这样你后面就可以一边做 DB migration，一边有明确的代码改造边界。

