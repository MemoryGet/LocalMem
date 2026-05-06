# Plan G Milestones: General Text Engine + Entity Decision Layer

> **Planning only:** This is a milestone expansion of `Plan G`. It is intended for phased review, sequencing, and delivery governance only.

**Base plan:** `2026-04-12-plan-g-general-text-engine-balanced.md`

**Purpose:** Break the balanced architecture into reviewable milestones with explicit scope, outputs, dependencies, risks, and acceptance criteria.

---

## Delivery Strategy

This plan should be delivered as a staged refactor, not a single cutover.

### Principles
- Keep current retrieval and storage behavior stable wherever possible.
- Introduce the new architecture under feature flags first.
- Make every stage observable before moving to the next.
- Avoid simultaneous schema, runtime, and pipeline rewrites in one release.

---

## Milestone 0: Architecture Freeze

**Objective**
- Lock down vocabulary, boundaries, and approval scope before code changes.

**Scope**
- Approve the meaning of:
  - `text engine`
  - `mention`
  - `link result`
  - `decision`
  - `strong candidate`
  - `weak candidate`
  - `relation event`

**Outputs**
- Approved architecture vocabulary
- Approved data flow diagram
- Approved acceptance metrics

**Dependencies**
- Review of `Plan G`

**Risks**
- Ambiguous terms will cause later design churn

**Acceptance**
- Shared written agreement on terminology and desired end state

---

## Milestone 1: General Text Engine Foundation

**Objective**
- Introduce the standalone text engine without changing graph behavior yet.

**Scope**
- Add `textengine` module
- Add structured `AnalyzeRequest` / `AnalyzeResult`
- Support multiple analysis modes:
  - `fts`
  - `entity`
  - `query`
- Add compatibility adapter for old tokenizer string contract

**Non-goals**
- No new entity decision logic yet
- No schema changes yet
- No graph behavior changes yet

**Outputs**
- New engine abstraction
- Adapter so existing FTS still works
- Test coverage for mode outputs

**Dependencies**
- Milestone 0

**Risks**
- Structured output may not preserve current FTS recall if adapter behavior is wrong

**Acceptance**
- Existing FTS-oriented tests continue to pass through adapter path
- Engine can switch behavior by mode while reusing same backend segmenter

---

## Milestone 2: Mention Extraction Layer

**Objective**
- Add a distinct mention layer so segmentation output is no longer consumed directly by the resolver.

**Scope**
- Add mention data structure
- Add mention extractor based on:
  - phrases
  - naming rules
  - domain dictionaries
  - token features
- Emit candidate mentions from `Analyze(..., mode=entity)`

**Non-goals**
- No linker yet
- No candidate stratification yet

**Outputs**
- Mention extractor package
- Mention extraction metrics
- Debug output showing tokens -> mentions

**Dependencies**
- Milestone 1

**Risks**
- Mention extractor may overgenerate if phrase merge and token feature rules are weak

**Acceptance**
- Mentions are materially fewer than raw tokens
- Mention output is explainable and inspectable

---

## Milestone 3: Entity Linking Layer

**Objective**
- Add linking against existing entities before any candidate creation.

**Scope**
- Add entity linker
- Support:
  - exact name match
  - normalized match
  - alias match
  - scope-aware filtering

**Non-goals**
- No fuzzy/vector linking yet unless clearly needed
- No graph decision scoring yet

**Outputs**
- Link result model
- Link metrics:
  - exact hit rate
  - alias hit rate
  - unresolved rate

**Dependencies**
- Milestone 2

**Risks**
- Poor alias data may reduce linker usefulness

**Acceptance**
- Existing-entity matches happen through linker, not ad hoc resolver calls
- Link attempts and reasons are logged/debuggable

---

## Milestone 4: Entity Decision Layer

**Objective**
- Introduce a formal decision stage controlling graph admission.

**Scope**
- Add decision scoring
- Classify each mention into:
  - `link`
  - `strong_candidate`
  - `weak_candidate`
  - `reject`
- Add configurable thresholds
- Add per-memory budgets:
  - max linked entities
  - max new strong candidates

**Outputs**
- Entity decision module
- Decision logs with reason codes
- Policy config

**Dependencies**
- Milestone 3

**Risks**
- Threshold tuning may require several iterations
- Overly aggressive gating may hurt recall

**Acceptance**
- No raw mention reaches graph writer without decision output
- Candidate volume drops materially versus current A-mode baseline

---

## Milestone 5: Candidate Stratification and Persistence

**Objective**
- Separate strong and weak evidence in persistence.

**Scope**
- Extend candidate persistence model
- Either:
  - split strong/weak tables, or
  - add `candidate_tier`
- Track:
  - hit count
  - distinct memory count
  - source diversity
  - last seen

**Outputs**
- Tiered candidate storage
- Aggregation fields required for later promotion

**Dependencies**
- Milestone 4

**Risks**
- Candidate store growth may still be large if retention rules are missing

**Acceptance**
- Weak candidates are persisted separately from formal graph entities
- Strong candidates can be queried independently for promotion

---

## Milestone 6: Relation Events Instead of Direct Formal Relations

**Objective**
- Stop writing stable graph relations directly from first co-occurrence.

**Scope**
- Add `relation_events`
- Online pipeline writes association + relation event only
- Formal `entity_relations` remain a promoted layer

**Outputs**
- Relation event writer
- Pair aggregation logic specification

**Dependencies**
- Milestone 4

**Risks**
- Temporary perception that graph relations are “missing” until promotion runs

**Acceptance**
- First co-occurrence does not immediately create a stable formal relation
- Event volume is measurable

---

## Milestone 7: Offline Aggregation and Promotion

**Objective**
- Aggregate candidates and relation events into formal graph structures.

**Scope**
- Candidate promotion job
- Relation promotion job
- Configurable thresholds for:
  - candidate promotion
  - relation promotion

**Outputs**
- Offline promotion jobs
- Promotion metrics
- Before/after summaries for candidate/entity/relation volumes

**Dependencies**
- Milestone 5
- Milestone 6

**Risks**
- Promotion criteria may be too weak or too strict initially

**Acceptance**
- Candidate promotion becomes evidence-driven
- Stable relation creation becomes event-threshold-driven

---

## Milestone 8: Resolver Cutover

**Objective**
- Replace current Layer 1 resolver behavior with the new orchestrated pipeline.

**Scope**
- Refactor `EntityResolver` into coordinator over:
  - text engine
  - mention extractor
  - linker
  - decider
  - graph writer
- Feature flag controlled rollout

**Outputs**
- New resolver path
- Legacy fallback flag

**Dependencies**
- Milestones 1 through 7

**Risks**
- Hidden assumptions in current resolver may surface late

**Acceptance**
- New path can be enabled and disabled safely
- Legacy fallback remains available during rollout

---

## Milestone 9: Evaluation and Tuning

**Objective**
- Validate the architecture against current failure modes and quality targets.

**Scope**
- Compare old A-mode vs new path on:
  - promoted entity count
  - candidate count
  - relation count
  - resolver latency
  - query hit rate
- Tune thresholds and budgets

**Outputs**
- Evaluation report
- Threshold recommendations
- Release-readiness assessment

**Dependencies**
- Milestone 8

**Risks**
- Recall/precision tradeoff may require multiple tuning rounds

**Acceptance**
- Candidate and relation explosion are materially reduced
- Query quality is stable or improved

---

## Suggested Timeline Shape

### Stage A: Foundation
- Milestone 0
- Milestone 1
- Milestone 2

### Stage B: Decision Control
- Milestone 3
- Milestone 4
- Milestone 5

### Stage C: Graph Discipline
- Milestone 6
- Milestone 7

### Stage D: Cutover and Validation
- Milestone 8
- Milestone 9

---

## Review Gates

### Gate 1
After Milestone 1:
- Is the text engine abstraction acceptable?
- Does compatibility preserve current search behavior?

### Gate 2
After Milestone 4:
- Is the entity decision model understandable and tunable?
- Are budgets and thresholds acceptable?

### Gate 3
After Milestone 7:
- Are promotion rules strong enough to avoid graph pollution?
- Is the relation event model sufficient?

### Gate 4
After Milestone 9:
- Is the new resolver path ready for default enablement?

---

## Exit Criteria for Plan G

Plan G should be considered successfully delivered when:

1. Segmentation output no longer directly creates graph entities.
2. Mentions, links, and decisions are explicit and observable.
3. Candidate growth is bounded and tiered.
4. Stable relations are promoted from evidence rather than immediate co-occurrence.
5. The new pipeline improves graph discipline without causing unacceptable recall loss.

---

## Recommended Review Position

Use this milestone document if the team wants:
- phased approval,
- explicit review gates,
- and a low-risk path from current resolver behavior to the balanced architecture.

