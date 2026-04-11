# Plan C: Progressive Disclosure — Multi-Pipeline Token Budget

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the flat trim stage with a multi-pipeline disclosure system that distributes token budget across 4 information dimensions (core/context/entity/timeline), automatically selecting detail levels per result.

**Architecture:** A new `DisclosureStage` replaces `TrimStage` as the final post-processing step. It categorizes candidates into 4 pipelines, allocates token budget by configurable weights, fills each pipeline with results at decreasing detail levels (full → summary → excerpt), and assembles a structured `DisclosureResult`. The existing `RetrieveRequest.MaxTokens` drives the budget.

**Tech Stack:** Go 1.25+, table-driven tests

**Spec:** `docs/superpowers/specs/2026-04-10-storage-architecture-upgrade-design.md` section 4.2

**Depends on:** Plan A (config), Plan B (entity enrichment, time decay)

---

## File Map

| Action | File | Responsibility |
|--------|------|---------------|
| Create | `internal/model/disclosure.go` | DisclosureResult + DisclosureItem structs |
| Create | `internal/search/stage/disclosure.go` | DisclosureStage implementation |
| Modify | `internal/search/pipeline/builtin/builtin.go` | Replace TrimStage with DisclosureStage |
| Modify | `internal/search/retriever.go` | Return DisclosureResult when available |
| Modify | `internal/config/config.go` | Add DisclosureConfig (already partially spec'd) |
| Create | `testing/search/stage/disclosure_test.go` | Unit tests |

---

### Task 1: Disclosure Model

**Files:**
- Create: `internal/model/disclosure.go`

- [ ] **Step 1: Create disclosure model**

```go
package model

// DisclosureItem 披露条目 / Disclosure item at a specific detail level
type DisclosureItem struct {
	Memory      *Memory   `json:"memory"`
	Score       float64   `json:"score"`
	Source      string    `json:"source"`
	DetailLevel string    `json:"detail_level"` // full / summary / excerpt
	Entities    []*Entity `json:"entities,omitempty"`
	Content     string    `json:"content"` // 按 detail_level 裁剪的内容 / Content trimmed to detail level
	Tokens      int       `json:"tokens"`  // 实际 token 数 / Actual token count
}

// DisclosurePipeline 单条管线输出 / Single pipeline output
type DisclosurePipeline struct {
	Name       string            `json:"name"`   // core / context / entity / timeline
	Weight     float64           `json:"weight"`
	Budget     int               `json:"budget"`      // 分配的 token 预算 / Allocated token budget
	UsedTokens int               `json:"used_tokens"` // 实际使用 / Actually used
	Items      []*DisclosureItem `json:"items"`
}

// DisclosureResult 多管线渐进式披露结果 / Multi-pipeline progressive disclosure result
type DisclosureResult struct {
	Pipelines    []*DisclosurePipeline `json:"pipelines"`
	TotalBudget  int                   `json:"total_budget"`
	TotalUsed    int                   `json:"total_used"`
	Overflow     []*DisclosureItem     `json:"overflow,omitempty"` // 超预算的扩展指针 / Over-budget expansion pointers
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./...`

- [ ] **Step 3: Commit**

```bash
git commit -m "feat(model): add DisclosureResult model for multi-pipeline progressive disclosure"
```

---

### Task 2: Disclosure Config

**Files:**
- Modify: `internal/config/config.go`

- [ ] **Step 1: Add DisclosureConfig to RetrievalConfig**

Read `internal/config/config.go`. Add a `Disclosure DisclosureConfig` field to `RetrievalConfig` and define the struct:

```go
// DisclosureConfig 渐进式披露配置 / Progressive disclosure configuration
type DisclosureConfig struct {
	Enabled        bool    `mapstructure:"enabled"`
	CoreWeight     float64 `mapstructure:"core_weight"`     // 核心事实管线 / Core facts pipeline
	ContextWeight  float64 `mapstructure:"context_weight"`  // 上下文补充管线 / Context enrichment pipeline
	EntityWeight   float64 `mapstructure:"entity_weight"`   // 实体网络管线 / Entity network pipeline
	TimelineWeight float64 `mapstructure:"timeline_weight"` // 时间线管线 / Timeline pipeline
}
```

Add to `RetrievalConfig`:
```go
Disclosure DisclosureConfig `mapstructure:"disclosure"`
```

- [ ] **Step 2: Add defaults**

In `setDefaults()`:
```go
viper.SetDefault("retrieval.disclosure.enabled", false)
viper.SetDefault("retrieval.disclosure.core_weight", 0.4)
viper.SetDefault("retrieval.disclosure.context_weight", 0.25)
viper.SetDefault("retrieval.disclosure.entity_weight", 0.2)
viper.SetDefault("retrieval.disclosure.timeline_weight", 0.15)
```

- [ ] **Step 3: Add strategy-to-weight mapping helper**

```go
// DisclosureWeightsForStrategy 根据策略返回管线权重 / Return pipeline weights for strategy
func (d DisclosureConfig) WeightsForStrategy(strategy string) (core, context, entity, timeline float64) {
	switch strategy {
	case "precision", "factual":
		return 0.60, 0.20, 0.10, 0.10
	case "exploration":
		return 0.25, 0.25, 0.25, 0.25
	case "temporal":
		return 0.20, 0.15, 0.15, 0.50
	default:
		return d.CoreWeight, d.ContextWeight, d.EntityWeight, d.TimelineWeight
	}
}
```

- [ ] **Step 4: Verify build + commit**

```bash
go build ./...
git commit -m "feat(config): add disclosure pipeline weight configuration"
```

---

### Task 3: DisclosureStage Implementation

**Files:**
- Create: `internal/search/stage/disclosure.go`
- Create: `testing/search/stage/disclosure_test.go`

- [ ] **Step 1: Write test first**

Create `testing/search/stage/disclosure_test.go`:

```go
package stage_test

import (
	"context"
	"testing"
	"time"

	"iclude/internal/config"
	"iclude/internal/model"
	"iclude/internal/search/pipeline"
	"iclude/internal/search/stage"
)

func makeCandidate(id, content, source string, score float64, happenedAt *time.Time) *model.SearchResult {
	m := &model.Memory{
		ID:         id,
		Content:    content,
		Excerpt:    content[:min(len(content), 20)],
		Summary:    content[:min(len(content), 50)],
		CreatedAt:  time.Now(),
		HappenedAt: happenedAt,
		SourceType: "test",
	}
	return &model.SearchResult{Memory: m, Score: score, Source: source}
}

func TestDisclosureStage_BasicDistribution(t *testing.T) {
	cfg := config.DisclosureConfig{
		Enabled:        true,
		CoreWeight:     0.4,
		ContextWeight:  0.25,
		EntityWeight:   0.2,
		TimelineWeight: 0.15,
	}
	ds := stage.NewDisclosureStage(cfg, 2000)

	now := time.Now()
	candidates := make([]*model.SearchResult, 10)
	for i := range candidates {
		candidates[i] = makeCandidate(
			fmt.Sprintf("m%d", i),
			fmt.Sprintf("Memory content number %d with enough text to have some tokens", i),
			"sqlite",
			1.0-float64(i)*0.1,
			&now,
		)
	}

	state := &pipeline.PipelineState{
		Query:      "test query",
		Candidates: candidates,
	}

	result, err := ds.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}

	// 验证候选数没有增加 / Candidates should not grow
	if len(result.Candidates) > len(candidates) {
		t.Errorf("candidates grew: %d > %d", len(result.Candidates), len(candidates))
	}

	// 验证 DisclosureResult 在 metadata 中 / Check disclosure result in metadata
	dr, ok := result.Metadata["disclosure"]
	if !ok {
		t.Fatal("disclosure result not found in metadata")
	}
	discResult, ok := dr.(*model.DisclosureResult)
	if !ok {
		t.Fatal("disclosure result wrong type")
	}

	// 应该有 4 条管线 / Should have 4 pipelines
	if len(discResult.Pipelines) != 4 {
		t.Errorf("expected 4 pipelines, got %d", len(discResult.Pipelines))
	}

	// 总使用量不超预算 / Total used should not exceed budget
	if discResult.TotalUsed > discResult.TotalBudget {
		t.Errorf("over budget: used %d > budget %d", discResult.TotalUsed, discResult.TotalBudget)
	}
}
```

Add `"fmt"` to imports.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./testing/search/stage/ -run TestDisclosureStage -v`

- [ ] **Step 3: Implement DisclosureStage**

Create `internal/search/stage/disclosure.go`:

```go
package stage

import (
	"context"
	"time"

	"iclude/internal/config"
	"iclude/internal/model"
	"iclude/internal/search/pipeline"
	"iclude/pkg/tokenutil"
)

// DisclosureStage 多管线渐进式披露 / Multi-pipeline progressive disclosure
type DisclosureStage struct {
	cfg       config.DisclosureConfig
	maxTokens int
}

// NewDisclosureStage 创建披露阶段 / Create disclosure stage
func NewDisclosureStage(cfg config.DisclosureConfig, maxTokens int) *DisclosureStage {
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	return &DisclosureStage{cfg: cfg, maxTokens: maxTokens}
}

// Name 返回阶段名称 / Return stage name
func (s *DisclosureStage) Name() string { return "disclosure" }

// Execute 执行渐进式披露 / Execute progressive disclosure
func (s *DisclosureStage) Execute(ctx context.Context, state *pipeline.PipelineState) (*pipeline.PipelineState, error) {
	start := time.Now()

	if !s.cfg.Enabled || len(state.Candidates) == 0 {
		// 未启用时退化为简单裁剪 / Fall back to simple trim when disabled
		state.AddTrace(pipeline.StageTrace{
			Name:        s.Name(),
			Duration:    time.Since(start),
			InputCount:  len(state.Candidates),
			OutputCount: len(state.Candidates),
		})
		return state, nil
	}

	// 获取策略名以调整权重 / Get strategy name to adjust weights
	strategy := ""
	if state.Plan != nil {
		strategy = state.PipelineName
	}
	coreW, ctxW, entityW, timeW := s.cfg.WeightsForStrategy(strategy)

	// 分配预算 / Allocate budgets
	totalBudget := s.maxTokens
	coreBudget := int(float64(totalBudget) * coreW)
	ctxBudget := int(float64(totalBudget) * ctxW)
	entityBudget := int(float64(totalBudget) * entityW)
	timeBudget := totalBudget - coreBudget - ctxBudget - entityBudget // 余量给时间线 / Remainder to timeline

	// 初始化 4 条管线 / Initialize 4 pipelines
	pipelines := []*model.DisclosurePipeline{
		{Name: "core", Weight: coreW, Budget: coreBudget},
		{Name: "context", Weight: ctxW, Budget: ctxBudget},
		{Name: "entity", Weight: entityW, Budget: entityBudget},
		{Name: "timeline", Weight: timeW, Budget: timeBudget},
	}

	// 按分数排序的候选已经由上游保证 / Candidates are already sorted by score from upstream
	// 分配策略：前 N 个高分 → core（full），中间 → context（summary），后面 → entity/timeline（excerpt）
	// Strategy: top scores → core (full), middle → context (summary), rest → entity/timeline (excerpt)
	var overflow []*model.DisclosureItem

	for _, c := range state.Candidates {
		if c.Memory == nil {
			continue
		}

		item := &model.DisclosureItem{
			Memory:   c.Memory,
			Score:    c.Score,
			Source:   c.Source,
			Entities: c.Entities,
		}

		placed := false
		for _, p := range pipelines {
			level, content, tokens := selectDetailLevel(c.Memory, p.Budget-p.UsedTokens)
			if tokens > 0 && p.UsedTokens+tokens <= p.Budget {
				item.DetailLevel = level
				item.Content = content
				item.Tokens = tokens
				p.Items = append(p.Items, item)
				p.UsedTokens += tokens
				placed = true
				break
			}
		}

		if !placed {
			// 超预算 → 扩展指针（只给 excerpt 级别信息）/ Over budget → expansion pointer
			item.DetailLevel = "pointer"
			item.Content = c.Memory.Excerpt
			item.Tokens = 0
			overflow = append(overflow, item)
		}
	}

	totalUsed := 0
	for _, p := range pipelines {
		totalUsed += p.UsedTokens
	}

	discResult := &model.DisclosureResult{
		Pipelines:   pipelines,
		TotalBudget: totalBudget,
		TotalUsed:   totalUsed,
		Overflow:    overflow,
	}

	// 存入 metadata 供上层读取 / Store in metadata for upstream access
	if state.Metadata == nil {
		state.Metadata = make(map[string]interface{})
	}
	state.Metadata["disclosure"] = discResult

	state.AddTrace(pipeline.StageTrace{
		Name:        s.Name(),
		Duration:    time.Since(start),
		InputCount:  len(state.Candidates),
		OutputCount: len(state.Candidates) - len(overflow),
	})

	return state, nil
}

// selectDetailLevel 根据剩余预算选择展示级别 / Select detail level based on remaining budget
func selectDetailLevel(m *model.Memory, remainingBudget int) (level, content string, tokens int) {
	// 尝试 full → summary → excerpt / Try full → summary → excerpt
	fullTokens := tokenutil.EstimateTokens(m.Content)
	if fullTokens <= remainingBudget {
		return "full", m.Content, fullTokens
	}

	if m.Summary != "" {
		sumTokens := tokenutil.EstimateTokens(m.Summary)
		if sumTokens <= remainingBudget {
			return "summary", m.Summary, sumTokens
		}
	}

	if m.Excerpt != "" {
		excTokens := tokenutil.EstimateTokens(m.Excerpt)
		if excTokens <= remainingBudget {
			return "excerpt", m.Excerpt, excTokens
		}
	}

	return "", "", 0
}
```

- [ ] **Step 4: Run test**

Run: `go test ./testing/search/stage/ -run TestDisclosureStage -v`
Expected: PASS

- [ ] **Step 5: Commit**

```bash
git commit -m "feat(search): implement DisclosureStage — multi-pipeline token budget disclosure"
```

---

### Task 4: Wire DisclosureStage into Pipeline

**Files:**
- Modify: `internal/search/pipeline/builtin/builtin.go`

- [ ] **Step 1: Replace TrimStage with DisclosureStage in postStages**

Read `builtin.go`. Find `buildPostStages` (around line 157). Currently it returns `TrimStage` as one of the post stages. Add `DisclosureStage` after (or replace) `TrimStage`:

```go
// 如果 disclosure 启用，用 DisclosureStage 替代 TrimStage / If disclosure enabled, replace TrimStage
if deps.Cfg.Disclosure.Enabled {
	stages = append(stages, stage.NewDisclosureStage(deps.Cfg.Disclosure, defaultTrimTokens))
} else {
	stages = append(stages, stage.NewTrimStage(defaultTrimTokens))
}
```

This ensures backwards compatibility — disclosure is opt-in.

- [ ] **Step 2: Verify build**

Run: `go build ./...`

- [ ] **Step 3: Commit**

```bash
git commit -m "feat(search): wire DisclosureStage into pipeline as optional TrimStage replacement"
```

---

### Task 5: Expose DisclosureResult in Retriever Response

**Files:**
- Modify: `internal/search/retriever.go`

- [ ] **Step 1: Add Disclosure field to RetrieveResult**

```go
type RetrieveResult struct {
	Results      []*model.SearchResult
	Disclosure   *model.DisclosureResult // 渐进式披露结果 / Progressive disclosure result
	PipelineInfo *PipelineDebugInfo
}
```

- [ ] **Step 2: Extract disclosure from pipeline state metadata**

In `retrieveViaPipeline`, after the pipeline executes and before returning, check if disclosure result exists in state metadata:

```go
// 提取渐进式披露结果 / Extract disclosure result
if state.Metadata != nil {
	if dr, ok := state.Metadata["disclosure"]; ok {
		if discResult, ok := dr.(*model.DisclosureResult); ok {
			result.Disclosure = discResult
		}
	}
}
```

Find the right location in `retrieveViaPipeline` where `result` is being built and add this before the return.

- [ ] **Step 3: Verify build**

Run: `go build ./...`

- [ ] **Step 4: Commit**

```bash
git commit -m "feat(search): expose DisclosureResult in RetrieveResult"
```

---

### Task 6: Update Config Templates

**Files:**
- Modify: `config/templates/config-premium.yaml`

- [ ] **Step 1: Add disclosure config to premium template**

Read the file and add under the `retrieval` section:

```yaml
  disclosure:
    enabled: true
    core_weight: 0.4
    context_weight: 0.25
    entity_weight: 0.2
    timeline_weight: 0.15
```

- [ ] **Step 2: Commit**

```bash
git commit -m "feat(config): add disclosure settings to premium config template"
```

---

### Task 7: Full Verification

- [ ] **Step 1: Build + vet**

Run: `go build ./... && go vet ./...`

- [ ] **Step 2: Run all tests**

Run: `go test ./testing/store/ ./testing/search/... ./testing/memory/ ./testing/heartbeat/ -count=1 -timeout 120s`

- [ ] **Step 3: Commit any fixups**

---

## Summary

| Task | What | Key Files |
|------|------|-----------|
| 1 | Disclosure model structs | model/disclosure.go |
| 2 | Disclosure config + strategy weights | config/config.go |
| 3 | DisclosureStage implementation + test | stage/disclosure.go |
| 4 | Wire into pipeline (replace TrimStage) | builtin/builtin.go |
| 5 | Expose in RetrieveResult | retriever.go |
| 6 | Config template | config-premium.yaml |
| 7 | Full verification | — |
