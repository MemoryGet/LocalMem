# Search Layer Optimization Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Eliminate redundant normalization passes in the search pipeline so score discrimination is preserved from source to output, and remove the dual-path CascadeRetriever ambiguity.

**Architecture:** Structural weights (kind/class/scope/strength) are integrated directly into the RRF formula instead of applied as a separate WeightStage post-pass. CascadeRetriever is deleted; all traffic flows through the unified pipeline. HyDE is config-gated and intent-restricted to semantic queries only.

**Tech Stack:** Go 1.25+, `iclude/internal/search/stage`, `iclude/internal/search/pipeline/builtin`, `iclude/internal/search`, `iclude/internal/config`, `iclude/pkg/scoring`

---

## File Map

| Action | Path |
|--------|------|
| Modify | `internal/search/stage/merge.go` |
| Delete | `internal/search/stage/weight.go` |
| Modify | `testing/search/stage/merge_test.go` |
| Delete | `testing/search/stage/weight_test.go` |
| Modify | `internal/search/pipeline/builtin/builtin.go` |
| Delete | `internal/search/cascade.go` |
| Delete | `testing/search/cascade_test.go` |
| Modify | `internal/search/retriever.go` |
| Modify | `internal/bootstrap/wiring.go` |
| Modify | `internal/config/config.go` |
| Modify | `internal/search/preprocess.go` |
| Modify | `testing/search/preprocess_test.go` |

---

## Task 1: Integrate structural weights into RRF / 将结构权重融入 RRF 公式

**Goal:** Replace the WeightStage post-pass with a `computeStructuralWeight()` multiplier embedded inside the RRF term. One fewer normalization pass; quality signal is preserved.

**Files:**
- Modify: `internal/search/stage/merge.go`
- Modify: `testing/search/stage/merge_test.go`

---

- [ ] **Step 1: Write failing tests in merge_test.go**

Open `testing/search/stage/merge_test.go`. Make three changes:

**1a. Update all existing `NewMergeStage` calls from 3-param to 4-param** (add `0.1` as fourth arg):

```go
// Line 13
stage.NewMergeStage("rrf", 0, 0, 0.1)
// Line 20
stage.NewMergeStage("", 0, 0, 0.1)
// Line 75
stage.NewMergeStage("rrf", 60, limit, 0.1)
// Line 107
stage.NewMergeStage("rrf", 60, 100, 0.1)
// Line 132
stage.NewMergeStage("rrf", 60, 100, 0.1)
// Line 220
stage.NewMergeStage("graph_aware", 60, 100, 0.1)
// Line 254
stage.NewMergeStage("graph_aware", 60, 100, 0.1)
// Line 304
stage.NewMergeStage("rrf", 60, 100, 0.1)
```

**1b. Add new test cases at the bottom of the file:**

```go
func TestMergeStage_StructuralWeight_ExpiredFiltered(t *testing.T) {
    now := time.Now()
    past := now.Add(-1 * time.Hour)
    future := now.Add(1 * time.Hour)

    expired := &model.Memory{ID: "expired", Kind: "note", ExpiresAt: &past, Strength: 1.0}
    live := &model.Memory{ID: "live", Kind: "note", ExpiresAt: &future, Strength: 1.0}

    s := stage.NewMergeStage("rrf", 60, 100, 0.1)
    state := &pipeline.PipelineState{
        Candidates: []*model.SearchResult{
            {Memory: expired, Score: 0.9},
            {Memory: live, Score: 0.8},
        },
    }
    out, err := s.Execute(context.Background(), state)
    require.NoError(t, err)
    require.Len(t, out.Candidates, 1)
    assert.Equal(t, "live", out.Candidates[0].Memory.ID)
}

func TestMergeStage_StructuralWeight_SkillRanksAboveNote(t *testing.T) {
    skill := &model.Memory{ID: "skill", Kind: "skill", Strength: 0.8, MemoryClass: "procedural"}
    note := &model.Memory{ID: "note", Kind: "note", Strength: 0.8, MemoryClass: "episodic"}

    s := stage.NewMergeStage("rrf", 60, 100, 0.1)
    state := &pipeline.PipelineState{
        Candidates: []*model.SearchResult{
            {Memory: note, Score: 1.0, Source: "fts"},
            {Memory: skill, Score: 1.0, Source: "fts"},
        },
    }
    out, err := s.Execute(context.Background(), state)
    require.NoError(t, err)
    require.Len(t, out.Candidates, 2)
    assert.Equal(t, "skill", out.Candidates[0].Memory.ID, "skill (1.5x kind weight) should rank above note (1.0x)")
}

func TestMergeStage_StructuralWeight_SessionScopeBoost(t *testing.T) {
    session := &model.Memory{ID: "session", Kind: "note", Scope: "session/abc", Strength: 0.8}
    agent := &model.Memory{ID: "agent", Kind: "note", Scope: "agent/xyz", Strength: 0.8}

    s := stage.NewMergeStage("rrf", 60, 100, 0.1)
    state := &pipeline.PipelineState{
        Candidates: []*model.SearchResult{
            {Memory: agent, Score: 1.0, Source: "fts"},
            {Memory: session, Score: 1.0, Source: "fts"},
        },
    }
    out, err := s.Execute(context.Background(), state)
    require.NoError(t, err)
    require.Len(t, out.Candidates, 2)
    assert.Equal(t, "session", out.Candidates[0].Memory.ID, "session/ scope (1.3x) should rank above agent/ (1.0x)")
}

func TestMergeStage_StructuralWeight_ProceduralClass(t *testing.T) {
    procedural := &model.Memory{ID: "proc", Kind: "note", MemoryClass: "procedural", Strength: 0.8}
    episodic := &model.Memory{ID: "epis", Kind: "note", MemoryClass: "episodic", Strength: 0.8}

    s := stage.NewMergeStage("rrf", 60, 100, 0.1)
    state := &pipeline.PipelineState{
        Candidates: []*model.SearchResult{
            {Memory: episodic, Score: 1.0, Source: "fts"},
            {Memory: procedural, Score: 1.0, Source: "fts"},
        },
    }
    out, err := s.Execute(context.Background(), state)
    require.NoError(t, err)
    require.Len(t, out.Candidates, 2)
    assert.Equal(t, "proc", out.Candidates[0].Memory.ID, "procedural class (1.5x) should rank above episodic (1.0x)")
}

func TestMergeStage_StructuralWeight_PermanentNoDecay(t *testing.T) {
    oldTime := time.Now().Add(-720 * time.Hour)
    permanent := &model.Memory{
        ID: "perm", Kind: "note", Strength: 0.8,
        RetentionTier: model.TierPermanent, LastAccessedAt: &oldTime,
    }
    standard := &model.Memory{
        ID: "std", Kind: "note", Strength: 0.8,
        RetentionTier: model.TierStandard, DecayRate: 0.01, LastAccessedAt: &oldTime,
    }

    s := stage.NewMergeStage("rrf", 60, 100, 0.1)
    state := &pipeline.PipelineState{
        Candidates: []*model.SearchResult{
            {Memory: standard, Score: 1.0, Source: "fts"},
            {Memory: permanent, Score: 1.0, Source: "fts"},
        },
    }
    out, err := s.Execute(context.Background(), state)
    require.NoError(t, err)
    require.Len(t, out.Candidates, 2)
    assert.Equal(t, "perm", out.Candidates[0].Memory.ID, "permanent tier should outrank standard after 720h decay")
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test -race ./testing/search/stage/... -run "TestMergeStage" -v 2>&1 | head -60
```

Expected: compile error (`too many arguments in call to stage.NewMergeStage`) or `FAIL` for new tests — confirms tests are wired.

- [ ] **Step 3: Implement changes in merge.go**

Replace the contents of `internal/search/stage/merge.go` with the updated version:

**3a. Update imports** — add `"strings"`, `"time"`, `"iclude/pkg/scoring"` to imports block.

**3b. Add constants and maps** at top of file (after package/imports):

```go
const (
    defaultRRFK        = 60
    defaultAccessAlpha = 0.1
    minEffectiveStrength = 0.05
    weightCap          = 2.0
)

var kindWeights = map[string]float64{
    "skill":       1.5,
    "rule":        1.4,
    "mental_model": 1.3,
    "preference":  1.2,
    "goal":        1.2,
    "note":        1.0,
    "event":       0.9,
    "error":       0.8,
}

var subKindWeights = map[string]float64{
    "core_belief":    1.4,
    "working_memory": 0.7,
}

var classWeights = map[string]float64{
    "procedural": 1.5,
    "semantic":   1.2,
    "core":       1.4,
    "episodic":   1.0,
}

var scopePriorityBoost = map[string]float64{
    "session": 1.3,
    "project": 1.1,
    "user":    1.0,
    "agent":   1.0,
    "global":  0.9,
}
```

**3c. Update `MergeStage` struct and constructor:**

```go
type MergeStage struct {
    strategy    string
    k           int
    limit       int
    accessAlpha float64
}

func NewMergeStage(strategy string, k int, limit int, accessAlpha float64) *MergeStage {
    if strategy == "" {
        strategy = MergeStrategyRRF
    }
    if k <= 0 {
        k = defaultRRFK
    }
    if limit <= 0 {
        limit = 100
    }
    if accessAlpha <= 0 {
        accessAlpha = defaultAccessAlpha
    }
    return &MergeStage{strategy: strategy, k: k, limit: limit, accessAlpha: accessAlpha}
}
```

**3d. Add `computeStructuralWeight` method:**

```go
func (s *MergeStage) computeStructuralWeight(m *model.Memory) float64 {
    if m == nil {
        return 1.0
    }

    kw := kindWeights[m.Kind]
    if kw == 0 {
        kw = 1.0
    }
    if sw, ok := subKindWeights[m.SubKind]; ok {
        kw *= sw
    }
    if cw, ok := classWeights[m.MemoryClass]; ok {
        kw *= cw
    }

    // scope boost / 作用域提升
    boost := 1.0
    if m.Scope != "" {
        prefix := strings.SplitN(m.Scope, "/", 2)[0]
        if b, ok := scopePriorityBoost[prefix]; ok {
            boost = b
        }
    }
    // user/ + core class gets extra nudge / user作用域核心记忆额外提升
    if strings.HasPrefix(m.Scope, "user/") && m.MemoryClass == "core" {
        boost *= 1.15
    }

    effective := scoring.CalculateEffectiveStrength(
        m.Strength, m.DecayRate, m.LastAccessedAt,
        string(m.RetentionTier), m.AccessCount, s.accessAlpha,
    )
    if effective < minEffectiveStrength {
        effective = minEffectiveStrength
    }

    w := kw * boost * effective
    if w > weightCap {
        w = weightCap
    }
    return w
}
```

**3e. Filter expired at top of `Execute()`** — add this block immediately after the `multi-source path` comment check, before any scoring:

```go
// filter expired memories / 过滤过期记忆
now := time.Now()
var alive []*model.SearchResult
for _, r := range state.Candidates {
    if r.Memory != nil && r.Memory.ExpiresAt != nil && r.Memory.ExpiresAt.Before(now) {
        continue
    }
    alive = append(alive, r)
}
state.Candidates = alive
```

**3f. Update `mergeRRF`** — change the score accumulation line:

```go
// was: scores[id] += 1.0 / float64(s.k+rank+1)
scores[id] += s.computeStructuralWeight(r.Memory) / float64(s.k+rank+1)
```

**3g. Update `mergeGraphAware`** — change the score accumulation line:

```go
// was: scores[id] += trust * 1.0 / float64(s.k+rank+1)
scores[id] += trust * s.computeStructuralWeight(r.Memory) / float64(s.k+rank+1)
```

**3h. Update single-source path** — in the `Execute()` single-source branch, after dedup, apply structural weight and re-sort:

```go
// apply structural weight to single-source results / 单源时乘以结构权重
for _, r := range deduped {
    r.Score *= s.computeStructuralWeight(r.Memory)
}
sort.Slice(deduped, func(i, j int) bool {
    return deduped[i].Score > deduped[j].Score
})
```

- [ ] **Step 4: Run tests to confirm they pass**

```bash
go test -race ./testing/search/stage/... -run "TestMergeStage" -v 2>&1 | tail -30
```

Expected: all `TestMergeStage_*` tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/search/stage/merge.go testing/search/stage/merge_test.go
git commit -m "feat(search): integrate structural weights into RRF formula

Moves kind/class/scope/strength weighting from WeightStage post-pass
into the RRF denominator term. Expired filtering also moves to MergeStage.
Eliminates one normalization pass, preserving score discrimination."
```

---

## Task 2: Delete WeightStage / 删除 WeightStage

**Goal:** Remove the now-redundant WeightStage and its tests.

**Files:**
- Delete: `internal/search/stage/weight.go`
- Delete: `testing/search/stage/weight_test.go`

---

- [ ] **Step 1: Verify no remaining callers of WeightStage**

```bash
grep -r "NewWeightStage\|WeightStage" --include="*.go" . | grep -v "_test.go" | grep -v "^Binary"
```

Expected: only `internal/search/pipeline/builtin/builtin.go` still references `NewWeightStage` (will be removed in Task 3).

- [ ] **Step 2: Delete weight.go**

```bash
rm internal/search/stage/weight.go
```

- [ ] **Step 3: Delete weight_test.go**

```bash
rm testing/search/stage/weight_test.go
```

- [ ] **Step 4: Run full stage tests to confirm no regressions**

```bash
go test -race ./testing/search/stage/... -v 2>&1 | tail -40
```

Expected: all remaining stage tests PASS; no reference to `WeightStage` in output.

- [ ] **Step 5: Commit**

```bash
git add -u internal/search/stage/weight.go testing/search/stage/weight_test.go
git commit -m "refactor(search): delete WeightStage — logic merged into MergeStage"
```

---

## Task 3: Update builtin pipelines / 更新内置流水线

**Goal:** Pass `AccessAlpha` to `NewMergeStage`, remove `NewWeightStage` call, replace LLM reranker with overlap reranker.

**Files:**
- Modify: `internal/search/pipeline/builtin/builtin.go`

---

- [ ] **Step 1: Write a failing compilation check**

```bash
go build ./internal/search/pipeline/... 2>&1
```

Expected: compile error — `NewMergeStage` still called with 3 args; `NewWeightStage` referenced but deleted. Confirms the state before changes.

- [ ] **Step 2: Apply changes in builtin.go**

**2a. Update all four `NewMergeStage` calls** — add `deps.Cfg.AccessAlpha` as 4th arg:

In `buildPrecision()` (line ~55):
```go
stage.NewMergeStage(stage.MergeStrategyGraphAware, 60, 100, deps.Cfg.AccessAlpha),
```

In `buildExploration()` (line ~79):
```go
stage.NewMergeStage(stage.MergeStrategyRRF, 60, 100, deps.Cfg.AccessAlpha),
```

In `buildSemantic()` (line ~97):
```go
stage.NewMergeStage(stage.MergeStrategyRRF, 60, 100, deps.Cfg.AccessAlpha),
```

In `buildFull()` (line ~153):
```go
stage.NewMergeStage(stage.MergeStrategyGraphAware, 60, 100, deps.Cfg.AccessAlpha),
```

**2b. Remove `NewWeightStage` from `buildPostStages()`** (line ~164):

Find and delete the line:
```go
stage.NewWeightStage(deps.Cfg.AccessAlpha),
```

**2c. Replace LLM reranker with overlap reranker in `buildFull()`** (line ~155):

Replace:
```go
stage.NewRerankLLMStage(deps.LLM, 20, 0.7, 0.3, 0),
```
With:
```go
stage.NewOverlapRerankStage(20, 0.7),
```

- [ ] **Step 3: Verify compilation passes**

```bash
go build ./internal/search/pipeline/... 2>&1
```

Expected: no output (clean build).

- [ ] **Step 4: Run pipeline tests**

```bash
go build ./... 2>&1
go test -race ./testing/search/... -v 2>&1 | tail -40
```

Expected: clean build and all search tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/search/pipeline/builtin/builtin.go
git commit -m "refactor(search): update builtin pipelines

- Pass AccessAlpha to NewMergeStage (4th param)
- Remove WeightStage from buildPostStages
- Replace RerankLLMStage with OverlapRerankStage in buildFull"
```

---

## Task 4: Delete CascadeRetriever / 删除 CascadeRetriever

**Goal:** Remove the dual-path ambiguity. All traffic routes through the unified pipeline; `CascadeRetriever` is deleted entirely.

**Files:**
- Delete: `internal/search/cascade.go`
- Delete: `testing/search/cascade_test.go`
- Modify: `internal/search/retriever.go`
- Modify: `internal/bootstrap/wiring.go`

---

- [ ] **Step 1: Verify cascade usage scope**

```bash
grep -r "CascadeRetriever\|SetCascade\|NewCascadeRetriever" --include="*.go" . | grep -v "^Binary"
```

Expected: references in `cascade.go`, `retriever.go`, `wiring.go`, `cascade_test.go` only.

- [ ] **Step 2: Remove cascade from retriever.go**

In `internal/search/retriever.go`:

**2a. Remove `cascade` field** from the `Retriever` struct (line ~56):
```go
// DELETE this line:
cascade *CascadeRetriever
```

**2b. Remove `SetCascade` method** (lines ~78-81):
```go
// DELETE these lines:
func (r *Retriever) SetCascade(c *CascadeRetriever) {
    r.cascade = c
}
```

**2c. Remove cascade priority block** from `Retrieve()` (lines ~215-224):
```go
// DELETE this entire block:
// 降级链模式 / Cascade mode
if r.cascade != nil {
    results, err := r.cascade.Retrieve(ctx, req)
    if err != nil {
        return nil, err
    }
    // 实体发现 / Entity enrichment
    r.enrichWithEntities(ctx, results)
    return results, nil
}
```

- [ ] **Step 3: Remove cascade wiring from bootstrap**

In `internal/bootstrap/wiring.go`, find and delete two lines (lines ~380-381):
```go
// DELETE these lines:
cascade := search.NewCascadeRetriever(classifier, graphStage, ftsStage, vecStage, tempStage, nil, cfg.Retrieval.Cascade)
ret.SetCascade(cascade)
```

- [ ] **Step 4: Delete cascade source files**

```bash
rm internal/search/cascade.go
rm testing/search/cascade_test.go
```

- [ ] **Step 5: Verify full build**

```bash
go build ./... 2>&1
```

Expected: no output (clean build).

- [ ] **Step 6: Run tests**

```bash
go test -race ./testing/search/... -v 2>&1 | tail -40
```

Expected: all search tests PASS; no reference to `CascadeRetriever` in output.

- [ ] **Step 7: Commit**

```bash
git add -u internal/search/cascade.go testing/search/cascade_test.go
git add internal/search/retriever.go internal/bootstrap/wiring.go
git commit -m "refactor(search): delete CascadeRetriever

All retrieval now flows through the unified pipeline system.
Removes dual-path intent routing ambiguity."
```

---

## Task 5: Gate HyDE on intent + config / HyDE 意图门控与配置开关

**Goal:** HyDE only runs for semantic queries when explicitly enabled. Non-semantic queries skip the LLM call entirely.

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/search/preprocess.go`
- Modify: `testing/search/preprocess_test.go`

---

- [ ] **Step 1: Write failing HyDE gate tests**

Append to `testing/search/preprocess_test.go`:

```go
func TestPreprocess_HyDE_DisabledByDefault(t *testing.T) {
    ctrl := gomock.NewController(t)
    defer ctrl.Finish()

    mockLLM := mocks.NewMockProvider(ctrl)
    // LLM must NOT be called for HyDE when disabled
    mockLLM.EXPECT().Chat(gomock.Any(), gomock.Any()).Times(0)

    cfg := config.RetrievalConfig{
        Strategy: config.StrategyConfig{UseLLM: true},
        Preprocess: config.PreprocessConfig{HyDEEnabled: false},
    }

    p := search.NewQueryPreprocessor(cfg, mockLLM)
    plan, err := p.Preprocess(context.Background(), "what is the deployment process?")
    require.NoError(t, err)
    assert.Empty(t, plan.HyDEDoc, "HyDE doc should be empty when HyDEEnabled=false")
}

func TestPreprocess_HyDE_EnabledButNonSemantic(t *testing.T) {
    ctrl := gomock.NewController(t)
    defer ctrl.Finish()

    mockLLM := mocks.NewMockProvider(ctrl)
    // HyDE must NOT fire for entity/temporal intent even when enabled
    mockLLM.EXPECT().Chat(gomock.Any(), gomock.Any()).Times(0)

    cfg := config.RetrievalConfig{
        Strategy: config.StrategyConfig{UseLLM: true},
        Preprocess: config.PreprocessConfig{HyDEEnabled: true},
    }

    p := search.NewQueryPreprocessor(cfg, mockLLM)
    // "Alice yesterday" → temporal intent, not semantic
    plan, err := p.Preprocess(context.Background(), "Alice yesterday meeting")
    require.NoError(t, err)
    assert.Empty(t, plan.HyDEDoc, "HyDE doc should be empty for non-semantic intent")
}

func TestPreprocess_HyDE_EnabledAndSemantic(t *testing.T) {
    ctrl := gomock.NewController(t)
    defer ctrl.Finish()

    mockLLM := mocks.NewMockProvider(ctrl)
    mockLLM.EXPECT().Chat(gomock.Any(), gomock.Any()).Return(&llm.ChatResponse{
        Content: "This is a hypothetical document about deployment processes.",
    }, nil).AnyTimes()

    cfg := config.RetrievalConfig{
        Strategy: config.StrategyConfig{UseLLM: true},
        Preprocess: config.PreprocessConfig{HyDEEnabled: true},
    }

    p := search.NewQueryPreprocessor(cfg, mockLLM)
    plan, err := p.Preprocess(context.Background(), "explain the conceptual architecture of the system")
    require.NoError(t, err)
    assert.NotEmpty(t, plan.HyDEDoc, "HyDE doc should be populated for semantic intent when enabled")
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test -race ./testing/search/... -run "TestPreprocess_HyDE" -v 2>&1 | head -30
```

Expected: compile error (`PreprocessConfig has no field HyDEEnabled`) or FAIL — tests are wired.

- [ ] **Step 3: Add HyDEEnabled to config**

In `internal/config/config.go`, find the `PreprocessConfig` struct and add the new field:

```go
type PreprocessConfig struct {
    // ... existing fields ...
    HyDEEnabled bool `mapstructure:"hyde_enabled"`
}
```

Add viper default (in the `setDefaults()` function):
```go
viper.SetDefault("retrieval.preprocess.hyde_enabled", false)
```

- [ ] **Step 4: Extract generateHyDE and add intent gate in preprocess.go**

In `internal/search/preprocess.go`:

**4a. Extract the HyDE block** (currently lines ~379-395) into a new method:

```go
// generateHyDE generates a hypothetical document for semantic queries.
// HyDE: 语义查询生成假设性回答文档 / only called for semantic intent
func (p *QueryPreprocessor) generateHyDE(ctx context.Context, plan *pipeline.QueryPlan) {
    timeout := 5 * time.Second
    temp := float32(0.7)
    hydeCtx, hydeCancel := context.WithTimeout(ctx, timeout)
    defer hydeCancel()
    hydeResp, hydeErr := p.llm.Chat(hydeCtx, &llm.ChatRequest{
        Messages: []llm.ChatMessage{
            {Role: "system", Content: "你是一个记忆系统。根据用户的问题，写出一段可能存在于记忆库中的文档片段（50-100字）。直接输出内容，不加前缀。用中文回答。"},
            {Role: "user", Content: plan.OriginalQuery},
        },
        Temperature: &temp,
    })
    if hydeErr != nil {
        logger.Debug("preprocess: HyDE generation failed", zap.Error(hydeErr))
        return
    }
    if hydeResp.Content != "" {
        plan.HyDEDoc = strings.TrimSpace(hydeResp.Content)
    }
}
```

**4b. Replace the original inline HyDE block** in `llmEnhance()` with a gated call:

```go
// gate HyDE: only semantic intent + explicitly enabled / 仅语义意图且配置开启时调用
if intent == IntentSemantic && p.cfg.Preprocess.HyDEEnabled {
    p.generateHyDE(ctx, plan)
}
```

- [ ] **Step 5: Run HyDE tests**

```bash
go test -race ./testing/search/... -run "TestPreprocess_HyDE" -v 2>&1 | tail -20
```

Expected: all three `TestPreprocess_HyDE_*` tests PASS.

- [ ] **Step 6: Run full test suite**

```bash
go build ./... 2>&1
go test -race ./testing/... -v 2>&1 | tail -50
```

Expected: clean build and all tests PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go internal/search/preprocess.go testing/search/preprocess_test.go
git commit -m "feat(search): gate HyDE on intent type and config flag

HyDE now only runs when PreprocessConfig.HyDEEnabled=true AND intent
is semantic. Non-semantic queries skip the LLM call entirely.
Adds hyde_enabled config field (default false)."
```

---

## Task 6: Verify use_llm default and add clarifying comment / 确认 use_llm 默认值

**Goal:** Confirm `retrieval.strategy.use_llm` defaults to `false` so LLM-enhanced preprocessing is opt-in. Add a comment explaining why `InitPipeline()` gates the LLM.

**Files:**
- Modify: `internal/config/config.go` (if default missing)
- Modify: `internal/search/retriever.go` (comment only)

---

- [ ] **Step 1: Verify the viper default**

```bash
grep -n "use_llm\|UseLLM" internal/config/config.go
```

Expected: a line like `viper.SetDefault("retrieval.strategy.use_llm", false)`.

If that line is missing, add it in `setDefaults()`:
```go
viper.SetDefault("retrieval.strategy.use_llm", false)
```

- [ ] **Step 2: Add clarifying comment in retriever.go**

In `internal/search/retriever.go`, around the `InitPipeline()` LLM gate (lines ~112-115), add a single-line comment:

```go
// LLM is only injected into the strategy when use_llm=true; nil LLM falls back to RuleClassifier.
var strategyLLM llm.Provider
if r.cfg.Strategy.UseLLM {
    strategyLLM = r.llm
}
```

- [ ] **Step 3: Build and run all tests**

```bash
go build ./... 2>&1
go test -race ./testing/... 2>&1 | tail -20
```

Expected: clean build and all tests PASS.

- [ ] **Step 4: Commit**

```bash
git add internal/config/config.go internal/search/retriever.go
git commit -m "docs(search): confirm use_llm default false, add LLM gate comment"
```

---

## Final Verification

- [ ] **Run the full test suite with race detector**

```bash
go test -race ./testing/... -count=1 2>&1 | tail -30
```

Expected: all tests PASS, no data races.

- [ ] **Verify deleted files are gone**

```bash
ls internal/search/stage/weight.go internal/search/cascade.go testing/search/cascade_test.go testing/search/stage/weight_test.go 2>&1
```

Expected: `No such file or directory` for all four.

- [ ] **Verify no orphan WeightStage references**

```bash
grep -r "WeightStage\|NewWeightStage\|CascadeRetriever\|SetCascade" --include="*.go" . | grep -v "^Binary"
```

Expected: no output.

- [ ] **Final commit summary tag**

```bash
git log --oneline -6
```

Expected to see 5-6 commits from this plan plus the cleanup verification.
