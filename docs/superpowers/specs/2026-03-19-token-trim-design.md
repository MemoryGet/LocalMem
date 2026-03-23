# Token 感知裁剪设计规格 / Token-Aware Trimming Design Spec

**日期 / Date:** 2026-03-19
**状态 / Status:** Approved
**阶段 / Phase:** Phase 2 Task 4
**预估工期 / Estimated effort:** 1 week

## 概述 / Overview

在 Retrieve 检索管道末尾增加 token 预算裁剪能力。按 RRF 分数从高到低累加 token 数，超出预算则截断，确保返回结果不超出 LLM context window。

Adds token budget trimming at the end of the Retrieve pipeline. Accumulates token counts from highest to lowest RRF score, truncating when budget is exceeded, ensuring returned results fit within LLM context windows.

## 设计决策 / Design Decisions

| 决策项 / Decision | 选择 / Choice | 原因 / Rationale |
|-------------------|---------------|-------------------|
| Token 计数方式 / Token counting | 简单字符估算（rune 数）/ Simple character estimation (rune count) | 零依赖零延迟，±20% 误差可接受 / Zero dependency, zero latency, ±20% error acceptable |

## 1. 核心实现 / Core Implementation（`internal/search/retriever.go` 新增 / additions）

### 1.1 Token 估算 / Token Estimation

```go
// estimateTokens 估算文本token数 / Estimate token count for text
// 使用 rune 数作为 token 估算：中文 1 rune ≈ 1 token，英文偏保守（安全）
// Uses rune count as token estimate: Chinese 1 rune ≈ 1 token, English conservative (safe)
func estimateTokens(text string) int {
    return len([]rune(text))
}
```

> **精度说明 / Accuracy note:** 中文 1 rune ≈ 1 token（准确），英文 1 rune ≈ 0.25 token（偏保守，会少放结果）。整体偏保守，安全——宁可少返回也不超出预算。
> Chinese 1 rune ≈ 1 token (accurate), English 1 rune ≈ 0.25 token (conservative, returns fewer results). Overall conservative and safe — better to return less than exceed budget.

### 1.2 裁剪函数 / Trim Function

```go
// trimByTokenBudget 按token预算裁剪检索结果 / Trim search results by token budget
// 从高分到低分累加 memory.Content 的 token 数，超出预算则截断
// Accumulates tokens from highest to lowest score, truncates when budget exceeded
// 至少返回 1 条结果（即使单条超出预算）/ Returns at least 1 result (even if single result exceeds budget)
// 返回: (裁剪后结果, 总token数, 是否截断) / Returns: (trimmed results, total tokens, whether truncated)
func trimByTokenBudget(results []*model.SearchResult, maxTokens int) ([]*model.SearchResult, int, bool)
```

### 1.3 管道集成位置 / Pipeline Integration Point

```
现有 Retrieve 管道 / Existing Retrieve pipeline:
  ① FTS5/Qdrant/Graph 检索 → ② RRF 融合 → ③ 强度加权 → ④ 按 limit 截断
  / retrieval → RRF fusion → strength weighting → limit truncation

新增步骤 ⑤（在 ④ 之后）/ New step ⑤ (after ④):
  ⑤ Token 裁剪（仅当 req.MaxTokens > 0 时触发）
  / Token trimming (only triggered when req.MaxTokens > 0)

伪代码 / Pseudocode:
  if req.MaxTokens > 0 {
      results, totalTokens, truncated = trimByTokenBudget(results, req.MaxTokens)
  }
```

## 2. 请求/响应模型变更 / Request/Response Model Changes（`internal/model/request.go`）

### 2.1 RetrieveRequest 新增字段 / New field

```go
type RetrieveRequest struct {
    // ... 现有字段不变 / existing fields unchanged
    MaxTokens int // 新增：token 预算，0 表示不裁剪（默认 0）
                  // new: token budget, 0 means no trimming (default 0)
}
```

### 2.2 RetrieveResponse 新增字段 / New fields

```go
type RetrieveResponse struct {
    Results     []*SearchResult `json:"results"`
    TotalTokens int             `json:"total_tokens"` // 新增：实际返回的总 token 数 / actual total tokens returned
    Truncated   bool            `json:"truncated"`    // 新增：是否因 token 预算截断 / whether truncated by token budget
}
```

### 2.3 API 响应示例 / API Response Example

**请求 / Request:** `POST /v1/retrieve`
```json
{
  "query": "用户的技术栈",
  "max_tokens": 500
}
```

**响应 / Response:**
```json
{
  "code": 0,
  "data": {
    "results": [
      {"memory": {...}, "score": 0.85, "source": "hybrid"},
      {"memory": {...}, "score": 0.72, "source": "sqlite"}
    ],
    "total_tokens": 487,
    "truncated": true
  }
}
```

## 3. 文件变更清单 / File Change List

### 新增 1 个文件 / 1 New File

| 文件 / File | 说明 / Description |
|-------------|---------------------|
| `testing/search/token_trim_test.go` | Token 估算 + 裁剪测试 / Token estimation + trimming tests |

### 修改 3 个文件 / 3 Modified Files

| 文件 / File | 变更 / Changes |
|-------------|----------------|
| `internal/search/retriever.go` | 新增 `estimateTokens()` + `trimByTokenBudget()` + Retrieve() 末尾集成 / Add functions + pipeline integration |
| `internal/model/request.go` | RetrieveRequest.MaxTokens + RetrieveResponse.TotalTokens/Truncated / Add fields |
| `internal/api/search_handler.go` | 传递 MaxTokens + 返回 TotalTokens/Truncated / Pass and return new fields |

## 4. 测试计划 / Test Plan（`testing/search/token_trim_test.go`）

| 用例 / Test Case | 说明 / Description |
|-------------------|---------------------|
| TestEstimateTokens_Chinese | 纯中文 token 估算准确 / Pure Chinese token estimation accuracy |
| TestEstimateTokens_English | 纯英文 token 估算（偏保守）/ Pure English token estimation (conservative) |
| TestEstimateTokens_Mixed | 中英混合文本 / Mixed Chinese-English text |
| TestEstimateTokens_Empty | 空字符串返回 0 / Empty string returns 0 |
| TestTrimByTokenBudget_NoTrim | 预算充足不截断 / Sufficient budget, no truncation |
| TestTrimByTokenBudget_Trim | 预算不足截断到第 N 条 / Insufficient budget, truncate at Nth result |
| TestTrimByTokenBudget_ZeroBudget | MaxTokens=0 不裁剪（返回全部）/ MaxTokens=0 returns all |
| TestTrimByTokenBudget_SingleResultExceedsBudget | 单条记忆超预算仍返回至少 1 条 / Single result exceeds budget, still returns at least 1 |
| TestTrimByTokenBudget_EmptyResults | 空结果集返回空 / Empty results returns empty |
