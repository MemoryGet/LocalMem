# Plan H: Enterprise Text Intelligence Platform

> **Planning only:** This is a target-state planning document. It is not intended for immediate implementation without staged approval.

**Goal:** Define a full enterprise-grade architecture where text analysis, entity linking, master-data alignment, graph production, and review governance are separate but coordinated systems.

**Architecture:** Online processing performs lightweight text analysis and high-confidence linking only. Offline pipelines perform candidate aggregation, alias resolution, relation consolidation, and graph promotion. Master data is treated as the entity backbone. Text does not “invent” most formal entities; it mainly links to authority sources and surfaces new candidate discoveries for controlled promotion.

**Tech Stack:** Go 1.25+, SQLite/Qdrant, offline jobs, optional event bus, optional admin/review UI, authority system integrations (Org/CMDB/Project/Git/CRM)

**Why this plan exists:** For enterprise deployment, segmentation quality alone is not enough. The system needs authority alignment, staged graph admission, auditability, and operational governance.

---

## Target Outcomes

1. Formal graph entities are backed by authority sources wherever possible.
2. Text contributes evidence and discovery, not uncontrolled graph pollution.
3. Online write path stays fast and bounded.
4. Offline pipelines absorb heavy aggregation and graph construction.
5. Human review is available for high-value corrections and promotions.

---

## Reference Architecture

```text
                 +----------------------+
                 |  Master Data Hub     |
                 |  Org / CMDB / Git    |
                 |  CRM / Projects      |
                 +----------+-----------+
                            |
                            v
Text Input -> Online Text Intelligence Layer -> High-Confidence Linking -> MemoryEntity
                            |
                            v
                    Candidate Discovery Queue
                            |
                            v
                 Offline Aggregation & Promotion
                            |
                            v
                Graph Build / Relation Consolidation
                            |
                            v
                 Review Console / Audit / Corrections
                            |
                            v
                    Graph Serving & Retrieval
```

---

## System Layers

### 1. Master Data Hub
Authority-backed entities from:
- org structure
- project registry
- CMDB/service inventory
- repository registry
- customer/product systems

This layer defines canonical entities and aliases.

### 2. Online Text Intelligence Layer
Responsibilities:
- normalization
- segmentation and phrase merge
- mention extraction
- high-confidence entity linking
- write only bounded, safe associations

The online path must remain low-latency.

### 3. Candidate Discovery Layer
Responsibilities:
- capture unresolved mentions
- aggregate weak evidence
- stratify by source quality and stability

### 4. Offline Aggregation Layer
Responsibilities:
- mention clustering
- alias consolidation
- candidate scoring
- candidate promotion recommendation
- relation event aggregation

### 5. Governance Layer
Responsibilities:
- human review tasks
- entity merge/split
- alias correction
- blacklist/whitelist policy
- audit logs

### 6. Serving Layer
Responsibilities:
- graph-backed retrieval
- profile views
- explanation/evidence tracing
- search enrichment

---

## Entity Classes

### Canonical Entity
- sourced from master data
- strongest trust

### Verified Entity
- discovered from text but reviewed/approved

### Discovered Entity
- machine-promoted with sufficient evidence but not yet reviewed

### Candidate
- pre-entity evidence bucket

This hierarchy prevents text noise from contaminating the highest-trust layer.

---

## Relation Model

Formal relations should retain evidence.

### Relation Event
- source memory / document
- entity pair
- event timestamp
- extraction method
- source system
- confidence

### Stable Relation
- relation type
- aggregate confidence
- first seen
- last seen
- event count
- distinct source count
- representative evidence samples

Possible relation types:
- `belongs_to`
- `depends_on`
- `uses`
- `owner_of`
- `collaborates_with`
- `discussed_with`
- `related_to`

---

## Proposed Data Model

### Core tables
- `master_entities`
- `master_entity_aliases`
- `entity_mentions`
- `entity_link_results`
- `entity_candidates`
- `discovered_entities`
- `memory_entities`
- `relation_events`
- `entity_relations`
- `entity_quality_scores`
- `entity_review_tasks`
- `entity_audit_logs`

### Key idea
Formal graph is only one layer in a larger evidence system.

---

## Online vs Offline Split

### Online path
- parse text
- attempt authority/entity link
- write only high-confidence associations
- enqueue unresolved mentions and relation events

### Offline path
- aggregate candidates
- compute entity quality scores
- run alias normalization
- promote candidates
- consolidate stable relations
- recompute graph features

### Why this split matters
- protects write latency
- avoids graph explosion in hot path
- allows expensive analysis without blocking user workflows

---

## Master Data Strategy

### Principle
Authority entities should be imported and matched, not rediscovered from scratch.

### Import sources
- HR / org chart
- service catalog / CMDB
- Git repositories
- project management systems
- customer / product systems

### Text role
- detect mentions
- link to canonical entities
- discover missing aliases
- surface unknown candidates

This is the most important enterprise difference from a pure text-built graph.

---

## Review and Governance

Enterprise usage requires correction workflows.

### Review actions
- approve candidate promotion
- merge duplicate entities
- split conflated entities
- correct aliases
- delete noisy relations
- mark blacklist terms

### Audit requirements
Every promoted entity and relation should preserve:
- who/what created it
- from which evidence
- by which method
- when it changed

---

## Service and Deployment Model

Suggested deployable units:
- `text-engine-service`
- `entity-linking-service`
- `offline-candidate-job`
- `offline-relation-job`
- `graph-serving-service`
- `review-admin`

The current monolith can still host phase-1 implementations, but this plan defines the long-term boundaries.

---

## Migration Strategy

### Stage 1
- adopt lightweight general text engine
- preserve monolith deployment

### Stage 2
- add balanced mention/link/decision pipeline

### Stage 3
- import authority entity sources
- add alias and canonical mapping

### Stage 4
- introduce offline aggregation and relation consolidation

### Stage 5
- add governance and review tooling

This plan is intentionally evolutionary, not a rewrite mandate.

---

## Metrics

### Quality
- canonical link rate
- discovered entity precision
- duplicate entity rate
- noisy relation rate
- review acceptance rate

### Scale
- mention volume
- candidate backlog
- promoted entity volume
- relation event volume
- stable relation volume

### Performance
- online write latency
- offline processing throughput
- graph retrieval latency

---

## Risks

1. Master data integration complexity may dominate delivery.
2. Governance tooling adds product and operational overhead.
3. Poor source-system quality can poison canonical mappings.
4. Offline/online divergence may create temporary consistency gaps.

---

## Acceptance Criteria

1. The online path remains bounded and does not perform heavy graph construction.
2. Canonical entities can be imported and linked from authority systems.
3. New text-discovered entities stay outside the highest-trust layer until sufficiently validated.
4. Stable relations are evidence-backed and auditable.
5. Humans can inspect and correct graph errors without direct database surgery.

---

## Recommended Decision

Approve this plan as the long-term target state if the system is expected to become:
- a shared enterprise knowledge substrate,
- a graph-backed retrieval platform,
- or a cross-system memory layer with governance requirements.

Use this plan as the north star, not the first implementation milestone.

