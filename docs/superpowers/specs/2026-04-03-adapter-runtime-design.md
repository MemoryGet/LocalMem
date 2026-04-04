# LocalMem Adapter Runtime Design

**Date**: 2026-04-03  
**Status**: Draft  
**Author**: Codex + user discussion

---

## 1. Context

在 [Unified AI Tool Integration Protocol v1](./2026-04-03-unified-ai-tool-integration-protocol-design.md) 中，已经定义了：

- 统一身份模型
- 统一 scope 规则
- 统一最小工具集
- Codex / Claude Code / Cursor / Cline 四类接入 profile

但协议层仍缺少“运行时如何执行”的规范。  
本设计补齐 Adapter Runtime，重点解决：

1. `iclude launch <tool>` 如何工作
2. 会话如何创建、跟踪、结束、修复
3. transcript / hook / tool event 如何统一采集
4. `finalize_session` 应如何定义
5. 失败、重试、幂等、补偿如何落地

---

## 2. Goals

- 定义 LocalMem Adapter Runtime 的组件边界
- 统一 launcher / hook / bridge 的执行流程
- 定义 session registry 与 session state machine
- 定义 transcript capture 和 event capture 标准格式
- 定义 `iclude_finalize_session` 协议语义
- 定义 repair queue / retry queue / idempotency key 规则

## 3. Non-Goals

- 不在本轮直接实现所有 adapter
- 不要求所有宿主工具都走 launcher
- 不在本设计中规定 UI 交互细节
- 不替代后端 memory manager 的内部实现

---

## 4. Runtime Overview

```text
Host Tool
   |
   | native hooks / wrapper / plugin bridge
   v
Adapter Runtime
   |- launcher
   |- session registry
   |- event normalizer
   |- transcript collector
   |- retry queue
   |- repair queue
   v
Protocol Client
   |- create_session
   |- scan
   |- fetch
   |- retain
   |- ingest_conversation
   |- finalize_session
   v
LocalMem Backend
```

Adapter Runtime 是一层本地执行器，不直接决定“记忆内容如何检索排序”，只负责：

- 生命周期
- 事件归一化
- 可靠投递
- 会话闭环

---

## 5. Core Components

### 5.1 Launcher

作用：

- 以受控方式启动宿主工具
- 在宿主会话开始前先完成 LocalMem session bootstrap
- 为无原生 hook 的工具补足生命周期控制

建议命令：

```bash
iclude launch codex
iclude launch cursor
iclude launch cline
```

launcher 负责：

1. 解析宿主 profile
2. 解析工作目录与 `project_id`
3. 生成或获取 `session_id`
4. 调用 `iclude_create_session`
5. 调用 `iclude_scan`
6. 将 memory prelude 注入宿主
7. 启动 transcript collector / process watcher
8. 宿主退出后执行 finalize 流程

### 5.2 Session Registry

作用：

- 持久记录当前 adapter 管理的会话状态
- 支持 crash recovery
- 支持 pending finalize 修复

建议本地存储位置：

```text
~/.iclude/runtime/sessions.db
```

建议最小表结构：

| Field | Description |
|------|-------------|
| `session_id` | 宿主会话 ID |
| `context_id` | LocalMem context ID |
| `user_id` | 逻辑用户 ID |
| `tool_name` | 宿主工具名 |
| `project_id` | 项目 ID |
| `project_dir` | 工作目录 |
| `profile` | A/B/C/D profile |
| `state` | 当前状态 |
| `transcript_path` | transcript 文件路径 |
| `started_at` | 创建时间 |
| `last_seen_at` | 最近活动时间 |
| `finalize_attempts` | finalize 尝试次数 |
| `last_error` | 最近错误摘要 |

### 5.3 Event Normalizer

作用：

- 将不同宿主的原始事件归一为统一 envelope

输入来源：

- hook payload
- transcript 增量内容
- 插件 bridge 事件
- wrapper process lifecycle

输出统一事件类型：

- `session_started`
- `memory_scanned`
- `message_captured`
- `tool_observed`
- `fact_observed`
- `conversation_ingested`
- `session_finalized`
- `session_finalize_failed`

### 5.4 Transcript Collector

作用：

- 读取 transcript 文件或事件流
- 抽取结构化 turn
- 支持断点续读

适用：

- 无原生 hook 的工具
- stop hook 失败后的 repair
- 半自动 transcript 补录

### 5.5 Retry Queue

作用：

- 重试短暂失败的在线调用

适用动作：

- `retain`
- `ingest_conversation`
- `finalize_session`

### 5.6 Repair Queue

作用：

- 修复已经偏离正常生命周期的会话

适用场景：

- 进程崩溃
- stop hook 未触发
- backend 暂时不可达
- transcript 已存在但会话未 finalize

---

## 6. Session State Machine

Adapter Runtime 统一使用以下状态机：

| State | Meaning |
|------|---------|
| `created` | 本地会话已创建，尚未 bootstrap |
| `bootstrapped` | 已完成 create_session + 初始 scan |
| `active` | 正常进行中 |
| `finalizing` | 正在 ingest/finalize |
| `finalized` | 已成功结束 |
| `pending_repair` | 结束流程未完成，待修复 |
| `abandoned` | 宿主退出且无法恢复 |

### 6.1 Allowed Transitions

```text
created -> bootstrapped -> active -> finalizing -> finalized
active -> pending_repair
finalizing -> pending_repair
pending_repair -> finalizing -> finalized
pending_repair -> abandoned
```

### 6.2 Transition Rules

1. `bootstrapped` 之前不得写入 `active`
2. `finalized` 是幂等终态，可重复进入但不应重复产生副作用
3. `pending_repair` 必须保留 transcript pointer 和最后错误
4. 超过阈值仍无法 finalize 的会话可标记 `abandoned`

---

## 7. Bootstrap Flow

### 7.1 Standard Bootstrap

```text
resolve profile
  -> resolve identity
  -> resolve project_id
  -> get or create session_id
  -> write registry(created)
  -> call iclude_create_session
  -> call iclude_scan
  -> render memory prelude
  -> start watchers/collectors
  -> mark bootstrapped
  -> mark active
```

### 7.2 Bootstrap Requirements

必须满足：

1. `session_id` 在本地 registry 和后端 `context_id` 绑定
2. `scan` 结果只注入摘要层
3. 即使 `scan` 失败，也允许会话进入 `active`
4. `create_session` 失败时可以重试，但不得阻塞宿主无限等待

### 7.3 Bootstrap Timeout

建议：

- `create_session`: 3-5s
- `scan`: 3-8s
- 总 bootstrap: 10-15s

超时策略：

- 超时后降级启动宿主
- registry 标记 `memory_offline=true`

---

## 8. Capture Model

### 8.1 Capture Sources

统一支持三类采集源：

| Source | Priority | Notes |
|-------|----------|-------|
| native hook | highest | 结构化最好 |
| plugin/bridge event | medium | 依赖宿主扩展 API |
| transcript parse | fallback | 最通用，但质量最低 |

优先规则：

- 同一事件若 hook 已捕获，则 transcript parser 不应重复保留
- transcript 用于补缺，不用于覆盖高质量 hook 事件

### 8.2 Capture Granularity

推荐采集：

- 用户明确表达的偏好、决策、事实
- 关键工具调用结果
- 任务状态变化
- 会话总结

默认跳过：

- 高频搜索噪音
- 无结果的试探性命令
- 大块中间日志

### 8.3 Capture Modes

统一枚举：

- `auto`
- `manual`
- `repair`
- `imported`

必须写入 `metadata.capture_mode`

---

## 9. Transcript Format

Transcript Collector 内部统一使用 normalized turn：

```json
{
  "turn_id": "t_001",
  "session_id": "s_123",
  "role": "user",
  "content": "请修复 MCP 超时逻辑",
  "timestamp": "2026-04-03T10:00:00Z",
  "tool_name": "",
  "tool_call_id": "",
  "metadata": {}
}
```

tool turn 示例：

```json
{
  "turn_id": "t_002",
  "session_id": "s_123",
  "role": "tool",
  "content": "Updated retry timeout from 30s to 10s",
  "timestamp": "2026-04-03T10:00:15Z",
  "tool_name": "Edit",
  "tool_call_id": "call_xxx",
  "metadata": {
    "capture_mode": "auto"
  }
}
```

### 9.1 Transcript Cursor

为支持增量读取，collector 必须维护：

- `source_path`
- `byte_offset`
- `last_turn_id`
- `last_read_at`

### 9.2 Truncation Rules

默认规则：

- 单 turn 超过阈值时保留首尾摘要
- 二进制/超长日志不直接入库
- 工具输出优先提炼摘要后写 observation

---

## 10. `iclude_finalize_session`

建议新增明确的 finalize 工具，而不是把“结束会话”混在 `ingest_conversation` 语义里。

### 10.1 Purpose

`iclude_finalize_session` 的作用：

1. 将会话标记为 closed
2. 触发最终 transcript ingest 或确认 ingest 已完成
3. 触发会话摘要生成
4. 记录 finalize 成功时间
5. 保证终态幂等

### 10.2 Required Inputs

| Field | Required | Description |
|------|----------|-------------|
| `session_id` | yes | 宿主会话 ID |
| `context_id` | recommended | LocalMem 上下文 ID |
| `project_id` | recommended | 项目 ID |
| `tool_name` | yes | 宿主工具名 |
| `idempotency_key` | yes | finalize 幂等键 |
| `summary` | optional | adapter 侧预生成摘要 |
| `turns` | optional | 待 ingest 的 normalized turns |
| `transcript_ref` | optional | transcript 文件引用 |

### 10.3 Outputs

| Field | Description |
|------|-------------|
| `finalized` | 是否已成功 finalize |
| `conversation_ingested` | 是否完成 ingest |
| `summary_memory_id` | 会话摘要记忆 ID |
| `finalized_at` | 完成时间 |

### 10.4 Semantics

`iclude_finalize_session` 必须满足：

1. **幂等**
2. **可重入**
3. **可部分成功**
4. **可修复**

也就是说：

- 若之前已 ingest，但未写 final marker，再次 finalize 应补 marker，不重复导入内容
- 若摘要已生成，再次 finalize 不应重复生成重复 summary memory

### 10.5 Relation to `iclude_ingest_conversation`

建议关系：

- `ingest_conversation` 负责“导入内容”
- `finalize_session` 负责“关闭会话并保证完整收尾”

二者不可互相替代。

---

## 11. Idempotency Rules

### 11.1 Key Shapes

建议统一幂等键格式：

```text
retain:{tool_name}:{session_id}:{event_hash}
ingest:{tool_name}:{session_id}:v{n}
finalize:{tool_name}:{session_id}:v{n}
```

### 11.2 Event Hash Inputs

`retain` 类事件的 `event_hash` 建议基于：

- normalized content
- role
- tool_name
- tool_call_id
- coarse timestamp bucket

### 11.3 Version Bump Rules

只有以下情况允许 bump `v{n}`：

- transcript 发生实质补全
- 上次 finalize 因 backend 故障未完成
- summary 生成策略发生显式升级

---

## 12. Retry and Repair

### 12.1 Retry Queue

短暂错误进入 retry queue：

- 网络超时
- 429
- backend 5xx

建议退避：

- 1s
- 5s
- 30s
- 2min
- 10min

### 12.2 Repair Queue

以下情况进入 repair queue：

1. 宿主进程退出但未 finalize
2. stop hook 未执行
3. transcript 已存在但 ingest 缺失
4. finalize 重试超过短期阈值

repair worker 建议周期：

- 本地运行时：每 5-10 分钟
- 后端 scheduler：每 30-60 分钟

### 12.3 Repair Strategy

优先顺序：

1. 读取 registry
2. 查 transcript cursor
3. 补读 transcript
4. 调 `ingest_conversation`
5. 调 `finalize_session`
6. 标记 `finalized` 或 `abandoned`

---

## 13. Profile-Specific Runtime Behavior

### 13.1 Profile A: Native Hook + MCP

运行时重点：

- launcher 可选
- hook 是主信号源
- transcript 只是 repair fallback

适合：

- Claude Code

### 13.2 Profile B: MCP + Instruction Injection

运行时重点：

- bootstrap 依赖 instructions 保证首轮 scan
- finalize 依赖 wrapper 或 transcript repair

适合：

- Codex
- 部分 Cursor 形态

### 13.3 Profile C: Wrapper + Transcript Capture

运行时重点：

- launcher 必须存在
- transcript 是主信号源
- finalize 由 wrapper 主导

适合：

- Cline

### 13.4 Profile D: Plugin / Extension Bridge

运行时重点：

- extension API 提供 session / workspace / transcript 事件
- runtime 作为插件内部桥接层存在

适合：

- Cursor extension

---

## 14. Security Boundaries

Adapter Runtime 必须执行以下边界：

1. 不信任模型输出的 `user_id` / `tool_name`
2. 不允许外部 adapter 直接写 `core`
3. transcript 必须支持脱敏
4. 本地 registry 中的敏感字段必须限制权限
5. launcher 不应把 API key 暴露给宿主 transcript

建议：

- transcript 前置 redaction
- metadata size limit
- project scope allowlist

---

## 15. Observability

必须记录以下 runtime events：

| Event | Required Fields |
|------|-----------------|
| `runtime.session_created` | session_id, tool_name, project_id |
| `runtime.bootstrap_succeeded` | session_id, context_id |
| `runtime.bootstrap_failed` | session_id, error |
| `runtime.retain_succeeded` | session_id, event_hash |
| `runtime.retain_failed` | session_id, error |
| `runtime.ingest_succeeded` | session_id, turn_count |
| `runtime.finalize_succeeded` | session_id, finalized_at |
| `runtime.finalize_failed` | session_id, error |
| `runtime.repair_replayed` | session_id, action |

推荐指标：

- bootstrap success rate
- finalize success rate
- pending repair count
- average ingest latency
- memory recall hit rate by tool

---

## 16. Minimal File Layout

建议运行时目录：

```text
~/.iclude/
  runtime/
    sessions.db
    retry-queue.db
    repair-queue.db
    transcripts/
    logs/
```

说明：

- `sessions.db` 保存会话 registry
- `retry-queue.db` 保存短期重试项
- `repair-queue.db` 保存待修复 finalize 任务
- `transcripts/` 保存本地 transcript 快照或索引

---

## 17. Implementation Phases

### Phase 1

- 定义 `session registry`
- 在 CLI 中加 launcher 骨架
- 定义 normalized turn 格式

### Phase 2

- 增加 `finalize_session` MCP tool
- 实现 retry queue
- 实现 pending finalize repair

### Phase 3

- 接入 Claude Code 的完整 finalize 流程
- 为 Codex 增加 wrapper prototype
- 为 Cline 增加 transcript capture prototype

### Phase 4

- 加兼容性测试集
- 加 runtime metrics
- 加 installer 对 launcher profile 的支持

---

## 18. Key Decisions

本设计锁定以下决策：

1. Adapter Runtime 是协议执行层，不是检索逻辑层
2. 无 hook 的宿主必须通过 launcher/wrapper 建立可控生命周期
3. transcript 只作为 fallback 或补录，不应优先于原生 hook
4. 必须新增 `iclude_finalize_session`
5. 所有 finalize 路径都必须幂等且可修复
6. 本地 session registry 是 crash recovery 的基础设施

---

## 19. Open Questions

1. runtime 本地 registry 应使用 SQLite 还是复用现有主库
2. transcript 快照是否需要压缩存储
3. `finalize_session` 是否需要支持“只写 marker，不重新 ingest”
4. Codex 实际可用的 transcript / session 事件接口边界如何
5. Cursor extension bridge 的最小宿主 API 集是什么

---

## 20. Recommended Next Spec

下一份建议补：

**LocalMem Compliance Test Suite Design**

重点定义：

- 各 profile 的合规用例
- bootstrap/finalize 幂等测试
- scope 隔离测试
- repair 回放测试

