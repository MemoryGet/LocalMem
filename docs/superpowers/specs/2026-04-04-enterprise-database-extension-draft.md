# LocalMem 企业版新增表设计草案

**Date**: 2026-04-04  
**Status**: Draft  
**Author**: Codex + user discussion

---

## 1. 文档目的

本文档只基于**当前仓库已存在的数据库字段、认证方式和 runtime 结构**，整理企业版最值得新增的数据库对象。

本文档目标：

1. 明确企业版应新增哪些表
2. 说明这些表如何与当前已有结构衔接
3. 避免为企业版重做一套平行 schema
4. 控制企业版数据库扩展范围，优先补最关键的 4 组能力

本文档不覆盖：

- PostgreSQL 物理迁移细节
- 管理后台页面设计
- 计费系统
- 完整 RBAC/ABAC 全量实现

---

## 2. 当前数据库与认证现实

当前仓库已经具备以下企业能力相关基础：

### 2.1 已存在的身份/隔离相关字段

- `memories.team_id`
- `memories.owner_id`
- `memories.visibility`
- `sessions.user_id`
- `sessions.project_id`
- `sessions.tool_name`

### 2.2 已存在的认证/身份注入方式

当前配置层已有：

- `auth.api_keys[]`
- 每个 API key 绑定 `team_id`

定义位置：

- [config.go](/root/LocalMem/internal/config/config.go)

当前 API middleware 已支持：

- Bearer API key
- 注入 `team_id`
- 通过 `X-User-ID` 注入 `owner_id`

定义位置：

- [middleware.go](/root/LocalMem/internal/api/middleware.go)

当前 MCP server 已支持：

- 单一 `mcp.api_token`
- `default_team_id`
- `default_owner_id`

定义位置：

- [server.go](/root/LocalMem/internal/mcp/server.go)

### 2.3 当前缺口

企业版真正缺的是：

1. 没有 `tenant`
2. 没有数据库里的 API key / service account 主体
3. 没有 audit log
4. 没有角色或策略绑定表

因此企业版新增表应优先围绕这四类缺口展开。

---

## 3. 企业版扩展原则

### 3.1 不重做 `memories`

企业版不应新增第二张“企业记忆主表”。

继续沿用：

- `memories`
- `contexts`
- `documents`
- `sessions`

### 3.2 不让 `team_id` 独自承担租户概念

当前 `team_id` 已存在，但语义不够完整。

企业版应新增：

- `tenant_id`

然后让：

- `tenant_id` 成为顶层隔离边界
- `team_id` 继续表示团队或逻辑分组

### 3.3 新增表优先服务四件事

企业版第一批新增表只围绕：

1. 租户
2. 成员/主体
3. 凭据
4. 审计

---

## 4. 第一批建议新增的表

建议优先新增这 6 张表：

1. `tenants`
2. `tenant_members`
3. `service_accounts`
4. `api_keys`
5. `roles`
6. `audit_logs`

如果想再收敛一步，也可以先做前 4 张，把 `roles` 留到下一阶段。

---

## 5. `tenants`

## 5.1 作用

定义企业版顶层租户边界。

### 为什么必须新增

因为当前：

- `team_id` 只是字段，不是顶层租户对象
- 无法表达企业客户、组织、工作区的正式归属

## 5.2 建议字段

| Field | Type | Notes |
|------|------|-------|
| `id` | TEXT PK | tenant id |
| `name` | TEXT | 企业/组织名 |
| `slug` | TEXT UNIQUE | 外部可读标识 |
| `status` | TEXT | `active/suspended/deleted` |
| `metadata` | TEXT | JSON 扩展 |
| `created_at` | DATETIME | 创建时间 |
| `updated_at` | DATETIME | 更新时间 |

## 5.3 建议索引

- `idx_tenants_slug_unique`
- `idx_tenants_status`

## 5.4 与当前结构的关系

企业版后续应逐步把以下对象纳入 tenant 视角：

- `team_id`
- `project_id`
- `api_keys`
- `service_accounts`

---

## 6. `tenant_members`

## 6.1 作用

记录用户与租户之间的成员关系。

### 为什么需要

当前只有：

- `owner_id` 自由字符串

这不足以表达：

- 用户是否属于某租户
- 用户在租户中的角色
- 用户是否被禁用

## 6.2 建议字段

| Field | Type | Notes |
|------|------|-------|
| `tenant_id` | TEXT | FK -> tenants.id |
| `user_id` | TEXT | 当前系统中的逻辑用户标识 |
| `role` | TEXT | `owner/admin/editor/viewer` |
| `status` | TEXT | `active/invited/suspended` |
| `joined_at` | DATETIME | 加入时间 |
| `updated_at` | DATETIME | 更新时间 |

## 6.3 主键与索引

- 主键：`(tenant_id, user_id)`
- 索引：`idx_tenant_members_user_id`
- 索引：`idx_tenant_members_tenant_role`

## 6.4 与当前结构的关系

当前的：

- `owner_id`

在企业版中应能够映射到：

- 某个 `tenant_members.user_id`

---

## 7. `service_accounts`

## 7.1 作用

表示非人类主体，如：

- 后端服务
- 集成代理
- 自动化 worker
- 企业内部平台调用者

### 为什么需要

当前仓库只有：

- 配置级 API key
- `default_owner_id`

这不足以表达真正的机器身份。

## 7.2 建议字段

| Field | Type | Notes |
|------|------|-------|
| `id` | TEXT PK | service account id |
| `tenant_id` | TEXT | FK -> tenants.id |
| `name` | TEXT | 服务账号名称 |
| `status` | TEXT | `active/disabled` |
| `description` | TEXT | 说明 |
| `created_by` | TEXT | 创建者 user_id |
| `created_at` | DATETIME | 创建时间 |
| `updated_at` | DATETIME | 更新时间 |

## 7.3 建议索引

- `idx_service_accounts_tenant_id`
- `idx_service_accounts_status`

## 7.4 与当前结构的关系

未来企业版中，`owner_id` 不应只代表人类用户，也可代表：

- `user:<user_id>`
- `svc:<service_account_id>`

这样才能和现有 `owner_id` 字段平滑衔接。

---

## 8. `api_keys`

## 8.1 作用

把当前配置文件中的 API key 能力迁移成数据库里的正式凭据对象。

### 为什么需要

当前 API key 配置在：

- [config.go](/root/LocalMem/internal/config/config.go)

这种方式适合个人版和轻量部署，但不适合企业版：

- 不能轮换
- 不能审计
- 不能数据库管理
- 不能和 service account 绑定

## 8.2 建议字段

| Field | Type | Notes |
|------|------|-------|
| `id` | TEXT PK | key id |
| `tenant_id` | TEXT | FK -> tenants.id |
| `service_account_id` | TEXT | FK -> service_accounts.id, nullable |
| `name` | TEXT | 凭据名称 |
| `key_prefix` | TEXT | 仅保存前缀用于展示 |
| `key_hash` | TEXT | 存储哈希，不存明文 |
| `team_id` | TEXT | 兼容当前 team 语义 |
| `status` | TEXT | `active/revoked` |
| `created_by` | TEXT | 创建者 user_id |
| `last_used_at` | DATETIME | 最近使用时间 |
| `expires_at` | DATETIME | 过期时间 |
| `created_at` | DATETIME | 创建时间 |

## 8.3 建议索引

- `idx_api_keys_tenant_id`
- `idx_api_keys_service_account_id`
- `idx_api_keys_key_hash_unique`
- `idx_api_keys_status`
- `idx_api_keys_last_used_at`

## 8.4 与当前结构的关系

企业版应从：

- 配置文件 `auth.api_keys[]`

逐步迁移为：

- 数据库 `api_keys`

并在 middleware 中改为：

- 通过 `key_hash` 查凭据
- 注入 `tenant_id`
- 注入 `team_id`
- 注入 `owner/service account`

---

## 9. `roles`

## 9.1 作用

为后续更正式的授权体系提供角色定义。

### 为什么建议做

当前只有：

- `visibility`
- `team_id`

但没有角色。

企业版一旦出现：

- admin
- editor
- viewer
- service agent

就需要显式角色表。

## 9.2 建议字段

| Field | Type | Notes |
|------|------|-------|
| `id` | TEXT PK | role id |
| `tenant_id` | TEXT | nullable，支持系统角色与租户角色 |
| `name` | TEXT | 角色名 |
| `description` | TEXT | 描述 |
| `permissions` | TEXT | JSON 列，存基础权限集合 |
| `created_at` | DATETIME | 创建时间 |
| `updated_at` | DATETIME | 更新时间 |

## 9.3 建议索引

- `idx_roles_tenant_id`
- `idx_roles_name_tenant_unique`

说明：

如果你想更收敛，`roles` 可以放到第二阶段，不一定和前四张表一起做。

---

## 10. `audit_logs`

## 10.1 作用

记录企业版最关键的可审计事件。

### 为什么必须新增

当前数据库没有系统级审计表。

企业版至少要知道：

- 谁创建了什么
- 谁读取了什么
- 谁删除/恢复了什么
- 谁 finalize 了 session
- 哪个 key / service account 发起的调用

## 10.2 建议字段

| Field | Type | Notes |
|------|------|-------|
| `id` | TEXT PK | audit event id |
| `tenant_id` | TEXT | FK -> tenants.id |
| `actor_type` | TEXT | `user/service_account/system` |
| `actor_id` | TEXT | user_id 或 service_account_id |
| `api_key_id` | TEXT | nullable |
| `action` | TEXT | 如 `memory.create`, `memory.read`, `session.finalize` |
| `resource_type` | TEXT | `memory/context/document/session` |
| `resource_id` | TEXT | 资源 id |
| `scope` | TEXT | 相关 scope |
| `status` | TEXT | `success/denied/error` |
| `metadata` | TEXT | JSON 附加信息 |
| `created_at` | DATETIME | 事件时间 |

## 10.3 建议索引

- `idx_audit_logs_tenant_created_at`
- `idx_audit_logs_actor`
- `idx_audit_logs_action`
- `idx_audit_logs_resource`

## 10.4 最小事件集建议

第一批只审计：

- `memory.create`
- `memory.read`
- `memory.delete`
- `memory.restore`
- `session.create`
- `session.finalize`
- `api_key.authenticate`

---

## 11. 企业版新增表与当前表的关系映射

### 11.1 与 `memories`

企业版不改主表结构为新主表，但应逐步补强语义：

- `owner_id` 绑定真实 actor
- `team_id` 绑定租户内团队
- `visibility` 与角色策略联动

### 11.2 与 `sessions`

企业版中的 `sessions` 建议新增 tenant 过滤能力，但第一阶段不一定要加列。

可以先通过：

- `user_id`
- `project_id`
- `tool_name`

结合外部 tenant 解析做逻辑归属。

### 11.3 与 `idempotency_keys`

企业版仍可复用当前表，不必重做。

必要时后续增强：

- 增加 `tenant_id`
- 增加 `expires_at`

但不建议作为第一批新增表的一部分。

### 11.4 与当前 API / MCP 认证

当前：

- API middleware 通过 Bearer token 找 `config.Auth.APIKeys`
- MCP 通过单一 `mcp.api_token`

企业版目标：

- API / MCP 都统一到数据库凭据模型

---

## 12. 推荐的新增优先级

为了不干扰当前个人版主线，建议这样分批：

### Batch 1

必须先做：

1. `tenants`
2. `tenant_members`
3. `service_accounts`
4. `api_keys`

### Batch 2

在 Batch 1 稳定后再做：

5. `audit_logs`

### Batch 3

授权体系成熟后再做：

6. `roles`

---

## 13. 当前不要做的企业版数据库动作

以下内容不建议和第一批新增表一起做：

1. 全量 PostgreSQL 迁移
2. 给所有现有表强制补 `tenant_id`
3. 一次性引入完整 RBAC/ABAC 全模型
4. 给所有读操作都立刻加审计
5. 立即重构 Qdrant 为企业主向量层

原因：

- 当前个人版 runtime 主链路还在完善
- 企业版应以最小新增表方式先建立顶层边界

---

## 14. 最小可实施版本

如果只做企业版数据库最小起步，我建议先只落这 4 张表：

- `tenants`
- `tenant_members`
- `service_accounts`
- `api_keys`

这样你就能先把：

- 租户边界
- 成员关系
- 凭据主体

三件最本质的企业能力补起来。

`audit_logs` 和 `roles` 可以紧跟其后，但不需要强绑第一版。

---

## 15. 最终结论

基于当前仓库现实，企业版数据库最合理的第一步不是重做主 schema，而是新增：

1. `tenants`
2. `tenant_members`
3. `service_accounts`
4. `api_keys`
5. `audit_logs`
6. `roles`

其中最优先的是前四张。  
它们能和当前已有的：

- `owner_id`
- `team_id`
- `visibility`
- `sessions`
- `idempotency_keys`

自然衔接，而不会破坏当前个人版主线。

