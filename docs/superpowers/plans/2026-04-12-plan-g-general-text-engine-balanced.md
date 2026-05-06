# Plan G: General Text Engine + Entity Decision Layer

> **Planning only:** This document is for review only. No code changes should be made from this plan until approved.

**Goal:** Upgrade the system from “segmentation-driven candidate creation” to a balanced enterprise architecture where text analysis, entity recognition, entity linking, and graph admission are separate concerns.

**Architecture:** Add a standalone general text engine, then layer an entity decision pipeline on top: mention extraction, entity linking, decision scoring, candidate stratification, and delayed relation materialization. Segmentation is demoted to a recall mechanism. Formal graph admission is controlled by a decision layer.

**Tech Stack:** Go 1.25+, SQLite, optional Qdrant, existing tokenizer backends, optional lightweight NER/classification models later

**Why this plan exists:** The lightweight plan fixes immediate explosion, but it still relies mostly on rules. This balanced plan introduces the enterprise pattern most suitable for the current repository: a general text engine plus an explicit entity decision layer.

---

## Target Outcomes

1. Segmentation no longer directly creates graph entities.
2. Entity mentions are separated from formal entities.
3. Existing entities, strong candidates, weak candidates, and rejects become distinct outcomes.
4. Formal relation creation becomes evidence-driven instead of immediate co-occurrence.
5. The pipeline becomes measurable and explainable at each decision step.

---

## High-Level Pipeline

```text
Text Input
  -> TextEngine
  -> Mention Extractor
  -> Entity Linker
  -> Entity Decider
      -> Linked Entity
      -> Strong Candidate
      -> Weak Candidate
      -> Reject
  -> MemoryEntity Write
  -> Relation Event Write
  -> Offline Aggregation
      -> Entity Promotion
      -> Stable Relation Promotion
```

---

## Proposed Module Layout

```text
internal/textengine/
internal/entitymention/
  extractor.go
  features.go

internal/entitylink/
  linker.go
  alias_match.go
  exact_match.go
  fuzzy_match.go

internal/entitydecision/
  decider.go
  scoring.go
  policy.go

internal/graphpipeline/
  writer.go
  relation_events.go

internal/offline/
  candidate_aggregation.go
  relation_promotion.go
```

---

## Processing Responsibilities

### Text Engine
- normalization
- segmentation
- phrase merge
- stopword and noise annotation
- domain-aware token weighting

### Mention Extractor
- identify entity mentions from tokens and phrases
- produce candidate mentions, not final entities
- support mixed strategies:
  - dictionary hits
  - naming rules
  - phrase heuristics
  - optional future model hints

### Entity Linker
- exact name match
- alias match
- normalized match
- scoped lookup
- later optional fuzzy/vector support

### Entity Decider
For each mention, decide:
- link to existing entity
- create strong candidate
- create weak candidate
- reject

---

## Core Data Structures

```go
type Mention struct {
    Text        string
    Normalized  string
    SpanStart   int
    SpanEnd     int
    MentionType string
    Weight      float64
    Features    map[string]any
}

type LinkResult struct {
    EntityID   string
    MatchType  string
    Score      float64
    AliasUsed  string
}

type Decision struct {
    Action      string // link | strong_candidate | weak_candidate | reject
    Score       float64
    ReasonCodes []string
}
```

---

## Candidate Stratification

### Strong Candidate
Use when:
- mention is entity-like
- appears across multiple memories or sources
- or co-occurs with trusted entities

### Weak Candidate
Use when:
- mention is plausible but unstable
- insufficient evidence for graph admission
- worth counting, not worth promoting yet

### Reject
Use when:
- generic term
- unstable fragment
- obvious noise
- low utility for graph construction

---

## Decision Scoring Model

Suggested scoring dimensions:
- lexical shape score
- phrase stability score
- stopword/generic penalty
- dictionary hit score
- alias hit score
- scoped exact match score
- cross-memory repetition score
- source diversity score
- co-occurrence with trusted entities
- recency and persistence score

The final score determines action thresholds.

---

## Data Model Plan

### New tables
- `entity_mentions`
  - raw extracted mentions
- `entity_aliases`
  - normalized aliases and abbreviations
- `relation_events`
  - evidence records before formal relation promotion
- `entity_decision_logs`
  - explainability and debugging trail

### Existing tables reused
- `entity_candidates`
- `entities`
- `memory_entities`
- `entity_relations`

### Optional extension
- split candidate table into `entity_candidates_strong` and `entity_candidates_weak`
  - or keep a single table with a `candidate_tier` column

---

## Relation Strategy

Formal relations should be promoted from evidence, not written immediately.

### Online
- write mention/entity associations
- emit relation events for entity pairs

### Offline
- aggregate pair statistics
- enforce distinct-memory threshold
- enforce time/window threshold
- promote to typed relation or `related_to`

### Benefits
- lower write amplification
- reduced noisy relation growth
- better explainability

---

## Integration with Current Repository

### Direct replacements
- `internal/memory/entity_resolver.go`
  - refactor into orchestrator over text engine + mention extractor + linker + decider

### New dependency injection path
- `internal/bootstrap/wiring.go`
  - instantiate `textengine`, `entitymention`, `entitylink`, `entitydecision`

### Keep compatible first
- FTS write path still uses text-engine adapter output
- no need to rewrite SQLite store first

---

## Configuration Design

```yaml
text_engine:
  default_lang: zh
  segmenter:
    provider: jieba
    jieba_url: http://localhost:8866
  modes:
    fts:
      phrase_merge: true
    entity:
      stopword_filter: true
      entity_like_only: true
      max_output_tokens: 8
    query:
      stopword_filter: true

entity_decision:
  strong_threshold: 0.78
  weak_threshold: 0.52
  per_memory_entity_budget: 5
  per_memory_new_candidate_budget: 3
  relation_event_enabled: true
```

---

## Migration Phases

### Phase 1: Foundation
- add `textengine`
- structured outputs
- adapter for old tokenizer contract

### Phase 2: Mention Layer
- add mention extractor
- keep current resolver behavior behind feature flag

### Phase 3: Linking + Decision
- add linker and decider
- stratify outputs into linked/strong/weak/reject

### Phase 4: Relation Events
- replace direct relation writes with event accumulation

### Phase 5: Evaluation
- compare A-mode before/after:
  - candidate volume
  - promoted entity volume
  - relation volume
  - query quality

---

## Observability Requirements

For every processed memory, the system should be able to expose:
- raw text
- analyzed tokens and phrases
- extracted mentions
- link attempts
- decision score and reason codes
- final written entities
- emitted relation events

This is mandatory for enterprise debugging and auditability.

---

## Risks

1. Increased pipeline complexity may slow initial delivery.
2. Scoring thresholds may require repeated tuning.
3. Without dictionary/alias quality, linker precision may remain weak.
4. Mention logs can grow quickly without retention policy.

---

## Acceptance Criteria

1. No raw segmentation output directly becomes a formal entity without decision-layer approval.
2. Weak candidates no longer pollute the main graph.
3. Relation growth becomes event-driven and thresholded.
4. Resolver output becomes explainable via decision logs.
5. Search quality remains stable or improves while entity volume drops materially.

---

## Recommended Decision

Approve this plan if the desired end state is:
- enterprise-usable graph quality,
- explicit control over entity admission,
- and a system that can evolve beyond rules without replacing the whole repository.

