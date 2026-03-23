# Layer 1: Identity & Ownership Design

## Summary

为 IClude 记忆系统引入身份认证与记忆归属机制，实现多租户场景下的记忆隔离与可见性控制。

**核心目标：** 解决"这是谁的记忆"和"谁能看到这条记忆"两个问题。

## Decision Log

| 决策 | 选择 | 理由 |
|------|------|------|
| 认证模式 | 信任调用方（API Key + X-User-ID） | IClude 是后端服务，调用方已有用户体系 |
| 归属模型 | owner_id + team_id + visibility 三层 | 支持个人空间 + 团队共享 + 公开知识库 |
| API Key 管理 | config.yaml 静态配置 | 当前阶段足够，后续可升级为数据库管理 |

## Architecture

### Request Flow

```
Client Request
  │
  ├─ Authorization: Bearer sk-xxx
  ├─ X-User-ID: alice
  │
  ▼
┌─────────────────────┐
│   AuthMiddleware     │  ← 验证 API Key，解析 team_id
│   (401 if invalid)   │
└──────────┬──────────┘
           ▼
┌─────────────────────┐
│  IdentityMiddleware  │  ← 提取 X-User-ID，组装 Identity
│  (set ctx identity)  │     未传则 owner_id = "anonymous"
└──────────┬──────────┘
           ▼
┌─────────────────────┐
│      Handler         │  ← 从 ctx 取 Identity
│  (auto-inject owner) │     创建时自动填 owner_id, team_id
└──────────┬──────────┘
           ▼
┌─────────────────────┐
│       Store          │  ← 所有查询强制 visibility 过滤
│  (enforced filtering)│
└─────────────────────┘
```

## Data Model Changes

### New Fields on `memories` Table

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `owner_id` | TEXT | `''` | 记忆创建者 ID，来自 X-User-ID Header |
| `visibility` | TEXT | `'private'` | 可见性级别：`private` / `team` / `public` |

### New Struct: `Identity`

```go
// Identity 请求身份信息 / Request identity context
type Identity struct {
    TeamID  string // 从 API Key 解析
    OwnerID string // 从 X-User-ID Header 提取
}
```

Location: `internal/model/identity.go`

### Memory Struct Changes

`internal/model/memory.go` 新增两个字段（放在 Lifecycle fields 区域之后）：

```go
// V6: 身份与归属 / Identity & Ownership
OwnerID    string `json:"owner_id,omitempty"`    // 创建者 ID
Visibility string `json:"visibility,omitempty"`  // private / team / public
```

### Request DTO Changes

`internal/model/request.go` 变更：

- `CreateMemoryRequest` 新增 `Visibility string` 字段（可选，默认 `private`）
- `UpdateMemoryRequest` 新增 `Visibility *string` 字段（允许更新可见性，如 private→team 提升）
  - `owner_id` 不可通过更新修改（归属不可转移）
- `SearchFilters` 新增 `OwnerID string` 和 `TeamID string` 字段（API 层自动注入，非用户传入）
- `RetrieveRequest` 中 `TeamID` 字段保留但不再从 query param 取值，改由中间件注入
- `ReflectRequest` 中 `TeamID` 字段保留但忽略请求体传入，由中间件注入
- `ExtractRequest` 中 `TeamID` 字段保留但忽略请求体传入，由中间件注入
- `TimelineRequest` 新增 `TeamID string` 和 `OwnerID string` 字段，供 visibility 过滤使用
- `ListRequest` 中 `TeamID` 字段保留但忽略请求体传入，由中间件注入（`List` 接口签名已改为接收 Identity）

### New Sentinel Errors

`internal/model/errors.go` 新增：

```go
var ErrUnauthorized = errors.New("authentication required")  // 401: API Key 缺失或无效
var ErrForbidden    = errors.New("access denied")            // 403: 无权访问该记忆
```

### System Identity (Internal Use)

内部系统操作（如 Consolidation、Scheduler 任务）使用系统级 Identity：

```go
// SystemIdentity 系统内部操作使用的身份 / Identity for internal system operations
// 可见所有非 private 记忆（team + public）
var SystemOwnerID = "__system__"
```

Visibility SQL 对系统 Identity 的特殊处理：

```sql
-- 当 owner_id = '__system__' 时，跳过 private 过滤，可见 team + public
AND (
    m.visibility = 'public'
    OR m.visibility = 'team'
    OR (m.visibility = 'private' AND m.owner_id = :owner_id)  -- __system__ 无法匹配任何 private 记忆
)
```

系统 Identity 天然无法看到 private 记忆（因为没有人的 owner_id 是 `__system__`），无需额外特殊逻辑。

## Config Changes

### Deprecation: `server.auth_enabled`

现有 `ServerConfig.AuthEnabled` (`server.auth_enabled`) 已弃用，由新的 `auth` 顶级配置节取代。

兼容策略：如果新 `auth` 节未配置但 `server.auth_enabled=true`，则日志输出 deprecation warning 并启用 auth（但无 API Key 列表则所有请求被拒绝）。

### `config.yaml` New Section

```yaml
auth:
  enabled: true                    # false 时跳过认证（开发模式）
  api_keys:
    - key: "sk-team-abc-xxxxx"
      team_id: "team-abc"
      name: "研发团队"
    - key: "sk-team-xyz-yyyyy"
      team_id: "team-xyz"
      name: "产品团队"
```

API Key 支持环境变量引用（Viper 原生支持），如：`key: "${MY_API_KEY}"`。

### Config Struct Changes

`internal/config/config.go` 变更：

```go
// Config 新增 Auth 字段
type Config struct {
    // ... existing fields ...
    Auth AuthConfig `mapstructure:"auth"`
}

type AuthConfig struct {
    Enabled bool         `mapstructure:"enabled"`
    APIKeys []APIKeyItem `mapstructure:"api_keys"`
}

type APIKeyItem struct {
    Key    string `mapstructure:"key"`
    TeamID string `mapstructure:"team_id"`
    Name   string `mapstructure:"name"`
}
```

## Middleware Design

### AuthMiddleware

Location: `internal/api/middleware.go`

```
逻辑：
1. 如果 auth.enabled = false → 注入默认 team_id="default" 并跳过
2. 从 Authorization Header 提取 Bearer token
3. 在 api_keys 内存 map 中查找匹配的 Key（O(1)）
4. 未找到 → 返回 401 Unauthorized（JSON 错误响应）
5. 找到 → 将对应 team_id 写入 gin.Context
```

Key 查找使用内存 `map[string]string`（key→team_id），启动时从 config 构建。

### IdentityMiddleware

Location: `internal/api/middleware.go`

```
逻辑：
1. 从 gin.Context 取 team_id（AuthMiddleware 已设置）
2. 从 X-User-ID Header 取 owner_id
3. 如果 X-User-ID 为空 → owner_id = "anonymous"
4. 构建 Identity{TeamID, OwnerID}，存入 gin.Context
```

**注意：** `anonymous` 用户的 `private` 记忆在同 team 内所有未传 X-User-ID 的请求都能看到。这是已知行为，因为调用方有责任传递 X-User-ID。文档中需明确说明此行为。

### Context Helper

```go
// GetIdentity 从请求上下文获取身份 / Get identity from request context
// 如果 Identity 不存在返回 nil（不 panic）
func GetIdentity(c *gin.Context) *model.Identity

// SetIdentity 将身份信息写入请求上下文 / Set identity into request context
func SetIdentity(c *gin.Context, id *model.Identity)
```

移除原设计中的 `MustGetIdentity`（避免 web server 中 panic）。Handler 中 `GetIdentity` 返回 nil 时返回 500 错误。

## Query-Level Enforced Filtering

### Core Visibility Rule

```sql
-- 用户能看到的记忆 / Memories visible to a user:
WHERE m.deleted_at IS NULL
  AND (
    m.visibility = 'public'                                              -- 公开：任何团队可见
    OR (m.team_id = :team_id AND m.visibility = 'team')                  -- 团队：同 team 可见
    OR (m.team_id = :team_id AND m.visibility = 'private' AND m.owner_id = :owner_id)  -- 私有：仅 owner
  )
```

> **注意：** `public` 记忆跨团队可见，适用于全局知识库场景。FTS5 查询路径依赖 JOIN 回主表 `memories` 做权限过滤（FTS5 虚表不含 owner_id/visibility 列）。

**不再支持 `team_id = ''` 穿透所有数据。**

开发模式（auth.enabled=false）下，中间件注入默认 Identity（team_id=`"default"`, owner_id=`"anonymous"`）。Migration V6 会将老数据 `team_id=''` 回填为 `"default"`，确保开发模式下数据可访问。

### Affected Store Interface Methods

以下 `MemoryStore` 接口方法需要签名变更：

| Method | Current Signature | New Signature |
|--------|-------------------|---------------|
| `List` | `List(ctx, teamID string, offset, limit int)` | `List(ctx, identity *model.Identity, offset, limit int)` |
| `SearchText` | `SearchText(ctx, query, teamID string, limit int)` | `SearchText(ctx, query string, identity *model.Identity, limit int)` |
| `ListByContext` | `ListByContext(ctx, contextID string, offset, limit int)` | `ListByContext(ctx, contextID string, identity *model.Identity, offset, limit int)` |
| `ListByContextOrdered` | `ListByContextOrdered(ctx, contextID string, offset, limit int)` | `ListByContextOrdered(ctx, contextID string, identity *model.Identity, offset, limit int)` |

以下方法通过已有结构体传递 Identity，不改签名：

| Method | Identity 来源 |
|--------|---------------|
| `SearchTextFiltered` | `SearchFilters` 新增 OwnerID + TeamID 字段 |
| `ListTimeline` | `TimelineRequest` 新增 TeamID + OwnerID 字段 |

以下方法需要新增权限校验：

| Method | 校验规则 |
|--------|----------|
| `Get` | 保持原签名（内部调用不过滤）。新增 `GetVisible(ctx, id, identity)` 供 API 层调用，加 visibility 过滤 |
| `SoftDelete` / `Restore` | 验证 owner_id 匹配 OR visibility='team' 且同 team（团队记忆团队成员可管理） |
| `Reinforce` | 验证 team_id 匹配（同团队成员可互相 reinforce） |

### VectorStore Interface Changes

`VectorStore` 接口签名变更：

| Method | Current Signature | New Signature |
|--------|-------------------|---------------|
| `Search` | `Search(ctx, embedding, teamID string, limit int)` | `Search(ctx, embedding, identity *model.Identity, limit int)` |
| `SearchFiltered` | 不改签名 | `SearchFilters` 已含 TeamID + OwnerID |

`Upsert` 时 payload 新增 `owner_id` 和 `visibility` 字段。

Search 和 SearchFiltered 的 Qdrant payload filter 新增可见性条件，与 SQLite 保持一致。

### Other Store Interfaces (Scope)

| Store Interface | Layer 1 改动 | 理由 |
|-----------------|-------------|------|
| `ContextStore` | 不改 | Context 是组织结构，不含敏感内容，通过 Memory 的 visibility 间接保护 |
| `TagStore` | 不改 | Tag 是全局分类标签，不需要 ownership |
| `GraphStore` | 不改 | Entity/Relation 通过关联的 Memory 间接保护。Layer 2 可加 scope 级隔离 |
| `DocumentStore` | Handler 层注入 team_id 过滤 | `List(ctx, scope, offset, limit)` 的 scope 参数由 Handler 从 Identity.TeamID 构造（如 `team/{teamID}`），接口签名不变 |

### Callers to Update (Complete List)

接口签名变更会导致以下调用方需要更新：

**internal/memory/:**
- `manager.go` — `List()`, `Create()` (注入 owner_id)
- `consolidation.go` — `selectCandidates()` 调用 `List(ctx, "", 0, ...)`
  - **策略：** Consolidation 使用系统级 Identity（team_id=目标团队, owner_id=`"__system__"`），可以看到该团队所有 team+public 记忆，天然无法访问 private 记忆

**internal/search/:**
- `retriever.go` — `SearchText()` 在 FTS 和 graph retrieve 中调用
  - **策略：** Retriever 接收 Identity 通过 RetrieveRequest 传入

**internal/api/:**
- 所有 handler 文件 — 从 `GetIdentity(c)` 获取 Identity 代替 query param

**cmd/:**
- `cmd/test-dashboard/testenv.go` — 测试环境调用 `List(ctx, "", 0, 1000)`
  - **策略：** 使用默认 Identity

**testing/:**
- `testing/store/`, `testing/report/`, `testing/memory/`, `testing/search/` 下所有相关测试文件

## Database Migration V6

> **重要：** V5 已被 `consolidated_into` 字段占用。本次迁移为 V5→V6。

Location: `internal/store/sqlite_migration.go`

```sql
-- V5→V6: 身份与归属 / Identity & Ownership
ALTER TABLE memories ADD COLUMN owner_id TEXT DEFAULT '';
ALTER TABLE memories ADD COLUMN visibility TEXT DEFAULT 'private';

-- 索引
CREATE INDEX IF NOT EXISTS idx_memories_owner_id ON memories(owner_id);
CREATE INDEX IF NOT EXISTS idx_memories_visibility ON memories(visibility);

-- 现有数据迁移
-- 1. 老数据设为 team 可见（向后兼容）
UPDATE memories SET visibility = 'team' WHERE owner_id = '';
-- 2. 空 team_id 回填为 'default'（确保开发模式下数据可访问）
UPDATE memories SET team_id = 'default' WHERE team_id = '';

-- 版本记录
INSERT INTO schema_version (version) VALUES (6);
```

`latestVersion` 从 5 更新为 6。

Migration 代码遵循现有模式：幂等（`ADD COLUMN` 忽略 duplicate column 错误）+ 事务包装。

**实现提醒：** `sqlite.go` 中的 `memoryColumns` 和 `memoryColumnsAliased` 常量需同步更新，从 32 列增至 34 列（新增 `owner_id`, `visibility`）。`scanMemory` 辅助函数也需对应更新。

## Handler Changes

### Auto-Injection on Create

`memory_handler.go` 的 `CreateMemory` 方法：

```
1. 从 ctx 获取 Identity
2. mem.OwnerID = identity.OwnerID      ← 强制覆盖，不信任请求体
3. mem.TeamID = identity.TeamID        ← 强制覆盖，不信任请求体
4. mem.Visibility = req.Visibility     ← 用户可选，默认 "private"
5. 校验 visibility 值合法性（private/team/public），非法值返回 400
```

**重要：** `owner_id` 和 `team_id` 由中间件决定，请求体中即使传了也会被覆盖。这是安全边界。

### Update Handler

`UpdateMemory` 方法：
- 允许更新 `visibility`（如将 private 记忆提升为 team 共享）
- **不允许** 更新 `owner_id`（归属不可转移）
- 需先校验当前用户有权修改该记忆（owner 或 team 管理者）

### All Handlers

所有 Handler 方法统一改为从 `GetIdentity(c)` 获取身份，不再从 query param 读取 `team_id`。

涉及的 handler 文件：
- `memory_handler.go` — CRUD + reinforce + soft-delete/restore
- `conversation_handler.go` — ingest + retrieve by context
- `document_handler.go` — upload/process/list
- `graph_handler.go` — entity/relation CRUD
- `tag_handler.go` — tag CRUD + associations
- `context_handler.go` — context tree management

## API Contract Changes

### Request Headers (New)

| Header | Required | Description |
|--------|----------|-------------|
| `Authorization` | Yes (when auth.enabled) | `Bearer <api_key>` |
| `X-User-ID` | No | 用户标识，未传默认 `anonymous` |

### Create Memory Request Body (Changed)

```json
{
  "content": "解决方案：重启服务后清理缓存",
  "visibility": "team",
  "kind": "skill",
  "tags": ["troubleshooting"]
}
```

- `team_id` 字段从请求体中移除（由 API Key 决定）
- 新增 `visibility` 可选字段（`private` / `team` / `public`，默认 `private`）

### Update Memory Request Body (Changed)

```json
{
  "visibility": "team"
}
```

- 新增 `visibility` 可选字段（允许变更可见性）
- `owner_id` 不可更新

### Search/Retrieve (Changed)

- `team_id` query param 移除（由 API Key 决定）
- Visibility 过滤自动应用，调用方无感

## Backward Compatibility

| 场景 | 处理策略 |
|------|----------|
| auth.enabled=false | 中间件注入默认 Identity(team_id="default", owner_id="anonymous") |
| 老数据无 owner_id | Migration V6: 设 visibility='team' + team_id 回填为 'default' |
| 请求未传 X-User-ID | owner_id='anonymous'，创建的记忆默认 private。**注意：** 同 team 内所有未传 X-User-ID 的请求共享同一个 anonymous 身份，彼此可见对方的 private 记忆。调用方有责任传递 X-User-ID 来实现真正的用户隔离 |
| 请求体仍传 team_id | 忽略，以 API Key 绑定的 team_id 为准 |
| server.auth_enabled (deprecated) | 日志 warning，映射到 auth.enabled |

## Not In Scope (Layer 1)

以下能力属于后续层级，本次不实现：

- **权限授予**（用户A授权用户B访问自己的 private 记忆）→ Layer 2
- **记忆审核/提升流程**（private → team 的审批工作流）→ Layer 2
- **质量反馈机制**（点赞/踩、验证标记）→ Layer 3
- **API Key 数据库管理**（动态 CRUD）→ 后续优化
- **细粒度 RBAC**（admin/editor/viewer 角色）→ 后续优化
- **API Key 轮换机制** → 后续优化

## Testing Strategy

测试文件位于 `testing/` 目录，按层级组织：

| 测试文件 | 覆盖范围 |
|----------|----------|
| `testing/store/migration_v6_test.go` | V6 迁移正确性 + 老数据兼容（team_id 回填、visibility 设置） |
| `testing/api/auth_middleware_test.go` | API Key 验证：有效 Key、无效 Key、缺失 Header、auth 关闭 |
| `testing/api/identity_middleware_test.go` | X-User-ID 提取、anonymous 降级、Identity 上下文传递 |
| `testing/store/visibility_test.go` | visibility 过滤：private 仅 owner 可见、team 同团队可见、public 跨团队可见、Get 加 team_id 校验 |
| `testing/report/identity_test.go` | testreport 集成测试（Dashboard 可视化），覆盖端到端认证+隔离流程 |

所有测试使用表驱动风格（table-driven）。
