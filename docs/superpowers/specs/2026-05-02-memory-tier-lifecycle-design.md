# Memory Tier Lifecycle Design

**Date:** 2026-05-02  
**Status:** Approved  
**Scope:** 让记忆层级系统真正运转——写入自动分配、使用触发晋升、过期自动清理

---

## 背景

系统已定义五个 RetentionTier（ephemeral / short_term / standard / long_term / permanent），衰减参数完整，但写入路径 99% 落到 `standard`。`MemoryClass`（episodic/semantic/procedural/core）与 `RetentionTier` 互不连通，`short_term`/`ephemeral` 几乎没有写入路径，生命周期处于"纸面上存在"的状态。

---

## 目标

1. 写入时按 MemoryClass + kind 自动分配正确 Tier
2. 使用频率和强化次数触发 Tier 晋升
3. ephemeral 到期后自动软删除
4. MemoryClass 晋升时同步更新 Tier（两系统联动）

---

## 设计

### 1. 写入时自动分配（ResolveTierFromClass）

在 `internal/memory/lifecycle.go` 新增 `ResolveTierFromClass(mem *model.Memory)`，在 `manager_create_helpers.go` 写入前调用，顺序在已有 `ResolveTierDefaults()` 之前。

**规则表（优先级从高到低）：**

| 条件 | 分配 Tier |
|------|-----------|
| 已显式设置 RetentionTier | 不覆盖，直接返回 |
| `kind == "conversation"` | `ephemeral` |
| `memory_class == "episodic"` | `short_term` |
| `memory_class == "semantic"` | `standard` |
| `memory_class == "procedural"` | `long_term` |
| `memory_class == "core"` | `permanent` |
| 其余（未设 class） | `standard` |

**说明：**
- consolidation/reflect 已显式写 `TierPermanent`/`TierLongTerm`，由"不覆盖"规则自然跳过，不需特殊处理
- `kind == "conversation"` 优先级高于 `memory_class`，确保对话轮次始终为 ephemeral

### 2. 辅助函数（minTierForClass）

在 `lifecycle.go` 新增 `minTierForClass(class string) string`，返回该 class 对应的最低 tier。供节 3 的 Promoter 联动使用，避免重复逻辑。

```
episodic   → short_term
semantic   → standard
procedural → long_term
core       → permanent
其余       → standard
```

### 3. MemoryClass Promoter 联动

`internal/memory/promoter.go` 在提升 MemoryClass（如 episodic_candidate → semantic）后，调用 `minTierForClass(newClass)` 检查当前 tier 是否低于新 class 的最低要求。若低则调用 `PromoteTier()` + `ResolveTierDefaults()` + `Store.Update()` 一并提升。

防止"class 升了但 tier 没跟上"的状态漂移。

### 4. Tier 晋升 Heartbeat 任务

新增 `internal/heartbeat/tier_promotion.go`，注册到 scheduler，建议间隔 1h（与 decay_audit 错开）。

**晋升条件：**

| 当前 Tier | 晋升到 | 条件 |
|-----------|--------|------|
| `short_term` | `standard` | `reinforced_count >= 2` AND `age >= 24h` AND `kind != "conversation"` |
| `standard` | `long_term` | `ShouldCrystallize()` 返回 true（复用已有判断） |

`long_term → permanent` 不自动晋升，保留给 Promoter 手动确认或 consolidation 显式写入。

**执行逻辑：**
1. 查询 `tier IN (short_term, standard)` 的活跃记忆，分批处理（limit 100）
2. `short_term` 前置过滤 `kind != "conversation"`，避免对话记忆被误晋升
3. `standard` 调用 `ShouldCrystallize()` 前确认 `tier == standard`，避免跳级
4. 满足条件 → 调用 `PromoteTier()` + `ResolveTierDefaults()` + `Store.Update()`
5. 记录晋升数量日志

### 5. Ephemeral 过期清理

新增 `internal/heartbeat/expiry_cleanup.go`（或扩展 `decay_audit.go`），注册到 scheduler。

**逻辑：**
```sql
WHERE expires_at IS NOT NULL AND expires_at < NOW() AND deleted_at IS NULL
→ 软删除（SET deleted_at = NOW()）
```

补全现有 decay_audit 未覆盖的定时过期路径。

---

## 完整生命周期图

```
写入（Create）
  └─ ResolveTierFromClass()        [lifecycle.go]
  └─ ResolveTierDefaults()         [lifecycle.go, 已有]

Promoter（class 晋升时）
  └─ minTierForClass(newClass)
      → tier 不足 → PromoteTier() + Update()

Heartbeat（每1小时）
  ├─ TierPromotionAuditor          [tier_promotion.go]
  │   ├─ short_term → standard: reinforce≥2 AND age≥24h
  │   └─ standard → long_term: ShouldCrystallize()
  ├─ DecayAudit                    [已有，覆盖所有非permanent]
  └─ ExpiryCleanup                 [expiry_cleanup.go]
      └─ expires_at < NOW() → 软删除
```

---

## 改动文件

| 文件 | 类型 | 估计行数 |
|------|------|---------|
| `internal/memory/lifecycle.go` | 修改 | +40 行（2个函数） |
| `internal/memory/manager_create_helpers.go` | 修改 | +1 行 |
| `internal/memory/promoter.go` | 修改 | +10 行 |
| `internal/heartbeat/tier_promotion.go` | 新增 | ~80 行 |
| `internal/heartbeat/expiry_cleanup.go` | 新增 | ~20 行 |
| `internal/heartbeat/inspector.go` | 修改 | +注册2个新任务 |
| `testing/memory/tier_lifecycle_test.go` | 新增 | ~120 行 |

---

## 测试覆盖

`testing/memory/tier_lifecycle_test.go` 表驱动测试：

- 6 条写入规则（各 class + kind=conversation + 显式tier不覆盖）
- short_term 晋升条件（满足 / 不满足强化次数 / 不满足年龄）
- standard 晶化条件（复用已有 ShouldCrystallize 测试）
- Promoter class 晋升 → tier 联动
- ephemeral 过期清理（已过期 / 未过期）

---

## 不在范围内

- long_term → permanent 自动晋升（保持手动，避免永久记忆膨胀）
- Tier 降级（现有 strength 衰减已处理，不需要显式降级）
- 配置化晋升阈值（当前硬编码，后续可扩展）
