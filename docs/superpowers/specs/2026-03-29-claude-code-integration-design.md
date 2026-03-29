# Claude Code Integration Design

> IClude 集成到 Claude Code，实现自动捕获、自动注入、取代系统上下文注入。

## 1. 目标

- **自动捕获**：PostToolUse 逐条记录工具调用为结构化记忆
- **自动注入**：SessionStart 注入最近记忆的 abstract 摘要层
- **会话摘要**：Stop 时生成会话总结
- **零摩擦**：用户无需手动操作，hooks 全自动

## 2. 整体架构

```
Claude Code Session
    |
    +-- SessionStart hook --> iclude-cli hook session-start
    |                           |
    |                           +-- POST /messages -> iclude_create_session(session_id)
    |                           +-- POST /messages -> iclude_scan(limit=20)
    |                           +-- stdout: abstract 摘要列表 -> 注入对话
    |
    +-- PostToolUse hook ----> iclude-cli hook capture
    |   (每次工具调用)          |
    |                           +-- 从 stdin JSON 读 session_id + tool_name + tool_input + tool_response
    |                           +-- 类别过滤（skip_tools 黑名单）
    |                           +-- POST /messages -> iclude_retain(content, context_id, kind=observation)
    |                           +-- 后端自动：abstract 生成 + 实体抽取 + content_hash 去重
    |
    +-- Stop hook -----------> iclude-cli hook session-stop
                                |
                                +-- 从 stdin JSON 读 session_id, stop_hook_active
                                +-- 检查 stop_hook_active 防死循环
                                +-- POST /messages -> iclude_scan(context_id, limit=50)
                                +-- 汇总本会话 observation abstract -> 会话摘要
                                +-- POST /messages -> iclude_retain(摘要, kind=session_summary)
```

## 3. 实现方案

**Go CLI 子命令 + MCP HTTP 调用（方案 B）。**

`cmd/cli/main.go` 编译为 `iclude-cli`，子命令通过 HTTP 调用 MCP Server（localhost:8081）。

选择理由：Go 原生处理 JSON/错误比 shell 可靠；走 MCP 保持单一数据入口；编译成一个二进制不比 shell 重。

## 4. CLI 子命令详设

### 4.1 `iclude-cli hook session-start`

**触发**：Claude Code SessionStart hook

**流程**：
1. 从 stdin 读取 JSON，提取 `session_id`、`cwd`
2. 读 config.yaml 拿 MCP 地址和 token
3. 调 MCP `iclude_create_session`（session_id 存入 Context metadata）
4. 调 MCP `iclude_scan`（limit=20, 按最近时间排序）
5. 取结果中的 abstract 拼接成文本块
6. stdout 输出注入文本：

```
# IClude Session Context (session_id: xxx)
最近 20 条记忆摘要：
1. [fact] 2026-03-28 -- IClude MCP Server 支持 7 个工具...
2. [observation] 2026-03-28 -- 修复了 RRF 融合中 Qdrant 空壳问题...
...
如需查看完整内容，请调用 iclude_fetch(ids)。
```

### 4.2 `iclude-cli hook capture`

**触发**：Claude Code PostToolUse hook

**输入**：stdin JSON（含 session_id, tool_name, tool_input, tool_response, tool_use_id）

**流程**：
1. 解析 stdin JSON
2. 检查 skip_tools 黑名单，命中则静默退出
3. 用 session_id 查找对应的 context_id（调 MCP 或本地缓存）
4. 格式化 content 文本：`[{tool_name}] {简要描述}`
5. 调 MCP `iclude_retain`：
   - content = 格式化文本
   - context_id = 当前会话 Context
   - kind = `observation`
   - source_type = `hook`
   - message_role = `tool`
   - metadata = `{tool_name, tool_input(截断), tool_output(截断), tool_use_id}`
6. 无 stdout 输出（静默）

### 4.3 `iclude-cli hook session-stop`

**触发**：Claude Code Stop hook

**输入**：stdin JSON（含 session_id, stop_hook_active, last_assistant_message）

**流程**：
1. 解析 stdin JSON
2. 检查 `stop_hook_active`，若为 true 则直接退出（防死循环）
3. 用 session_id 查找 context_id
4. 调 MCP `iclude_scan`（filter: context_id=当前会话, limit=50）
5. 汇总所有 observation 的 abstract 为会话摘要文本
6. 调 MCP `iclude_retain`：content=摘要, kind=session_summary, context_id
7. 无 stdout 输出

## 5. 会话归属

复用现有 ContextID（kind=session），不加 session_id 列。

**理由**：
- `IngestConversation` 已用 `kind=session` 的 Context 做会话容器
- Context 有 metadata（存 Claude Code session_id）、memory_count、树形能力
- `iclude://context/session/{id}` Resource 已实现按 Context 查询
- 零 migration

**映射关系**：Claude Code `session_id` -> Context.metadata.session_id -> context_id

## 6. 上下文注入

SessionStart hook 注入 **abstract 摘要层**（渐进式披露中间层）。

- abstract: 一句话摘要，<=150 字符，自动生成 + heartbeat 兜底
- 注入 20 条约 3000 token 以内
- Claude 需要细节时调 `iclude_fetch(ids)` 获取完整内容

## 7. 模型扩展

不加列，不做 migration。结构化数据放 metadata：

- `message_role` = `"tool"`（已有字段）
- `kind` = `"observation"`（新增 kind 值，字符串字段无需改 schema）
- `source_type` = `"hook"`（新增 source_type 值）
- `content` = 格式化工具调用描述文本（供 FTS5 检索）
- `metadata` = `{tool_name, tool_input, tool_output, tool_use_id}`

## 8. MCP 侧改动

| 改动 | 类型 | 说明 |
|------|------|------|
| `iclude_create_session` | 新增工具 | 创建 kind=session 的 Context，接收 session_id 存入 metadata，返回 context_id |
| `retainArgs` 加 `context_id` | 扩展字段 | 可选，retain 时关联会话 |
| `retainArgs` 加 `source_type` | 扩展字段 | 可选，标记来源 |
| `retainArgs` 加 `message_role` | 扩展字段 | 可选，标记角色 |

## 9. 捕获过滤策略

四层漏斗，参考行业最佳实践（"存压缩版，查询时排序"）：

### Layer 1: 类别排除（黑名单）

```yaml
hooks:
  capture:
    skip_tools:
      - Glob
      - Grep
      - ToolSearch
      - TaskCreate
      - TaskUpdate
      - TaskList
      - TaskGet
      - TodoWrite
    max_input_chars: 1000
    max_output_chars: 500
```

默认捕获所有未在黑名单中的工具调用。宁可多存压缩版，不在入口端丢信息。

### Layer 2: 内容去重

IClude 已有 `content_hash` 字段 + `Dedup` 配置。短时间内重复内容自动跳过。零改动。

### Layer 3: AI 压缩

IClude 已有 `asyncGenerateAbstract` + heartbeat `runAbstractBackfill`。零改动。

### Layer 4: 检索时排序

IClude 已有三路 RRF + 强度加权 + MMR 多样性重排。零改动。

## 10. Hooks 配置

项目 `.claude/settings.local.json`：

```json
{
  "hooks": {
    "SessionStart": [{
      "command": "iclude-cli hook session-start",
      "timeout": 10000
    }],
    "PostToolUse": [{
      "command": "iclude-cli hook capture",
      "timeout": 5000
    }],
    "Stop": [{
      "command": "iclude-cli hook session-stop",
      "timeout": 10000
    }]
  }
}
```

- SessionStart stdout 自动注入 Claude 上下文
- PostToolUse 静默，失败不阻断 Claude
- Stop 检查 `stop_hook_active` 防死循环
- timeout 设合理值，MCP 不可达时快速失败

## 11. 配置扩展

config.yaml 新增 `hooks` 配置节：

```yaml
hooks:
  enabled: true
  mcp_url: "http://localhost:8081"
  capture:
    skip_tools: [Glob, Grep, ToolSearch, TaskCreate, TaskUpdate, TaskList, TaskGet, TodoWrite]
    max_input_chars: 1000
    max_output_chars: 500
  session_start:
    inject_limit: 20      # 注入最近 N 条记忆 abstract
  session_stop:
    summary_limit: 50     # 汇总最近 N 条 observation
```

## 12. 已有能力复用（零改动）

| 能力 | 现有实现 | 改动 |
|------|---------|------|
| 摘要压缩 | `asyncGenerateAbstract` + heartbeat backfill | 无 |
| 实体抽取 | `Extractor` 在 Create 时自动触发 | 无 |
| 内容去重 | `content_hash` + `Dedup` 配置 | 无 |
| 渐进式检索 | scan -> fetch 三层披露 | 无 |
| 会话资源 | `iclude://context/session/{id}` Resource | 无 |
| 三路检索 | FTS5 + Qdrant + Graph RRF 融合 | 无 |
| 强度加权 | `ApplyStrengthWeighting` | 无 |
| 生命周期 | 5 级保留 + 衰减 + 强化 | 无 |

## 13. 设计决策记录

| 决策 | 选择 | 理由 |
|------|------|------|
| 捕获粒度 | 逐条 | 细粒度，不丢信息 |
| 注入方式 | Hook stdout | 零摩擦，Claude 自动看到 |
| 注入内容 | abstract 摘要层 | 兼顾 token 效率和即时可用 |
| 工具调用存储 | metadata 结构化 | 不加列不 migration，metadata 已有 |
| 会话归属 | 复用 ContextID | Context 已有 kind=session，零 migration |
| 实现方案 | Go CLI + MCP HTTP | 类型安全，单一数据入口 |
| 过滤策略 | 黑名单 + 去重 + AI 压缩 + 检索时排序 | 行业共识：存压缩版，查询时排序 |
| 跨 hook 状态 | stdin JSON 自带 session_id | 无临时文件，零并发问题 |
