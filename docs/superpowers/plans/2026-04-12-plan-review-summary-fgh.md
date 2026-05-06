# Plan Review Summary: F vs G vs H

> **Review draft:** This document compresses the three planning documents into one comparison-oriented review memo for decision making. No implementation instructions are included here.

**Related plans**
- `Plan F` — General Text Engine Lite
- `Plan G` — General Text Engine + Entity Decision Layer
- `Plan H` — Enterprise Text Intelligence Platform

---

## Executive Summary

The three plans are not competing end states of equal scope. They represent three maturity levels:

- `F` is the fastest stabilization plan.
- `G` is the recommended product-quality architecture.
- `H` is the long-term enterprise target state.

If the current problem is graph explosion, noisy entities, and unstable A-mode performance, `F` is the fastest way to stop damage. If the goal is to make the system enterprise-usable without overbuilding governance too early, `G` is the best primary track. If the system is intended to become a shared enterprise knowledge substrate across multiple authority systems, `H` is the strategic destination.

---

## Decision Framing

### Current observed pain
- Segmentation output is too weak and too coarse as a system boundary.
- Layer 1 creates too many low-value candidates.
- Candidate promotion leads to excessive formal entities.
- Co-occurrence causes relation growth to explode.
- “Segmentation” is coupled to storage concerns instead of being a standalone capability.

### What the architecture must fix
1. Separate text analysis from graph admission.
2. Bound candidate intake and per-memory graph growth.
3. Preserve retrieval usefulness while reducing graph pollution.
4. Make graph construction explainable and measurable.

---

## One-Line Positioning

| Plan | One-line positioning |
|------|----------------------|
| `F` | Make segmentation a reusable text engine and add hard intake controls. |
| `G` | Add a proper entity decision layer so segmentation no longer directly creates graph entities. |
| `H` | Build a full enterprise text intelligence platform with master-data alignment, offline aggregation, and governance. |

---

## Comparative View

| Dimension | Plan F | Plan G | Plan H |
|------|------|------|------|
| Primary goal | Fast stabilization | Enterprise-usable graph quality | Enterprise operating model |
| Change size | Low | Medium | High |
| Time to first value | Fast | Medium | Slow |
| Risk to current codebase | Low | Medium | High |
| Candidate explosion control | Good | Very good | Excellent |
| Entity quality | Medium | High | Very high |
| Relation quality | Medium | High | Very high |
| Governance capability | Low | Medium | High |
| Master data alignment | No | Optional later | Core principle |
| Best fit now | Strong | Strongest recommended | Too early unless strategic program exists |

---

## Plan-by-Plan Review

### Plan F

**What it changes**
- Introduces a standalone `textengine`
- Keeps FTS working via compatibility adapter
- Adds candidate gate rules
- Adds per-memory budgets
- Delays formal relation creation through relation events

**What it fixes well**
- Immediate graph explosion
- Over-admission of low-value segmentation output
- Tight coupling between tokenizer and store initialization

**What it does not solve fully**
- It still relies heavily on rules
- It does not formally separate mention extraction from entity decision
- It does not create a long-term governance model

**Review verdict**
- Best immediate mitigation plan
- Strong choice if the next priority is operational stability

---

### Plan G

**What it changes**
- Keeps the standalone text engine
- Adds mention extraction
- Adds entity linker
- Adds entity decision layer
- Splits outputs into linked / strong candidate / weak candidate / reject
- Makes relations evidence-driven

**What it fixes well**
- Segmentation no longer acts as direct entity creation
- Candidate quality becomes controllable
- Formal graph admission becomes explicit and explainable
- The pipeline becomes tunable with metrics and policies

**What it does not solve fully**
- Does not yet provide full authority-system integration
- Does not yet define governance UI and operational workflow in full

**Review verdict**
- Best balance of practicality and architectural correctness
- Recommended primary roadmap for this repository

---

### Plan H

**What it changes**
- Treats master data as entity backbone
- Splits online and offline pipelines clearly
- Adds governance and review workflows
- Adds auditable evidence-backed graph production

**What it fixes well**
- Long-term enterprise quality, governance, and scale
- Canonical entity alignment
- Operational safety for shared enterprise usage

**What it costs**
- Highest complexity
- Requires broader product and operational support
- Not suitable as the first implementation milestone unless a large program is already approved

**Review verdict**
- Best strategic target state
- Not recommended as immediate first implementation

---

## Recommended Decision

### Recommended path
1. Use `Plan F` ideas to stop immediate graph explosion.
2. Treat `Plan G` as the main approved architecture track.
3. Keep `Plan H` as the north-star document for future enterprise expansion.

### Practical interpretation
- If only one plan is approved for near-term execution, approve `Plan G`.
- If the team wants a two-step route, do `F -> G`.
- Do not start directly from `H` unless master-data integration and governance are already funded.

---

## Review Questions

### Questions for approving Plan F
- Is immediate stabilization the top priority?
- Is the team willing to accept rule-heavy first-stage logic?
- Is backward compatibility with current FTS behavior mandatory?

### Questions for approving Plan G
- Is the team ready to add mention/link/decision concepts to the codebase?
- Is explainability of graph admission now a requirement?
- Is medium-complexity refactor acceptable to avoid repeated future rewrites?

### Questions for approving Plan H
- Will this system serve multiple enterprise systems as a shared substrate?
- Are authority data sources available and reliable?
- Is there appetite for review tooling and operational governance?

---

## Approval Guidance

### Approve Plan F if
- the immediate concern is performance and graph growth control,
- and the team wants the smallest safe move.

### Approve Plan G if
- the team wants the most reasonable medium-term architecture,
- and wants to fix the core conceptual issue: segmentation is not entity admission.

### Approve Plan H if
- this system is explicitly being funded as enterprise infrastructure,
- with master-data integration and review operations included in scope.

---

## Final Recommendation

For this repository and the problems already observed, the preferred evaluation outcome should be:

- `Plan G` as the main architecture approval
- `Plan F` as fallback or first stabilization slice
- `Plan H` as long-term target state

This yields the best balance between delivery risk, graph quality, and architectural integrity.

