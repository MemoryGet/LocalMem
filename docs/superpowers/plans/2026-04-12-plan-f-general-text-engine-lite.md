# Plan F: General Text Engine Lite

> **Planning only:** This document is for architecture review and sequencing only. Do not implement directly from this file without follow-up approval.

**Goal:** Replace the current tokenizer-centric design with a standalone general text engine that serves FTS, entity candidate generation, and query preprocessing, while minimizing code churn and immediately reducing candidate/entity explosion in A mode.

**Architecture:** Introduce a new `textengine` module as the single entry point for text normalization, segmentation, phrase merge, stopword filtering, and entity-like scoring. Keep existing FTS behavior compatible through an adapter layer. Layer 1 entity resolution consumes structured tokens instead of raw space-separated strings. Candidate intake is gated, per-memory budgets are enforced, and relation writes are delayed through event aggregation.

**Tech Stack:** Go 1.25+, SQLite, existing tokenizer backends (`simple`, `jieba`, `gse`)

**Motivation:**
- Current `Tokenizer` only returns a space-separated string, which is too weak for enterprise text analysis.
- Tokenizer creation is coupled to store initialization, so “segmentation” is treated as a SQLite detail instead of a shared text-processing capability.
- A-mode entity resolution currently promotes too many low-value tokens into candidates/entities, causing graph and relation explosion.

---

## Target Outcomes

1. A standalone text processing module exists outside storage concerns.
2. Existing FTS indexing still works without schema rewrite.
3. Entity candidate volume drops materially through stronger intake control.
4. Per-memory entity count and candidate count are bounded.
5. The system supports multiple analysis modes instead of one global tokenizer behavior.

---

## Scope

**In scope**
- New `textengine` abstraction and compatibility adapter
- Structured token output
- `fts`, `entity`, `query` analysis modes
- Candidate gate and per-memory budget
- Delayed relation creation through event accumulation

**Out of scope**
- New ML/NER service
- Master data integration
- Human review workflows
- Large schema redesign beyond minimum support tables

---

## Proposed Module Layout

```text
internal/textengine/
  engine.go              # public interfaces
  pipeline.go            # orchestration
  request.go             # AnalyzeRequest / AnalyzeResult
  normalize/
  segment/
    simple.go
    jieba.go
    gse.go
  phrase/
  filter/
  scoring/
  adapter/
    tokenizer_compat.go  # old Tokenizer interface bridge

internal/entitygate/
  gate.go                # candidate admission rules
  rules.go               # entity-like / noise rules

internal/relationevent/
  aggregator.go          # event->relation promotion logic
```

---

## Core Interfaces

```go
type Engine interface {
    Analyze(ctx context.Context, req AnalyzeRequest) (*AnalyzeResult, error)
    Name() string
}

type AnalyzeRequest struct {
    Text   string
    Lang   string
    Domain string
    Mode   string // fts | entity | query
}

type AnalyzeResult struct {
    Tokens  []Token
    Phrases []Phrase
    Meta    map[string]any
}

type Token struct {
    Text         string
    Normalized   string
    Weight       float64
    Pos          string
    IsStopword   bool
    IsNumeric    bool
    IsSymbol     bool
    IsEntityLike bool
}
```

---

## Mode Semantics

### `fts`
- High recall
- Keep more tokens
- Phrase merge enabled
- Stopword filtering weaker
- Output is adaptable to existing FTS string format

### `entity`
- Precision-biased
- Strong stopword and noise filtering
- Prefer entity-like phrases over raw single tokens
- Hard limit on output token count

### `query`
- Keep intent-carrying tokens and phrases
- Drop weak fillers
- Bias toward stable normalized forms

---

## Candidate Intake Strategy

### Gate Rules
- Drop tokens with rune length `< 2`
- Drop pure numeric and pure symbol tokens
- Drop configured high-frequency generic words
- Drop low-value long fragments
- Prefer merged phrases over fragmented components
- Only admit tokens/phrases marked `IsEntityLike` or above weight threshold

### Budgets
- Max confirmed entity associations per memory: `3-5`
- Max new strong candidates per memory: `2-3`
- Remaining low-confidence items are discarded

### Expected Impact
- Large reduction in weak candidates
- Lower graph expansion pressure
- Lower relation explosion from noisy co-occurrence

---

## Relation Strategy

Replace direct “co-occur once => write relation” with:

1. Write `memory_entities`
2. Emit relation event for entity pairs
3. Aggregate events asynchronously
4. Promote to `entity_relations` only after threshold

Suggested thresholds:
- At least `3` co-occurrence events, or
- At least `2` events across distinct memories and time slices

---

## Data Model Changes

### New or revised tables
- `relation_events`
  - `memory_id`
  - `source_entity_id`
  - `target_entity_id`
  - `event_time`
  - `scope`

### Existing tables reused
- `entity_candidates`
- `entities`
- `memory_entities`
- `entity_relations`

No major schema rewrite is required in this plan.

---

## Integration Points

### Replace first
- `internal/memory/entity_resolver.go`
  - Layer 1 should consume `Analyze(..., mode=entity)`

### Adapt, do not rewrite immediately
- SQLite FTS write path
  - Use compatibility adapter to convert structured output into token string

### Later consumers
- Query preprocessor
- Search-time keyword extraction

---

## Migration Plan

### Phase 1
- Add `textengine` module
- Add compatibility adapter for old `Tokenizer`
- Keep current runtime behavior stable

### Phase 2
- Switch Layer 1 resolver to structured entity-mode output
- Add candidate gate and per-memory budgets

### Phase 3
- Introduce `relation_events`
- Delay relation promotion

### Phase 4
- Add instrumentation and compare against current A-mode behavior

---

## Metrics to Track

- Tokens emitted per memory
- Strong candidates admitted per memory
- Candidate promotion rate
- Total entities created
- Total relation events
- Total stable relations
- Resolver latency p50/p95
- Query hit rate deltas for A-mode

---

## Risks

1. FTS recall may drop if filtering is too aggressive.
2. Compatibility adapter may hide overly broad old assumptions.
3. Phrase merge rules may need domain tuning to avoid under-segmentation.
4. Hard budgets may suppress legitimate long-tail entities.

---

## Acceptance Criteria

1. Existing FTS tests still run through the compatibility adapter.
2. A-mode produces materially fewer promoted entities than current baseline.
3. Layer 1 runtime remains bounded under large seed sets.
4. Relation count grows slower than entity count.
5. The system can switch analysis behavior by mode without changing tokenizer provider.

---

## Recommended Decision

Approve this plan if the immediate priority is:
- stop graph explosion,
- keep the current architecture recognizable,
- and create a safe foundation for later enterprise upgrades.

