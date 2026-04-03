# B4 时间与演化 — Design Spec

**Date**: 2026-04-02
**Status**: Approved
**Track**: Benchmark Track
**Dependencies**: B1-B3 完成

---

## 1. Context

B1-B3 已完成评测闭环、精排管道和 Reflect 利用率优化。当前 LongMemEval 最佳 HitRate 83.8%。

B4 目标：补齐 LongMemEval 常见失分点 — temporal 类问题和记忆演化利用率。

现状差距：
- temporal 查询窗口硬编码 7 天，无法按查询语义动态调整
- 无 `memory_class` 分层，所有记忆在检索中等权处理
- reflect 产出的 mental_model 和 consolidation 产出的 consolidated 未参与分层加权
- 无 `derived_from` 溯源，演化链路不可追踪

---

## 2. Goals

- temporal 类问题通过动态时间窗口显著改善
- 引入 memory_class 三层分层（episodic / semantic / procedural），让高价值记忆在检索中获得更高权重
- observation / mental_model 真正进入 recall 主链路
- 为未来 Enterprise Track E4（Universal Memory Layer）预留字段兼容

## 3. Non-Goals

- 不实现 core memory blocks（E4 范畴）
- 不实现受控写入策略（E4 范畴）
- 不实现 scope 优先级检索（E4 范畴）
- 不实现 capture_mode / candidate_for 字段（E4 范畴）
- 不新增独立的 evolution 引擎

---

## 4. Schema Changes (V12 Migration)

### 4.1 新增列

```sql
ALTER TABLE memories ADD COLUMN memory_class TEXT NOT NULL DEFAULT 'episodic';
ALTER TABLE memories ADD COLUMN derived_from TEXT; -- JSON array, e.g. '["mem_abc","mem_def"]'
```

### 4.2 数据迁移

V12 迁移中按 `kind` 自动映射已有记忆：

| kind | → memory_class |
|------|----------------|
| `mental_model` | `procedural` |
| `consolidated` | `semantic` |
| 其余（note/fact/skill/profile 等） | `episodic` |

```sql
UPDATE memories SET memory_class = 'procedural' WHERE kind = 'mental_model';
UPDATE memories SET memory_class = 'semantic' WHERE kind = 'consolidated';
-- 其余已由 DEFAULT 'episodic' 覆盖
```

### 4.3 索引

```sql
CREATE INDEX idx_memories_memory_class ON memories(memory_class);
```

---

## 5. Temporal Dynamic Time Window

### 5.1 现状

`internal/search/preprocess.go` 中 temporal 检测后硬编码 7 天窗口：

```go
plan.TemporalRange = 7 * 24 * time.Hour
```

### 5.2 改进

按查询中的时间关键词动态设置窗口大小：

| 关键词模式 | 窗口大小 |
|-----------|---------|
| 今天/today | 1 天 |
| 昨天/yesterday | 1 天（center 偏移 -1d） |
| 这周/this week/最近几天 | 7 天 |
| 上周/last week | 7 天（center 偏移 -7d） |
| 这个月/this month/最近 | 30 天 |
| 上个月/last month | 30 天（center 偏移 -30d） |
| 最近几个月/recent months | 90 天 |
| 今年/this year | 365 天 |
| 去年/last year | 365 天（center 偏移 -365d） |
| 未匹配到具体时间词 | 默认 30 天 |

实现位置：`internal/search/preprocess.go` 的 `classifyIntent()` 或新增 `resolveTemporalWindow()` 函数。

### 5.3 CJK 安全

时间关键词匹配使用 `[]rune` 操作，避免中文字节截断。

---

## 6. Memory Evolution

### 6.1 三层模型

```
episodic (L0)  →  semantic (L1)  →  procedural (L2)
原始事实/事件      观察/提炼/规律      策略/心智模型/规则
```

### 6.2 演化触发（双驱动）

#### Consolidation 驱动（定时）

现有 `internal/memory/consolidation.go` 的 `Run()` 方法扩展：
- 合并相似 episodic 记忆时，产出记忆标记为 `memory_class = "semantic"`
- 设置 `derived_from` 为被合并记忆的 ID JSON 数组
- 已有 `kind = "consolidated"` 保持不变，`memory_class` 独立表达层级

#### Reflect 驱动（按需）

现有 `internal/reflect/engine.go` 的 `Run()` 方法调整：
- auto_save 产出的结论从 `kind = "mental_model"` 改为同时设置 `memory_class = "procedural"`
- `derived_from` 设置为该轮 reflect 使用的证据记忆 ID

#### Heartbeat 驱动（巡检）

现有 `internal/heartbeat/engine.go` 新增晋升检查：
- 扫描 `memory_class = 'episodic'` 且 `reinforced_count >= threshold` 的记忆
- 达阈值（默认 5 次）自动提升为 `memory_class = 'semantic'`
- 配置项：`heartbeat.promotion_threshold`（默认 5）
- 配置门控：`heartbeat.promotion_enabled`（默认 true，依赖 heartbeat.enabled）

### 6.3 演化规则

- episodic → semantic：consolidation 合并 或 reinforced_count 达阈值
- semantic → procedural：仅 reflect 可产出，不自动晋升
- 降级：不自动降级，仅 heartbeat 衰减 strength

---

## 7. Retrieval Adjustments

### 7.1 memory_class 权重

扩展 `internal/search/retriever.go` 的权重逻辑：

```go
var classWeights = map[string]float64{
    "procedural": 1.5,
    "semantic":   1.2,
    "episodic":   1.0,
}
```

与现有 `kindWeights` 叠乘：`finalWeight = kindWeight × classWeight`

### 7.2 检索过滤

`RetrieveRequest` 新增可选字段：

```go
MemoryClass string `json:"memory_class,omitempty"` // 过滤指定层级
```

不传时返回所有层级（向后兼容）。

---

## 8. API Changes

### 8.1 CreateMemoryRequest

新增可选字段：

```go
MemoryClass string `json:"memory_class,omitempty"` // episodic(default) / semantic / procedural
DerivedFrom []string `json:"derived_from,omitempty"` // 来源记忆 ID 列表
```

### 8.2 Memory Response

返回中包含 `memory_class` 和 `derived_from` 字段。

### 8.3 向后兼容

- 不传 `memory_class` 时默认 `episodic`
- 不传 `derived_from` 时为 null
- 现有所有 API 行为不变

---

## 9. Implementation Mapping

| 改动位置 | 改动内容 |
|---------|---------|
| `internal/store/sqlite_migration.go` | V12：加 memory_class + derived_from 列 + 数据迁移 + 索引 |
| `internal/model/memory.go` | Memory struct 加两个字段 |
| `internal/model/request.go` | CreateMemoryRequest / RetrieveRequest 加字段 |
| `internal/search/preprocess.go` | 动态时间窗口解析 |
| `internal/search/retriever.go` | classWeights + memory_class 过滤 |
| `internal/memory/consolidation.go` | 产出标记 semantic + derived_from |
| `internal/reflect/engine.go` | 产出标记 procedural + derived_from |
| `internal/heartbeat/engine.go` | reinforced_count 晋升逻辑 |
| `internal/store/sqlite_memory.go` | Create/Update/scan 支持新字段 |
| `internal/api/memory_handler.go` | DTO 序列化新字段 |

---

## 10. Test Plan

### 10.1 Schema Migration

- V12 迁移幂等（可重跑）
- mental_model → procedural 映射正确
- consolidated → semantic 映射正确
- 其余记忆 → episodic

### 10.2 Temporal Window

- "昨天" → 1 天窗口 + center 偏移 -1d
- "上个月" → 30 天窗口 + center 偏移 -30d
- "今年" → 365 天窗口
- 无时间词 → 默认 30 天
- 中文关键词不截断

### 10.3 Evolution

- consolidation 产出 memory_class=semantic + derived_from 非空
- reflect auto_save 产出 memory_class=procedural + derived_from 非空
- heartbeat 晋升：reinforced_count=5 的 episodic → semantic
- heartbeat 晋升：reinforced_count=4 不晋升

### 10.4 Retrieval

- procedural 权重 1.5x 生效
- semantic 权重 1.2x 生效
- memory_class 过滤返回正确子集
- 不传 memory_class 返回全部（兼容）

### 10.5 Backward Compatibility

- 旧版 CreateMemoryRequest 不传新字段 → 默认 episodic
- 旧版 RetrieveRequest 行为不变
- 现有测试全部通过

---

## 11. Risks

### 风险 1：映射不准确

mental_model 不全是策略类（有些可能是观察类）。

**缓解**：V12 迁移用保守映射，后续可通过 heartbeat 巡检修正。

### 风险 2：classWeight × kindWeight 叠乘过度放大

procedural + skill = 1.5 × 1.5 = 2.25x，可能过度偏好。

**缓解**：叠乘后设上限 cap = 2.0，超过截断。

### 风险 3：heartbeat 晋升误判

高频强化的 episodic 不一定值得晋升（如重复性低价值记忆）。

**缓解**：晋升阈值可配置，默认保守（5 次），且仅提升到 semantic 不到 procedural。

---

## 12. Relation to E4 (Universal Memory Layer)

本设计的 `memory_class` 字段与 E4 Spec 完全兼容：
- E4 只需加一个值 `core`
- E4 的 `capture_mode` / `candidate_for` 为独立新增字段
- 本设计不引入任何与 E4 冲突的结构

前向兼容已确认，零破坏性升级。
