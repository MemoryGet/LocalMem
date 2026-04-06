# 项目标识稳定化 + 团队记忆共享 + Scope 权限管控

**Date**: 2026-04-05
**Status**: Approved

---

## 1. 要解决的问题

1. **project_id 不稳定**：当前 `SHA256(绝对路径)` 生成，同一个 git 仓库 clone 到不同路径会产生不同 ID，多人协作时项目记忆互不可见
2. **git remote 格式不统一**：SSH/HTTPS/带 .git 后缀等不同格式 clone 同一个仓库会产生不同 hash
3. **fork 仓库 origin 不同**：fork 用户的 origin 指向自己的仓库，与上游 project_id 不一致
4. **visibility 形同虚设**：hook 写入的记忆全部 `private`，团队成员无法共享项目级知识
5. **scope 自动填充缺失**：大量记忆的 scope 是 `"default"`，scope priority、core injection 等机制无法生效
6. **scope 无权限管控**：任何人都能往任何 project scope 写入
7. **retain 工具不知道当前项目**：MCP retain 无法从 session 上下文推导 project scope
8. **scope 降级无用户感知**：权限不足时 scope 被降级但调用方不知道

---

## 2. project_id 生成策略

### 2.1 优先级链

```
1. .iclude.yaml 中的 project_id       → 手动 override
2. git remote URL 的 normalized hash   → 跨机器稳定
3. 目录绝对路径的 SHA256               → 非 git fallback
4. 空                                  → 日常会话模式
```

### 2.2 git remote 优先级

```
upstream > origin
```

fork 工作流中 upstream 指向上游仓库，两人解析到同一个 URL。纯 fork 没配 upstream 则视为独立项目。

### 2.3 URL Normalization

hash 前将不同格式统一为 `host/owner/repo`（全小写）：

```
git@github.com:MemoryGet/LocalMem.git      → github.com/memoryget/localmem
https://github.com/MemoryGet/LocalMem.git   → github.com/memoryget/localmem
ssh://git@github.com/MemoryGet/LocalMem     → github.com/memoryget/localmem
ssh://git@github.com:2222/MemoryGet/LocalMem → github.com/memoryget/localmem
```

规则：去掉协议前缀、`git@`、`.git` 后缀、端口号，`:` 替换为 `/`，全部小写，然后 `SHA256` 取前 6 字节。

### 2.4 项目模式判断

CWD 下存在 `.git/` 或 `.iclude.yaml` → 项目模式（scope=`project/{id}`），否则 → 日常会话模式（scope=`user/{owner_id}`）。

### 2.5 .iclude.yaml

放在项目根目录，可选，仅放标识：

```yaml
project_id: "my-backend"
```

不放权限、不放人员名单。权限由服务端管控。

---

## 3. Scope 自动分配（两层分层）

### 3.1 底层：确定性来源规则（无 LLM 也工作）

| 调用方 | 条件 | scope |
|--------|------|-------|
| hook capture | CWD 在项目中 | `project/{project_id}` |
| hook capture | CWD 不在项目中 | `user/{owner_id}` |
| hook session-stop | 会话摘要 | `session/{session_id}` |
| MCP retain（scope 为空） | 当前 session 关联了项目 | `project/{project_id}`（从 session context 读取） |
| MCP retain（scope 为空） | 无项目关联 | `user/{owner_id}` |
| MCP retain（scope 非空） | 调用方指定 | 原样使用 |
| REST API（scope 为空） | 同 MCP retain 规则 | 同上 |
| reflect/consolidation | 系统内部产出 | 继承源记忆的 scope |
| `agent/*` | 暂不自动填充 | 仅手动指定时使用 |

### 3.2 上层：AI 引导（有 AI 工具时更精准）

**session-start 输出增加项目上下文**：

```
Project scope: project/p_a1b2c3
User scope: user/local-user
```

**retain 工具描述引导 AI 选择 scope**：

```
scope 选择规则：
- 用户个人偏好/习惯 → "user/{owner_id}"
- 当前项目相关知识/决策/架构 → 使用 session-start 中给出的 project scope
- 不确定时 → 不传，系统根据会话上下文自动判断
```

AI 传了 scope → 用 AI 的判断。AI 没传 → fallback 到 3.1 的来源规则。

### 3.3 MCP Session 注入 project scope

`create_session` 被调用时传入 `project_id` 和 `scope`，存储到 `mcp.Session` 的 `projectScope` 字段。后续所有 MCP 工具调用通过 context 读取：

```
create_session(project_id="p_abc", scope="project/p_abc")
    → session.projectScope = "project/p_abc"

retain(content="...", scope="")
    → 读 ctx 中的 session.projectScope → 使用 "project/p_abc"
```

实现：`mcp.Session` 新增 `projectScope string` 字段 + `WithProjectScope(ctx)` / `ProjectScopeFromContext(ctx)` 上下文方法。

---

## 4. Visibility 自动填充

仅当调用方未显式指定 visibility 时生效：

| scope 前缀 | kind | 默认 visibility | 理由 |
|-----------|------|----------------|------|
| `project/*` | `observation` | private | 过程噪音，只对作者有用 |
| `project/*` | 其他（fact/decision/note/skill/session_summary/core...） | team | 有价值的知识沉淀，团队共享 |
| `user/*` | 任意 | private | 个人偏好不泄漏 |
| `session/*` | 任意 | private | 会话过程隔离 |
| `agent/*` | 任意 | private | agent 私有 |

逻辑放在 `Manager.Create()` 中。

---

## 5. Scope 权限管控

### 5.1 scope_policies 表

| 字段 | 类型 | 说明 |
|------|------|------|
| `id` | TEXT PK | UUID |
| `scope` | TEXT UNIQUE | scope 路径，如 `project/p_a1b2c3` |
| `display_name` | TEXT | 可读名称，如 "my-backend" |
| `team_id` | TEXT | 所属团队 |
| `allowed_writers` | TEXT | JSON 数组 `["alice","bob"]` |
| `created_by` | TEXT | 创建者 owner_id |
| `created_at` | DATETIME | 创建时间 |
| `updated_at` | DATETIME | 更新时间 |

### 5.2 写入校验规则

```
写入 scope=project/xxx 时：
1. 查 scope_policies 表
2. 无记录 → 允许（向后兼容，未配置=不限制）
3. 有记录但 allowed_writers 为空 → 允许（空白名单=不限制）
4. 有记录且 allowed_writers 非空：
   - 当前 owner_id 在列表中 → 允许
   - 不在列表中 → scope 降级为 user/{owner_id}，visibility 改为 private
```

降级而不是拒绝——保证记忆不丢失，只是变成个人记忆。

### 5.3 降级响应

retain 响应中增加降级信息：

```json
{
  "id": "mem_xxx",
  "scope_downgraded": true,
  "requested_scope": "project/p_abc",
  "actual_scope": "user/bob",
  "reason": "not in allowed_writers for project/p_abc"
}
```

未降级时 `scope_downgraded` 字段不出现，向后兼容。

### 5.4 API 端点

| 端点 | 方法 | 说明 |
|------|------|------|
| `/v1/scope-policies` | GET | 列出所有 scope 策略（同 team_id） |
| `/v1/scope-policies` | POST | 创建策略（调用者成为 created_by） |
| `/v1/scope-policies/:scope` | GET | 获取指定 scope 策略 |
| `/v1/scope-policies/:scope` | PUT | 更新策略（仅 created_by） |
| `/v1/scope-policies/:scope` | DELETE | 删除策略（仅 created_by） |

### 5.5 API 鉴权

| 操作 | 权限 |
|------|------|
| 创建策略 | 任何认证用户（自动成为 created_by） |
| 查看策略 | 同 team_id 的所有成员 |
| 修改/删除策略 | 仅 created_by 本人 |

校验逻辑：`if policy.CreatedBy != identity.OwnerID { return 403 }`。

---

## 6. 改动文件清单

### 新增文件

| 文件 | 说明 |
|------|------|
| `internal/model/scope_policy.go` | ScopePolicy model |
| `internal/store/sqlite_scope_policy.go` | SQLite 实现 |
| `internal/api/scope_policy_handler.go` | CRUD handler + 鉴权 |
| `testing/store/scope_policy_test.go` | store 层测试 |
| `testing/api/scope_policy_test.go` | API 层测试（含鉴权） |

### 修改文件

| 文件 | 改动 |
|------|------|
| `pkg/identity/project_id.go` | 优先级链 + git remote 读取 + URL normalize + .iclude.yaml 读取 |
| `internal/memory/manager.go` | Create() 中按 scope+kind 自动填充 visibility + scope 降级逻辑 |
| `internal/store/sqlite_memory_write.go` | 移除 visibility 硬编码默认值，交给 Manager 层 |
| `internal/store/interfaces.go` | 新增 ScopePolicyStore 接口 |
| `internal/store/sqlite_schema.go` | freshSchema 加 scope_policies 表 |
| `internal/store/sqlite_migration_v21_v25.go` | V24 迁移 |
| `internal/mcp/session.go` | Session 新增 projectScope 字段 + context 注入/读取 |
| `internal/mcp/tools/retain.go` | scope 为空时从 session context 推导；更新工具描述；响应增加降级信息 |
| `internal/mcp/tools/create_session.go` | 执行后将 projectScope 写入 session |
| `cmd/cli/hook_session_start.go` | 输出中增加 project scope / user scope |
| `cmd/cli/hook_capture.go` | 日常会话模式 fallback 到 user/ scope |
| `internal/api/router.go` | 注册 scope-policies 路由 |
| `testing/compliance/identity_test.go` | 补充 git remote normalize + .iclude.yaml + fork 场景 |

### 不改动的部分

- 数据库现有表结构不变（visibility/scope/owner_id 字段已有）
- 检索层 `visibilityCondition()` 已支持 team 过滤
- MCP 工具签名不变（scope 已是可选参数）
- 现有 API 行为向后兼容

---

## 7. 设计决策记录

| 决策 | 选择 | 理由 |
|------|------|------|
| project_id 来源 | git remote URL normalized hash + .iclude.yaml override | 跨机器稳定，特殊场景可手动覆盖 |
| git remote 优先级 | upstream > origin | fork 工作流中 upstream 指向上游，两人解析到同一仓库 |
| URL normalization | 提取 host/owner/repo 全小写 | SSH/HTTPS/端口等格式统一 |
| 项目模式判断 | .git/ 或 .iclude.yaml 存在 | 零配置识别 git 项目，手动项目通过 .iclude.yaml 标记 |
| 日常会话归属 | user/{owner_id}, private | 个人学习/临时问答，不共享 |
| 项目记忆 visibility | observation=private，其他=team | 过程噪音私有，知识沉淀共享 |
| scope 分配策略 | 两层：确定性来源规则 + AI 引导 | 无 LLM 也工作，有 AI 时更精准 |
| retain 获取项目上下文 | MCP session 注入 projectScope | create_session 时写入，retain 时读取 |
| 权限管控位置 | 服务端 scope_policies 数据库表 | 集中管理，动态增删，不依赖项目文件 |
| 权限不通过时 | scope 降级 + 响应提示 | 保证记忆不丢失，调用方知晓降级 |
| scope_policies 鉴权 | created_by 所有权校验 | 简单有效，未来可扩展 team admin |
| .iclude.yaml 内容 | 仅 project_id | 权限不放项目文件，防篡改 |
