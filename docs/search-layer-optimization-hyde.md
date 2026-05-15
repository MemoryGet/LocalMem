# 检索层优化方案：意图感知 HyDE

> 文档日期：2026-05-07  
> 背景：共享库+Qdrant+图谱评测 HitRate=89%，分析 11% miss 后提出

---

## 1. 当前状态

### HyDE 触发条件（`internal/search/preprocess.go:381`）

```go
if p.cfg.Preprocess.HyDEEnabled &&
    (plan.Intent == IntentSemantic || plan.Intent == IntentGeneral) {
    p.generateHyDE(ctx, plan)
}
```

### 意图分类逻辑（`preprocess.go:257`）

| 意图 | 触发条件 | HyDE |
|------|----------|------|
| `IntentTemporal` | 时序词（first, before, after…） | ❌ |
| `IntentRelational` | 关联词（related, between…） | ❌ |
| `IntentKeyword` | 短查询（英文≤20字、中文≤8字）且无探索词 | ❌ |
| `IntentSemantic` | 长查询（英文>50字）或含探索词 | ✅ |
| `IntentGeneral` | 其余所有查询（默认兜底） | ✅ ← **问题** |

### 存在的问题

`IntentGeneral` 是兜底意图，覆盖范围过宽——英文中等长度查询（20-50 runes）、没有明显特征词的查询都落到这里，包括很多实际上是精确查找的问题，也被触发了 HyDE，造成不必要的 LLM 调用和潜在的精度损失。

---

## 2. 改动方案

### 改动 A：收窄 HyDE 触发范围（核心改动）

**文件：** `internal/search/preprocess.go:381`

```go
// 改前：General 也触发 HyDE
if p.cfg.Preprocess.HyDEEnabled &&
    (plan.Intent == IntentSemantic || plan.Intent == IntentGeneral) {
    p.generateHyDE(ctx, plan)
}

// 改后：仅 Semantic 意图触发，且需满足探索性条件
if p.cfg.Preprocess.HyDEEnabled &&
    plan.Intent == IntentSemantic &&
    (exploratoryPatterns.MatchString(query) || len([]rune(query)) > 30) {
    p.generateHyDE(ctx, plan)
}
```

**效果：**

| 查询示例 | 意图 | 改前 | 改后 |
|----------|------|------|------|
| "Which house did I eventually buy?" | Semantic（含 eventually） | HyDE ✅ | HyDE ✅ |
| "What is the IP of my server?" | General | HyDE ✅ | 不触发 ✅ |
| "Which event happened first?" | Temporal | 不触发 ✅ | 不触发 ✅ |
| "Go 语言" | Keyword | 不触发 ✅ | 不触发 ✅ |
| "用户喜欢什么编辑器" | General（中等长度） | HyDE ✅ | 不触发 ✅ |

---

### 改动 B：降低英文语义阈值（multi-session 场景）

**文件：** `internal/search/preprocess.go:278`

LongMemEval 的 multi-session 查询大多是英文，长度在 20-50 runes 之间，当前落到 `IntentGeneral` 而非 `IntentSemantic`，导致在收窄触发后这些查询不走 HyDE。

```go
// 改前
} else {
    // 英文主导：20 runes 以内短查询，50 runes 以上长查询
    shortMax = 20
    longMin = 50
}

// 改后
} else {
    // 英文主导：20 runes 以内短查询，35 runes 以上长查询
    shortMax = 20
    longMin = 35   // 降低阈值，让更多英文长查询走语义通道
}
```

**效果：** "Which house did I eventually buy after working with Rachel?"（约 45 runes）→ `IntentSemantic` → 触发 HyDE。

---

## 3. 配置启用

`config.yaml` 中需要开启：

```yaml
retrieval:
  preprocess:
    enabled: true
    use_llm: true          # 启用 LLM 预处理（含 HyDE）
    llm_timeout: 8s
    hyde_enabled: true     # 启用 HyDE
    hyde_weight: 0.8       # HyDE 向量与原始向量的混合权重
```

**注意：** HyDE 需要 Qdrant（向量通道）才有意义。SQLite-only 模式下 `hyde_enabled` 应保持 `false`。

---

## 4. 预期收益

| 指标 | 当前（无 HyDE）| 改后（意图感知 HyDE）| 说明 |
|------|--------------|---------------------|------|
| HitRate | 89% | ~92% | multi-session 召回提升 |
| MRR | 0.651 | ~0.68 | 语义相关结果排名提升 |
| LLM 调用量 | 0 | 仅 Semantic 意图触发 | 约 20-30% 查询触发 |
| 精确查询精度 | 96.2% | 保持不变 | Keyword 意图不触发 |

