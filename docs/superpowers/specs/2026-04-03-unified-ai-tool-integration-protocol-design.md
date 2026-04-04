# LocalMem Unified AI Tool Integration Protocol v1

**Date**: 2026-04-03  
**Status**: Draft  
**Author**: Codex + user discussion

---

## 1. Context

LocalMem 已具备以下接入基础：

- MCP server（SSE / stdio）
- `iclude_scan` / `iclude_fetch` / `iclude_retain` / `iclude_ingest_conversation` / `iclude_create_session`
- Claude/Codex 安装脚本与接入说明
- CLI hook 能力（`session-start` / `capture` / `session-stop`）
- 通用记忆层设计（scope、core/episodic/semantic/procedural）

当前缺口不是“能否接入某个 AI 工具”，而是：

1. 不同工具的接法不统一
2. 生命周期事件没有形成正式协议
3. 身份、scope、写入权限、注入策略在不同工具上可能漂移
4. 对外只能说“支持 MCP”，但不能保证“记忆行为一致”

本设计定义一套面向 **Codex / Claude Code / Cursor / Cline** 的统一接入协议，使 LocalMem 从“可被调用的 MCP server”演进为“可被多数 AI 编码工具挂载的通用记忆层”。

---

## 2. Goals

- 为 Codex / Claude Code / Cursor / Cline 定义统一接入协议
- 统一生命周期事件：启动、检索、读取、写入、归档
- 统一用户身份、项目归属、会话归属、scope 规则
- 统一最小工具集与写入权限边界
- 允许不同工具以不同集成方式接入，但保持记忆语义一致
- 明确“原生集成”和“降级集成”的能力边界

## 3. Non-Goals

- 不要求所有宿主工具都支持完全相同的 UI 或 hook
- 不要求所有工具都能做到 100% 自动采集全量对话
- 不要求首版覆盖黑盒 SaaS 聊天产品
- 不把模型 prompt 本身视为可靠执行器
- 不在本设计中重做 MCP 协议本身

---

## 4. Design Principles

### 4.1 Memory Backend First

LocalMem 的定位是：

- **记忆后端**
- **统一协议层**
- **多工具接入适配层**

而不是仅仅一个 MCP 工具集合。

### 4.2 Capability-Normalized Integration

不同工具集成能力不同，但最终都要被归一到相同协议：

- 能力强的工具：走 hook + MCP + 自动归档
- 能力中等的工具：走 MCP + 指令注入 + 半自动归档
- 能力较弱的工具：走 wrapper / launcher + transcript 采集

### 4.3 Prompt Is Not a Guarantee

任何“必须执行”的流程，都不能只依赖 prompt 要求模型调用工具。  
协议必须把关键动作下沉到：

- hook
- wrapper
- CLI adapter
- 后端编排逻辑

### 4.4 Controlled Write

外部工具默认只允许直接写入：

- `episodic`
- `semantic_candidate`
- `core_candidate`

禁止外部工具直接覆盖：

- `core`
- `procedural`

---

## 5. Architecture

```text
AI Tool (Codex / Claude / Cursor / Cline)
    |
    |  profile-specific adapter
    v
LocalMem Adapter Layer
    |- session start hook / launcher
    |- transcript capture
    |- tool capture
    |- session stop ingest
    |- identity mapping
    v
LocalMem Protocol Layer
    |- session lifecycle
    |- scope resolution
    |- write policy
    |- retrieval policy
    v
LocalMem Backend
    |- MCP server
    |- REST API
    |- memory manager
    |- retrieval / reflect / consolidation
```

---

## 6. Protocol Surfaces

LocalMem 对外暴露三类接入面：

| Surface | 用途 | 适用对象 |
|--------|------|---------|
| MCP | 给 AI agent/tool runtime 调用记忆能力 | Codex, Claude Code, Cursor, Cline |
| CLI Adapter | 给 hook / wrapper / launcher 调用 | Claude Code, Cline, future Cursor/Codex wrappers |
| REST/SDK | 给插件、扩展、桌面桥接层调用 | Cursor extension, external desktop app, browser bridge |

协议要求：三种接入面最终都映射到同一套 LocalMem 生命周期语义。

---

## 7. Normative Identity Model

### 7.1 Canonical Identity Fields

每次接入必须能解析出以下逻辑身份：

| Field | Required | Description |
|------|----------|-------------|
| `user_id` | yes | 稳定用户身份，跨工具共享 |
| `tool_name` | yes | 宿主工具名，如 `codex` / `claude-code` / `cursor` / `cline` |
| `tool_instance_id` | no | 某一安装实例或设备标识 |
| `project_id` | recommended | 项目标识，建议来自仓库根路径哈希或显式配置 |
| `session_id` | yes | 当前会话 ID |
| `agent_id` | no | 子 agent 或 worker identity |

### 7.2 Identity Rules

1. `user_id` 必须由 LocalMem 侧统一生成或绑定，不能依赖模型自由填写
2. `tool_name` 必须来自 adapter 固定值，不能来自模型输出
3. `project_id` 必须稳定，推荐：
   - git remote URL 归一化后哈希
   - 无 git 时使用工作目录绝对路径哈希
4. `session_id` 必须唯一，优先使用宿主原生 session id；没有则由 adapter 生成 UUID
5. `agent_id` 仅在宿主存在显式子 agent 时使用

### 7.3 Canonical Scope Mapping

| Scope | Example | Shared Policy |
|------|---------|---------------|
| `user/*` | `user/u_123/preferences` | 跨工具共享 |
| `project/*` | `project/p_abc/status` | 同项目共享 |
| `session/*` | `session/s_789/thread` | 单会话隔离 |
| `agent/*` | `agent/a_456/scratchpad` | 单 agent 隔离 |

协议默认检索优先级：

1. `session/*`
2. `project/*`
3. `user/* core`
4. `user/* semantic`
5. 其他候选记忆

---

## 8. Required Tool Contract

首版统一协议定义以下 **核心最小工具集**：

| Tool | Required | Purpose |
|------|----------|---------|
| `iclude_create_session` | yes | 建立会话上下文 |
| `iclude_scan` | yes | 低成本扫描相关记忆 |
| `iclude_fetch` | yes | 按 ID 获取完整记忆 |
| `iclude_retain` | yes | 保存单条事件/事实 |
| `iclude_ingest_conversation` | recommended | 对话/会话归档 |
| `iclude_finalize_session` | recommended | 会话终结（幂等关闭 + 摘要生成） |
| `iclude_timeline` | optional | 时间线回顾 |
| `iclude_reflect` | optional | 跨记忆综合推理 |

### 8.1 Tool Semantics

#### `iclude_create_session`

用途：

- 建立当前会话上下文
- 将宿主 `session_id` 绑定到 LocalMem `context_id`

最小输入：

- `session_id`
- `project_dir` or `project_id`
- `tool_name`
- `user_id`

输出：

- `context_id`
- normalized scope metadata

#### `iclude_scan`

用途：

- 在会话开始或中途检索相关记忆摘要

要求：

- 默认低 token 成本
- 优先返回摘要、标签、scope、时间
- 支持按 `project_id` / `context_id` / `scope_prefix` 过滤

#### `iclude_fetch`

用途：

- 对 `scan` 选中的记忆做按需展开

#### `iclude_retain`

用途：

- 记录单条事件、事实、偏好、决策、工具结果摘要

要求：

- 支持 `context_id`
- 支持 `scope`
- 支持 `kind`
- 支持 `source_type`
- 支持 `message_role`
- 支持 `metadata`

#### `iclude_ingest_conversation`

用途：

- 在会话结束时批量写入压缩后的 conversation transcript

要求：

- 幂等
- 可重复提交
- 可截断长 transcript

#### `iclude_finalize_session`

用途：

- 在会话结束时执行完整终结流程
- 标记会话关闭、生成语义摘要、保证幂等

最小输入：

- `session_id`
- `tool_name`
- `idempotency_key`

可选输入：

- `context_id`
- `summary`（adapter 侧预生成摘要）

输出：

- `finalized`（是否成功终结）
- `conversation_ingested`
- `summary_memory_id`
- `finalized_at`

要求：

- 幂等（同一 idempotency_key 重复调用安全）
- 可重入（部分成功后再次调用补全）
- 摘要生成失败不阻塞终结
- 失败时标记 `pending_repair`，由 RepairService 后续补偿

与 `iclude_ingest_conversation` 的关系：

- `ingest_conversation` 负责"导入对话内容"
- `finalize_session` 负责"关闭会话并保证完整收尾"
- 二者不互相替代

---

## 9. Session Lifecycle Protocol

统一协议将所有宿主接入都归一为 5 个阶段。

### 9.1 Phase A: Session Start

触发时机：

- 用户打开新会话
- 用户进入某项目工作区
- 启动新的 agent thread

必须动作：

1. adapter 解析 `user_id` / `tool_name` / `project_id` / `session_id`
2. 调用 `iclude_create_session`
3. 调用 `iclude_scan`
4. 将返回摘要注入到宿主上下文，或在 adapter UI 中作为“memory prelude”展示

规范要求：

- 会话开始时的记忆注入必须是 **摘要层**，不是全量正文
- 默认注入上限建议 8-20 条摘要
- 每条摘要建议 <= 150 字符

### 9.2 Phase B: In-Session Retrieval

触发时机：

- 模型需要额外历史上下文
- 用户显式提到过去的工作、偏好、决策、bug、会话

允许动作：

- 调 `iclude_scan`
- 再按需调 `iclude_fetch`
- 深度综合时调 `iclude_reflect`

规范要求：

- 推荐 scan -> fetch，而非默认 recall 全量
- retrieval 结果必须带 scope，避免跨项目串味

### 9.3 Phase C: Event Capture

触发时机：

- 工具调用完成
- 用户给出新偏好/规则/事实
- 产生重要决策或状态变化

执行方式：

- 能接 hook 的宿主：自动调用 `iclude_retain`
- 不能接 hook 的宿主：由 wrapper/transcript parser 异步写入

建议保存内容：

- 重要工具调用摘要
- 用户偏好
- 设计决策
- bug 结论
- 当前任务状态变化

不建议保存内容：

- 低价值重复搜索
- 噪音日志
- 大段原始终端输出

### 9.4 Phase D: Session Stop

触发时机：

- 用户结束会话
- 宿主触发 stop hook
- wrapper 检测进程退出或 transcript closed

必须动作：

1. 调用 `iclude_finalize_session`（推荐），或 `iclude_ingest_conversation` + 手动标记
2. `finalize_session` 内部自动完成：摘要生成、状态推进、幂等保障
3. 失败时降级为 `iclude_retain` 写入会话摘要

规范要求：

- stop 阶段必须是幂等的
- 即使 stop hook 丢失，也应允许 scheduler/repair job 补归档
- RepairService 定期扫描 pending_repair 会话，自动补执行 finalize

### 9.5 Phase E: Repair / Recovery

触发时机：

- hook 未触发
- 进程崩溃
- transcript 仍在但未归档

执行方式：

- adapter 或后端 scheduler 扫描 `session_pending_finalize`
- 重新提交 `ingest_conversation`

---

## 10. Data Contract

### 10.1 Standard Retain Envelope

外部 adapter 写入 `iclude_retain` 时，推荐统一 envelope：

```json
{
  "content": "[Write] Updated retry policy in MCP client",
  "kind": "observation",
  "scope": "session/s_123",
  "context_id": "ctx_xxx",
  "source_type": "hook",
  "message_role": "tool",
  "metadata": {
    "tool_name": "Edit",
    "host_tool": "claude-code",
    "project_id": "p_abc",
    "session_id": "s_123",
    "agent_id": "",
    "capture_mode": "auto",
    "candidate_for": ["semantic_candidate"]
  }
}
```

### 10.2 Standard Ingest Envelope

```json
{
  "session_id": "s_123",
  "project_id": "p_abc",
  "tool_name": "codex",
  "user_id": "u_001",
  "turns": [],
  "summary": "Refactored MCP client retry flow and added timeout handling.",
  "idempotency_key": "codex:s_123:v1"
}
```

### 10.3 Required Metadata Keys

| Key | Required | Purpose |
|----|----------|---------|
| `host_tool` | yes | 宿主工具名 |
| `session_id` | yes | 会话归属 |
| `project_id` | recommended | 项目归属 |
| `capture_mode` | recommended | `auto` / `manual` / `repair` |
| `candidate_for` | optional | 晋升候选层 |
| `agent_id` | optional | 子 agent 归属 |

---

## 11. Adapter Profiles

四个目标工具不要求用同一种接法。协议定义四类 profile。

### 11.0 Reading Guide

本章中的 profile 分为两种语义：

1. **recommended profile**
   表示按宿主能力边界，长期最适合采用的接法
2. **current implementation status**
   表示按当前仓库实现，已经落地到什么程度

除非显式写明 `current implementation status`，否则本章默认描述的是：

- **目标接法**
- **推荐演进方向**

而不是“当前已经实现的能力”。

### 11.1 Profile A: Native Hook + MCP

适用：

- Claude Code
- 未来支持生命周期 hook 的工具

特征：

- 启动时可注入上下文
- 工具后可自动 capture
- 结束时可自动 stop ingest

这是最佳接入模式。

### 11.2 Profile B: MCP + Instruction Injection

适用：

- Codex 当前形态
- Cursor 某些仅支持 MCP 和项目指令的形态

特征：

- 有 MCP
- 可注入 AGENTS / rules / project instructions
- 生命周期自动化较弱

要求：

- 至少保证 session start scan
- 通过宿主指令强制首轮先 `iclude_scan`
- 对 stop ingest 由 wrapper 补齐

### 11.3 Profile C: Wrapper + Transcript Capture

适用：

- Cline
- 无可靠 hook 但可由外层启动的工具

特征：

- 工具本身不可靠提供 session 事件
- 由 `iclude launch <tool>` 启动
- LocalMem wrapper 负责 session lifecycle

### 11.4 Profile D: Plugin / Extension Bridge

适用：

- Cursor 扩展生态
- IDE 内插件桥接

特征：

- 用宿主扩展 API 获取活动工作区、聊天 transcript、会话切换事件
- 调 REST/SDK 或本地 CLI，再统一落到 LocalMem

---

## 12. Tool-Specific Mapping

### 12.1 Codex

目标接法：

- MCP `stdio`
- `AGENTS.md` / developer instructions 注入
- 可选 launcher/wrapper 补 session finalize

recommended profile：

- **Profile B**

current implementation status：

- 已有 `MCP + instruction injection`
- 尚无完整 launcher / wrapper finalize 闭环
- 因此当前更接近 **Profile B 的部分实现**

执行规范：

1. 安装器写入 Codex MCP 配置
2. 注入 LocalMem AGENTS block
3. 首轮必须要求 `iclude_scan`
4. 对重要事实即时 `iclude_retain`
5. 结束时若宿主无稳定 stop hook，则由 wrapper/transcript repair job 补 `ingest_conversation`

重点说明：

- 对 Codex，prompt/instruction 只能保证“高概率先 scan”
- 真正稳定的会话归档仍建议通过 wrapper 或 transcript collector 完成

### 12.2 Claude Code

目标接法：

- MCP `stdio`
- SessionStart / PostToolUse / Stop hooks
- CLI adapter

recommended profile：

- **Profile A**

current implementation status：

- 已有 `SessionStart / PostToolUse / Stop` hooks
- 已有 CLI hook adapter
- `Stop` hook 已切到 `iclude_finalize_session`（失败降级 retain）
- `SessionService / FinalizeService / RepairService` 已接入主链路
- 当前已达到 **Profile A + L4 基础行为**

执行规范：

1. `SessionStart` -> `iclude_create_session` + `iclude_scan`
2. `PostToolUse` -> `iclude_retain`
3. `Stop` -> `iclude_finalize_session`（失败降级 `iclude_retain`）

这是当前最接近完整统一协议的宿主。

### 12.3 Cursor

目标接法：

- 优先 MCP
- 若存在扩展 API，则由 extension bridge 补 session lifecycle
- 若无 hook，则加 workspace launcher

recommended profile：

- **Profile B** 或 **Profile D**

current implementation status：

- 当前文档中仅定义目标接法
- 当前仓库尚无稳定 Cursor bridge / extension runtime 实现
- 因此这里应视为 **规划状态**，不是当前能力声明

执行规范：

1. 用 MCP 提供记忆工具能力
2. 用 rules / workspace instructions 约束模型优先 scan
3. 若可通过扩展 API 监听会话切换和聊天 transcript，则由 bridge 自动 create_session / retain / ingest
4. 若宿主不开放足够事件，则退回 wrapper 方案

重点说明：

- Cursor 的关键不是“能不能配 MCP”，而是“能不能稳定拿到 chat/session 生命周期”
- 如果拿不到，记忆只能做到半自动

### 12.4 Cline

目标接法：

- MCP
- wrapper / transcript bridge
- 项目规则注入

recommended profile：

- **Profile C**

current implementation status：

- 当前仓库尚无 `iclude launch cline`
- 当前也没有完整 wrapper runtime
- 因此这里描述的是 **目标接法**，不是现状

执行规范：

1. `iclude launch cline` 启动宿主
2. launcher 生成 `session_id`
3. 启动前执行 `create_session + scan`
4. 运行中读取 transcript 或工具事件日志
5. 结束时统一 `ingest_conversation`

重点说明：

- Cline 场景更适合“外层包一层”而不是指望宿主本身提供完备 hook

---

## 13. Capability Matrix

下表描述的是**宿主能力判断与推荐接法**，不是当前仓库“已实现能力矩阵”。

| Capability | Codex | Claude Code | Cursor | Cline |
|-----------|-------|-------------|--------|-------|
| MCP tools | yes | yes | yes | yes |
| Instruction injection | yes | yes | yes | yes |
| Native session start hook | weak/unknown | strong | tool-dependent | weak |
| Native post-tool capture | weak | strong | tool-dependent | weak |
| Native session stop hook | weak/unknown | strong | tool-dependent | weak |
| Wrapper viability | high | medium | high | high |
| Recommended profile | B | A | B/D | C |

如果要表达当前仓库已实现程度，应单独看：

- `Codex`: MCP + instruction injection 为主
- `Claude Code`: hooks 已接，但 finalize / repair 未完全落地
- `Cursor`: 仍以规划为主
- `Cline`: 仍以规划为主

---

## 14. Reliability Requirements

### 14.1 Delivery Semantics

协议目标不是数学意义的“100% 必达”，而是工程意义的：

- 可恢复
- 可重试
- 可审计
- 幂等

### 14.2 Mandatory Reliability Controls

所有 adapter 都应满足：

1. `session_id` 唯一
2. `ingest_conversation` 幂等
3. 重要写入带 `idempotency_key`
4. adapter 失败不应阻塞宿主核心工作流
5. 存在 repair path

### 14.3 Failure Handling

| Failure | Required Behavior |
|--------|-------------------|
| MCP 不可达 | 降级运行，标记 memory offline |
| create_session 失败 | 允许后续重试，不阻塞宿主 |
| scan 失败 | 跳过注入，保留会话继续 |
| retain 失败 | 进入本地重试队列或静默降级 |
| stop ingest 失败 | 标记 pending_finalize，稍后修复 |

---

## 15. Security and Isolation

### 15.1 Must-Haves

- 服务端绑定 `user_id`，不能信任客户端任意伪造
- `project/*` 默认不跨项目共享
- SSE/HTTP 模式必须带鉴权与限流
- adapter 不得直接写 `core`
- transcript 存储必须允许脱敏和长度裁剪

### 15.2 Recommended Guards

- `scope` 白名单校验
- `tool_name` 固定枚举
- `metadata` 长度限制
- 大输出只存摘要，不存全量日志

---

## 16. Install and Packaging Standard

为了让“大部分 AI 工具愿意接入”，统一协议必须附带统一安装标准：

### 16.1 Installer Responsibilities

安装器必须自动完成：

1. 安装 `iclude-mcp` / `iclude-cli`
2. 写入宿主 MCP 配置
3. 写入宿主规则文件或指令注入文件
4. 写入 hook / wrapper 配置
5. 绑定本地 `user_id`
6. 验证 MCP 可连通

### 16.2 Official Support Definition

某工具要被标记为 “Officially Supported”，至少满足：

1. 已提供官方 installer
2. 已定义 identity mapping
3. 已定义 session lifecycle mapping
4. 已完成 start-scan 流程
5. 已有 stop ingest 或 repair 机制
6. 已通过最小接入测试集

---

## 17. Compliance Levels

### L1: MCP Reachable

- 工具能连上 MCP
- 模型可手动使用记忆工具

### L2: Guided Memory

- 有项目规则/指令注入
- 会话开头高概率先 scan

### L3: Lifecycle-Aware

- 有 create_session
- 有 start scan
- 有 retain 自动化

### L4: Fully Managed Memory

- 有 start/capture/stop 全链路
- 有 repair path
- 有统一归档

建议目标：

- Codex: 先做到 L2，再推进 L3
- Claude Code: 直接做到 L4
- Cursor: 先做到 L2/L3，再看扩展能力升到 L4
- Cline: 通过 wrapper 争取做到 L3/L4

---

## 18. Implementation Roadmap

### Phase 1

- 固化统一协议文档
- 稳定最小工具集定义
- 统一安装器产物命名与配置模板

### Phase 2

- Codex adapter 标准化
- Claude Code adapter 对齐本协议
- 增加统一 transcript ingest envelope

### Phase 3

- Cursor bridge 设计与 PoC
- Cline launcher/wrapper 设计与 PoC

### Phase 4

- 建立接入兼容性测试集
- 发布 “Officially Supported Integrations” 列表

---

## 19. Key Decisions

本设计锁定以下决策：

1. LocalMem 不只做 MCP server，而做通用记忆后端
2. 接入一致性依赖 adapter + lifecycle，而非 prompt
3. 统一协议核心围绕 `create_session / scan / fetch / retain / ingest_conversation / finalize_session`
4. `Claude Code` 作为完整协议标杆接入
5. `Codex` / `Cursor` / `Cline` 允许因宿主能力差异采用不同 profile
6. 必须坚持受控写入与 scope 隔离，避免跨工具记忆污染

---

## 20. Open Questions

1. Cursor 当前可用的正式 session / transcript 扩展接口边界是什么
2. Codex 后续是否会提供正式 session-stop 或 tool hooks
3. Cline 是否有稳定事件流可替代 transcript 解析
4. ~~是否需要增加 `iclude_finalize_session`~~ → **已实现**，已纳入 Section 8 工具表
5. 是否需要发布独立的 `LocalMem Adapter SDK`

---

## 21. Recommended Next Spec

基于本协议，下一份应补的设计文档是：

**LocalMem Adapter Runtime Design**

重点定义：

- `iclude launch <tool>` wrapper 协议
- transcript capture 格式
- repair queue
- idempotency key 规则
- `finalize_session` 语义
