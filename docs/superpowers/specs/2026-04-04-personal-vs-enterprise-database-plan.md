# LocalMem 个人版与企业版数据库规划

**Date**: 2026-04-04  
**Status**: Draft  
**Author**: Codex + user discussion

---

## 1. 文档目的

本文档只基于**当前仓库已经存在的数据库结构与代码**，规划：

1. 当前数据库如何定义为“个人版”
2. 在当前数据库基础上，企业版应如何扩展
3. 哪些表和字段个人版继续保留
4. 哪些表和字段在企业版中需要增强
5. 企业版需要新增哪些数据库对象

本文档不讨论：

- 纯抽象产品分层
- 与当前代码无关的理想化企业平台设计
- 立即切换 PostgreSQL 的实施细节

---

## 2. 当前数据库现状（基于仓库实际结构）

截至当前仓库状态，SQLite schema 中已存在以下表：

- `memories`
- `contexts`
- `tags`
- `memory_tags`
- `entities`
- `entity_relations`
- `memory_entities`
- `documents`
- `async_tasks`
- `memory_derivations`
- `sessions`
- `session_finalize_state`
- `transcript_cursors`
- `idempotency_keys`
- `schema_version`
- `meta`

定义位置：

- [sqlite_schema.go](/root/LocalMem/internal/store/sqlite_schema.go)

这说明当前数据库已经不是“只有记忆内容表”的阶段，而是已经具备：

- 记忆主存储
- 上下文管理
- 文档与图谱
- 异步任务
- runtime session 基础设施

---

## 3. 当前数据库的个人版定义

### 3.1 当前个人版的定位

按当前数据库结构，个人版应定义为：

- **SQLite 主库**
- **Qdrant 可选向量层**
- **单实例、本地优先**
- **不强调真正多租户**
- **不引入复杂组织权限模型**

### 3.2 当前个人版已经具备的数据库能力

#### 记忆主存储

- `memories`
- `memory_derivations`
- `tags / memory_tags`
- `entities / entity_relations / memory_entities`

#### 上下文与文档

- `contexts`
- `documents`

#### 运行时基础设施

- `sessions`
- `session_finalize_state`
- `transcript_cursors`
- `idempotency_keys`
- `async_tasks`

### 3.3 当前个人版的主隔离方式

当前个人版最合理的主隔离维度是：

- `owner_id`
- `scope`
- `context_id`

而不是：

- 真正的 `tenant_id`
- 复杂组织树

### 3.4 当前个人版中可直接作为主模型使用的字段

`memories` 表中，以下字段已经足够支撑个人版主逻辑：

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
- `owner_id`
- `visibility`

### 3.5 个人版中应视为“已存在但弱使用”的字段

这些字段个人版中存在，但不是主卖点：

#### `team_id`

现状：

- 已存在于 `memories`
- 已进入 Qdrant payload

个人版用法：

- 可以保留
- 不建议作为主隔离维度使用

#### `visibility`

现状：

- `private / team / public`

个人版用法：

- 适合轻量访问控制
- 但还不是完整权限系统

#### `owner_id`

现状：

- 已存在

个人版用法：

- 可作为单用户/单设备身份标识
- 还不是完整企业身份体系的一部分

#### `sessions`

现状：

- 数据库层已存在
- store 层已存在
- 运行时主流程尚未完全切过去

个人版用法：

- 这是个人版 runtime 的基础设施
- 但仍属于“基础已具备、主流程待接通”

---

## 4. 个人版建议保留的表

以下表建议作为个人版正式保留结构，不再区分“实验表”和“企业专属表”：

### 4.1 个人版核心表

- `memories`
- `contexts`
- `documents`
- `sessions`
- `session_finalize_state`
- `transcript_cursors`
- `idempotency_keys`
- `async_tasks`

### 4.2 个人版增强表

- `tags`
- `memory_tags`
- `entities`
- `entity_relations`
- `memory_entities`
- `memory_derivations`

说明：

- 这些表对个人版不是负担
- 当前 schema 已经具备
- 删除它们只会造成后续能力回退

---

## 5. 个人版中当前不要动的部分

为了避免过度设计，个人版当前不建议做以下改动：

1. 不拆 `memories` 主表
2. 不将 `contexts` 与 `sessions` 合并
3. 不为了企业版提前把所有表 tenant 化
4. 不将 SQLite 替换为 PostgreSQL
5. 不将 Qdrant 升级为必选组件
6. 不提前引入复杂 RBAC schema

---

## 6. 企业版与当前个人版的核心差异

企业版不应从零重做一套平行 schema。  
正确路线应是：

- **个人版作为基础层**
- **企业版在当前 schema 上增强**

企业版与个人版最核心的区别，不是“记忆表长什么样”，而是以下 5 点：

### 6.1 隔离维度不同

个人版：

- `owner_id + scope + context_id`

企业版：

- `tenant_id + team_id + owner_id + visibility + scope`

### 6.2 主库定位不同

个人版：

- SQLite 主库

企业版：

- PostgreSQL 更适合作为生产主库

### 6.3 向量层定位不同

个人版：

- Qdrant 可选
- 可单机、可关闭

企业版：

- 独立向量服务更合理
- 需要正式运维和重建策略

### 6.4 权限模型不同

个人版：

- `visibility` 做轻量控制

企业版：

- 需要真正的角色/策略系统

### 6.5 运维与审计要求不同

个人版：

- 单机可恢复

企业版：

- 备份、审计、权限追踪、租户隔离都必须显式建模

---

## 7. 企业版建议新增的数据库对象

以下内容当前仓库中**没有**，但企业版应在当前结构上新增。

## 7.1 租户与成员关系

### 建议新增表

- `tenants`
- `tenant_members`
- `tenant_projects` 或项目归属表

### 作用

- 表达真正多租户边界
- 绑定用户、团队、项目和租户之间的关系

### 为什么当前需要新增

因为当前库中：

- 没有 `tenant_id`
- `team_id` 无法完整替代租户模型

---

## 7.2 企业身份与凭据体系

### 建议新增表

- `service_accounts`
- `api_keys`
- 可选：`oauth_identities`

### 作用

- 明确机器身份与人类身份
- 支撑真正的服务级访问控制
- 替代当前主要依赖配置文件 token 的方式

---

## 7.3 权限与角色体系

### 建议新增表

- `roles`
- `role_bindings`
- 或 `access_policies`

### 作用

- 让 `visibility` 从静态字段升级为可执行策略的一部分

---

## 7.4 审计体系

### 建议新增表

- `audit_logs`

### 建议至少记录

- 谁读了什么
- 谁写了什么
- 谁删除/恢复了什么
- 谁 finalize 了 session
- 哪个凭据或 service account 做了操作

---

## 7.5 企业设置与保留策略

### 建议新增表

- `tenant_settings`
- `project_settings`
- `retention_policies`

### 作用

- 将 retention、共享、写入策略从纯代码配置提升为数据库配置能力

---

## 8. 企业版中哪些现有字段会变成强约束

以下字段当前已经存在，但到了企业版中，不应再只是“兼容字段”。

### 8.1 `team_id`

当前状态：

- 已存在但弱使用

企业版状态：

- 应作为团队维度的重要过滤字段
- 或被 `tenant_id + team_id` 组合体系吸收

### 8.2 `owner_id`

当前状态：

- 更接近自由身份字符串

企业版状态：

- 必须绑定真实身份体系
- 必须可追溯到用户或服务账号

### 8.3 `visibility`

当前状态：

- 轻量控制字段

企业版状态：

- 应与角色/策略系统联动
- 不能单独承担全部授权逻辑

### 8.4 `project_id`

当前状态：

- 主要体现在 runtime/session 层

企业版状态：

- 应成为正式项目管理与隔离的一部分

---

## 9. 企业版中建议增强但不必立即重构的现有表

这些表在企业版中仍可继续复用，不需要重做。

### 9.1 `memories`

继续保留，重点增强：

- 租户隔离
- 权限过滤
- 审计路径

不建议：

- 为企业版重做第二套记忆主表

### 9.2 `contexts`

继续保留，重点增强：

- 组织级访问控制
- 项目/租户归属

### 9.3 `sessions`

继续保留，重点增强：

- 审计
- 租户/项目过滤
- 后台运维查询能力

### 9.4 `idempotency_keys`

继续保留，重点增强：

- 过期清理策略
- 企业级 finalize / ingest 幂等控制

### 9.5 `async_tasks`

继续保留，重点增强：

- 任务审计
- 分组与租户归属
- 运维可观测性

---

## 10. 企业版数据库不建议现在就做的事

以下内容即使面向企业版，也不建议在当前阶段就急着做：

1. 立即强行将所有表改造为 PostgreSQL only
2. 立即引入全量多租户重构
3. 立即将所有 current schema 强制加 tenant FK
4. 立即引入复杂分布式任务表体系
5. 立即引入 Qdrant 集群强依赖

原因：

- 当前个人版 runtime 主链路尚未完全接通
- 过早进入企业化重构会稀释主线进度

---

## 11. 个人版与企业版差异清单

## 11.1 表级差异

### 当前个人版保留表

- `memories`
- `contexts`
- `documents`
- `tags`
- `memory_tags`
- `entities`
- `entity_relations`
- `memory_entities`
- `memory_derivations`
- `async_tasks`
- `sessions`
- `session_finalize_state`
- `transcript_cursors`
- `idempotency_keys`

### 企业版新增表

- `tenants`
- `tenant_members`
- `tenant_projects`
- `service_accounts`
- `api_keys`
- `roles`
- `role_bindings` 或 `access_policies`
- `audit_logs`
- `tenant_settings`
- `project_settings`
- `retention_policies`

---

## 11.2 字段级差异

### 当前已存在、个人版直接使用

- `scope`
- `context_id`
- `kind`
- `source_type`
- `document_id`
- `retention_tier`
- `message_role`
- `content_hash`
- `memory_class`

### 当前已存在、个人版弱使用、企业版强使用

- `owner_id`
- `team_id`
- `visibility`

### 企业版建议新增的关键字段（分布在新旧表中）

- `tenant_id`
- `service_account_id`
- `actor_type`
- `actor_id`
- `policy_id`
- `role_id`

---

## 11.3 部署级差异

### 个人版

- SQLite 主库
- Qdrant 可选
- 单机部署

### 企业版

- PostgreSQL 更合适
- Qdrant 独立服务更合理
- 正式备份/迁移/运维体系

---

## 12. 推荐演进顺序

建议按以下顺序推进：

### Phase 1

先把当前个人版 runtime 主链路接通：

- `SessionService`
- `FinalizeService`
- `RepairService`
- `iclude_finalize_session`

### Phase 2

在个人版上完成最小 compliance 与运行时稳定性验证。

### Phase 3

再开始企业版数据库增强：

- `tenant`
- `api_keys`
- `service_accounts`
- `audit_logs`

### Phase 4

最后再做：

- PostgreSQL 主库迁移
- 独立向量服务策略
- 企业级运维/审计增强

---

## 13. 最终结论

### 当前个人版

当前数据库已经足以定义为：

- SQLite 主库
- Qdrant 可选
- 单机本地优先
- `owner_id + scope + context_id` 为主隔离方式
- `team_id / visibility` 保留但轻量使用
- 已具备 session/runtime 基础设施

### 企业版

企业版不应重做一套完全不同的数据库。  
正确路线应是：

- 在当前个人版 schema 基础上增强
- 新增租户、凭据、角色、审计相关表
- 将 `team_id / owner_id / visibility` 升级为强约束字段
- 最终将主库演进到 PostgreSQL，更适合多租户与企业交付

---

## 14. 一句话总结

当前数据库已经足够支撑一个完整的**个人版本地记忆系统**；  
企业版应在其上增加**租户、凭据、角色、审计**四类核心结构，而不是另起一套平行 schema。

