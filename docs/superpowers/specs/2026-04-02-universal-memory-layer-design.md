# LocalMem Universal Memory Layer — Design Spec

**Date**: 2026-04-02
**Status**: Draft
**Author**: Codex + user discussion

---

## 1. Context

LocalMem 目前已经具备较完整的记忆底座能力：
- SQLite + Qdrant 混合检索
- conversation ingest
- reflect 多轮反思
- graph extraction / graph retrieval
- retention tier / consolidation / heartbeat
- MCP 接入能力

但这些能力目前更像“可存可搜的记忆后端”，还没有形成一套稳定的、可挂载到任意 AI 工具的**通用记忆层协议**。

本设计的目标不是做强认知科学模拟，也不是只为某一种 agent 定制，而是把 LocalMem 演进为：

1. 可挂载到任意 AI 工具的通用记忆层
2. 同时支持开发助手和通用 AI 助手
3. 支持跨工具共享用户级核心记忆
4. 通过 scope 和层级控制项目/会话隔离
5. 通过受控写入避免记忆污染

本轮已锁定的产品决策：
- **产品取向**：双线并进，同时兼顾平台抽象和开发助手/通用助手体验
- **默认隔离**：分层共享，核心用户记忆跨工具共享，项目和会话记忆按 scope 隔离
- **默认写入**：受控写入，外部工具默认只能写入低风险层级

---

## 2. Goals

- 定义一套适用于任意 AI 工具的统一记忆分层模型
- 将现有 `retain` / `scan` / `recall` / `reflect` / `consolidation` 串成清晰的数据流
- 引入显式的 core memory 机制，解决“总要靠检索命中”的问题
- 保持现有 REST API 与 MCP 工具向后兼容
- 让开发助手与通用 AI 助手复用同一底层模型，仅通过 scope 和策略差异化

## 3. Non-Goals

- 不实现 ACT-R / HTM 这类强仿生认知架构
- 不在本阶段引入复杂的多智能体协商协议
- 不重做现有存储后端或替换 SQLite / Qdrant
- 不要求首版就具备自动评估“真伪”或完全自动冲突解决能力
- 不在本阶段强制修改所有旧数据的物理 schema

---

## 4. Architecture

### 4.1 Four-Layer Memory Model

LocalMem 的通用记忆层采用 4 层模型：

| Layer | 用途 | 典型内容 | 默认写入方式 |
|------|------|---------|-------------|
| `core` | 始终注入上下文的稳定工作记忆 | 用户画像、长期目标、项目当前状态、操作规则 | 仅候选晋升 |
| `episodic` | 过程性、会话性、任务性记忆 | 对话、调试过程、任务过程、事件记录 | 外部工具默认写入 |
| `semantic` | 提炼后的事实与偏好 | 偏好、知识点、决策、稳定事实 | reflect/consolidation 晋升 |
| `procedural` | 策略与规则记忆 | 工具偏好、回答规范、工作流规则、操作 playbook | reflect/consolidation 晋升 |

这 4 层是**产品语义层**。首版不要求一定通过新增大表实现，可以先基于现有字段组合表达：
- `kind`
- `sub_kind`
- `retention_tier`
- `scope`
- `message_role`
- `metadata`

必要时再补充 `memory_class` 作为更稳定的显式字段。

### 4.2 Why This Architecture

对于 LocalMem，最适合借鉴的是工程化的分层记忆架构，而不是强仿生实现。原因如下：

1. 现有系统已经具备 conversation、retrieval、reflect、consolidation、heartbeat 等工程组件
2. 真正缺的是“工作记忆 / 情景记忆 / 语义记忆 / 程序性记忆”的清晰边界
3. 现有 agent 接入主要通过 API/MCP，最重要的是统一协议和跨工具行为一致性

因此，该架构的重点是：
- 小容量、强约束的 core memory
- 高吞吐、可衰减的 episodic memory
- 可晋升、可复用的 semantic / procedural memory

### 4.3 Data Flow

```text
External AI Tool
    │
    ├─ retain / ingest_conversation
    │        ↓
    │    episodic memory
    │        ↓
    ├─ scan / recall / fetch
    │        ↑
    │   core injection + retrieval fusion
    │
    └─ reflect
             ↓
      candidate memories
             ↓
      consolidation / promotion
             ↓
    semantic / procedural / core
```

### 4.4 Memory Lifecycle

#### Episodic

- 默认允许外部工具高频写入
- 使用现有 `retention_tier` 和 `decay` 机制控制寿命
- 优先保留原始事件和过程上下文

#### Semantic

- 表示“已经沉淀下来的事实、偏好、知识、决策”
- 主要来源于 reflect 或 consolidation 晋升
- 不鼓励任意工具直接写入大量 semantic 记忆

#### Procedural

- 表示“如何行动”的策略类记忆
- 例如：编码风格规则、工具调用偏好、回答策略、工作流习惯
- 只允许 reflect / consolidation 产出，普通 retain 默认不能直接写入

#### Core

- 小容量、强约束、固定注入上下文
- 用于保存“必须稳定知道”的少量高价值信息
- 不保存原始流水，只保存结构化摘要
- 只允许由 candidate 晋升或显式管理入口更新

---

## 5. Scope, Isolation, and Sharing

### 5.1 Scope Model

统一使用 scope 表达共享边界，首版固定四类：

| Scope Pattern | 含义 | 默认共享范围 |
|--------------|------|-------------|
| `user/*` | 用户级长期记忆 | 跨工具共享 |
| `project/*` | 项目级记忆 | 同项目/同团队共享 |
| `session/*` | 会话级记忆 | 单会话隔离 |
| `agent/*` | agent 私有工作记忆 | 单 agent 隔离 |

### 5.2 Default Sharing Policy

- `user/*`：允许开发助手和通用 AI 助手共享用户核心偏好与长期信息
- `project/*`：默认不跨项目共享，避免代码库串味
- `session/*`：只在当前线程/当前对话中有效
- `agent/*`：用于某个 agent 的私有策略或缓存

### 5.3 Retrieval Priority

固定默认检索优先级：

1. `session/*`
2. `project/*`
3. `user/*` core
4. `user/*` semantic
5. 其他低优先级跨工具记忆

这样可以保证：
- 当前会话上下文优先
- 当前项目状态优先于泛化偏好
- 用户长期偏好不会压过当前任务事实

---

## 6. Core Memory

### 6.1 Purpose

当前 LocalMem 的主要问题之一是：重要信息只能依赖检索命中，没有一个稳定注入上下文的小型工作记忆层。

因此需要新增 core memory 机制。

### 6.2 Core Blocks

首版按 scope 维护固定 memory blocks，推荐最小集合：

- `user_profile`
- `user_preferences`
- `active_goals`
- `current_project_state`
- `operating_rules`

不同 scope 下的 block 可以不同：

- `user/*`：更偏用户画像和长期偏好
- `project/*`：更偏项目状态、约定、当前任务
- `agent/*`：更偏 agent 工作策略

### 6.3 Core Constraints

- block 数量受限
- 每个 block 内容必须是摘要，不允许无限增长
- 普通 retain 不允许直接覆盖 core
- core 更新必须走 candidate -> promotion 或专门管理入口

---

## 7. Promotion and Controlled Write

### 7.1 Controlled Write Policy

外部 AI 工具默认采用受控写入：

- 默认只能直接写入 `episodic`
- 可写入 `semantic_candidate` / `core_candidate`
- 不能直接写 `core`
- 不能直接写 `procedural`

这样做的目标是降低：
- 噪声污染
- prompt 注入式持久污染
- 跨工具错误泛化
- 项目记忆被通用助手覆盖

### 7.2 Promotion Pipeline

首版明确 candidate 晋升链路：

```text
episodic
  └─ reflect/consolidation
        └─ semantic_candidate / procedural_candidate / core_candidate
              └─ promotion
                    ├─ semantic
                    ├─ procedural
                    └─ core
```

### 7.3 Promotion Signals

可用于晋升的信号：

- 高频复现
- `reinforced_count` 达阈值
- 跨多次会话仍然出现
- 明确被识别为偏好/规则/项目状态
- reflect 输出高置信结论
- 用户显式确认

### 7.4 Demotion / Cleanup

- `episodic` 继续按 retention tier 衰减或过期
- `semantic` 可在 consolidation 中被合并或降级
- `procedural` 只允许显式替换或高置信更新
- `core` 不自动删除，只允许显式替换、降级或人工确认式更新

---

## 8. Public API and MCP Changes

首版要求向后兼容，新增字段全部可选。

### 8.1 REST DTO Extensions

#### `CreateMemoryRequest`

建议新增可选字段：

```go
MemoryClass string `json:"memory_class,omitempty"` // core / episodic / semantic / procedural
CaptureMode string `json:"capture_mode,omitempty"` // episodic / semantic_candidate / core_candidate
CandidateFor string `json:"candidate_for,omitempty"` // semantic / procedural / core
```

默认行为：
- 老客户端不传时，仍按当前逻辑工作
- 普通 retain 默认写 `episodic`

#### `RetrieveRequest`

建议新增可选字段：

```go
MemoryClass string   `json:"memory_class,omitempty"`
IncludeCore *bool    `json:"include_core,omitempty"`
ScopePriority []string `json:"scope_priority,omitempty"`
```

默认行为：
- `include_core` 默认开启
- 不传 `memory_class` 时按现有混合检索

#### `ReflectRequest`

建议新增可选字段：

```go
OutputClass string `json:"output_class,omitempty"`       // semantic / procedural / core_candidate
PromoteOnSuccess *bool `json:"promote_on_success,omitempty"`
```

### 8.2 MCP Tool Extensions

现有 MCP 工具保留并扩展：

- `iclude_retain`
  - 默认只写 `episodic`
  - 支持 `capture_mode`
  - 不允许直接写 `core`

- `iclude_scan` / `iclude_recall` / `iclude_fetch`
  - 增加 `memory_class` 过滤能力

- `iclude_reflect`
  - 支持输出类型和候选晋升语义

新增建议工具：

- `iclude_read_core`
  - 读取指定 scope 下的 core blocks

- `iclude_promote_memory`
  - 将候选记忆晋升为 semantic / procedural / core

---

## 9. Implementation Mapping

这套方案优先复用现有组件，不重做核心架构：

| 现有组件 | 新角色 |
|---------|-------|
| `memory.Manager` | 受控写入入口、候选写入、晋升协调 |
| `search.Retriever` | 分层检索与优先级融合 |
| `reflect.Engine` | 产出 semantic/procedural/core candidates |
| `consolidation` | 晋升、合并、去重、摘要更新 |
| `heartbeat` | 巡检冲突、弱化低价值记忆、驱动维护任务 |
| `conversation ingest` | episodic 主入口 |
| `contexts` | session/project 层级组织 |

首版推荐实现顺序：

1. 补 `memory_class` / `capture_mode` / `include_core`
2. 加 core memory blocks 读写约束
3. 调整 retain / recall / reflect MCP 接口
4. 让 reflect 输出 candidate
5. 让 consolidation 执行晋升

---

## 10. Test Plan

### 10.1 Core Injection

- 指定 scope 时能读取到固定 core blocks
- 超出 block 数量或内容长度限制时按规则拒绝或替换
- 普通 retain 不能直接覆盖 core

### 10.2 Controlled Write

- 外部 retain 默认写入 `episodic`
- `procedural` / `core` 直接写入被拒绝或转 candidate
- scope 与 identity 不匹配时拒绝写入

### 10.3 Promotion Pipeline

- reflect 可产出 `semantic_candidate`
- consolidation 可将候选晋升为 `semantic` / `procedural`
- 只有达阈值的候选可进入 `core`

### 10.4 Cross-Tool Sharing

- 两个不同 AI 工具可共享同一 `user/*` core
- 两个不同项目的 `project/*` 记忆互不泄漏
- `session/*` 不跨线程泄漏

### 10.5 Retrieval Priority

- `session/*` 命中优先于 `project/*`
- `project/*` 优先于 `user/*` semantic
- `include_core=false` 时不会注入 core
- `memory_class` 过滤后结果集合稳定

### 10.6 Backward Compatibility

- 旧版 `CreateMemoryRequest` 不传新增字段时行为保持不变
- 旧版 `RetrieveRequest` / `ReflectRequest` 行为保持不变
- 现有 CRUD / timeline / ingest_conversation / MCP scan/fetch 不破坏

---

## 11. Risks and Tradeoffs

### 风险 1：抽象过度

如果一开始引入过多新层级、新字段、新规则，可能导致实现复杂度显著上升。

**缓解策略**：
- 首版优先作为语义层实现
- 复用现有字段和 store
- 只对高收益接口做增量扩展

### 风险 2：Core Memory 失控膨胀

如果 core 没有容量和更新约束，最终只会变成另一份“难检索的大文本”。

**缓解策略**：
- block 固定
- 容量上限固定
- 只允许摘要，不允许原始流水

### 风险 3：跨工具记忆串味

用户级共享如果缺乏 scope 纪律，开发助手和通用助手可能互相污染。

**缓解策略**：
- 默认检索优先级里，项目和会话优先于用户全局
- procedural 与 core 采用受控写入
- 项目级内容不得默认写入用户全局

---

## 12. Decision Summary

对 LocalMem 来说，最值得借鉴的不是强仿生认知架构，而是工程化的分层记忆设计。

最终决策：

- 采用 4 层记忆模型：`core` / `episodic` / `semantic` / `procedural`
- 采用分层共享：`user/*` 跨工具共享，`project/*` / `session/*` 按 scope 隔离
- 采用受控写入：普通工具默认只写 `episodic`
- 采用晋升链路：通过 `reflect + consolidation` 生成与提升高价值记忆
- 保持现有 API/MCP 向后兼容，以增量字段和增量工具落地

这条路线既能服务开发助手，也能服务通用 AI 助手，同时保持 LocalMem 作为“通用记忆层”而不是“单一 agent 的专用外挂”。
