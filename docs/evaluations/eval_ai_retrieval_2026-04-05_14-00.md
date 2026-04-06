# AI/Retrieval System Assessment Report

**Date**: 2026-04-05  
**Evaluator**: AI Engineer (Opus 4.6)  
**Scope**: Search/Retrieval pipeline, Reflect Engine, Entity Extractor, Embedding adapters, Memory strength/decay scoring  
**Codebase Revision**: `a6b4ee2` (main)

---

## Overall Score: 8.2 / 10

The IClude retrieval system demonstrates strong architectural maturity. The four-channel weighted RRF fusion (FTS + Vector + Graph + Temporal), adaptive query preprocessing with intent classification, and multi-round Reflect engine represent a well-engineered pipeline. The codebase follows sound Go idioms with proper nil-safety throughout. There are several areas for improvement, mostly around numeric precision in scoring, missing similarity thresholds in vector search, and the LRU cache implementation's O(n) touch operation.

---

## Key Findings

### CRITICAL

**C1. Float64 equality comparison in sort functions (rrf.go:49, rrf.go:99)**

The RRF sort uses direct `!=` comparison on `float64` scores:

```go
if merged[i].Score != merged[j].Score {
    return merged[i].Score > merged[j].Score
}
```

Due to IEEE 754 floating-point arithmetic, accumulated RRF scores (`weight * 1.0/float64(k+rank+1)`) will produce values that *should* be equal but differ at the ULP level after different addition sequences. This can cause non-deterministic sort order across runs, making reproducibility hard to verify and debug. The same pattern appears in `reranker.go:111` and `retriever.go:299`.

**Recommendation**: Use an epsilon-based comparison (`math.Abs(a-b) < 1e-12`) or, since the tie-break by ID already exists, simply always fall through to the tie-breaker when scores are within epsilon.

**Impact**: Non-deterministic ranking when scores are extremely close; test flakiness.

---

**C2. No minimum similarity threshold on Qdrant vector search results**

The vector search channel (`retriever.go:183-200`) passes all Qdrant results directly into RRF without filtering by cosine similarity threshold. A query with no semantically relevant memories will still return the top-K nearest vectors (even if similarity is 0.3), and these will contribute to the fused ranking with full `qdrant_weight`.

The adaptive retry mechanism (`retriever.go:318`) checks `results[0].Score < 0.3` but this fires *after* RRF fusion, where scores are in the RRF domain (typically 0.01-0.03), not the cosine similarity domain. The 0.3 threshold is effectively unreachable for RRF scores, making the adaptive retry dead code in hybrid mode.

**Recommendation**: 
1. Add a configurable `min_similarity` threshold to filter vector results before RRF fusion.
2. Fix the adaptive retry threshold to operate in the correct score domain (RRF scores, not cosine similarity).

**Impact**: Low-quality vector results pollute hybrid rankings; adaptive retry never fires in hybrid mode.

---

### HIGH

**H1. Embedding cache LRU `touchLocked` is O(n) per cache hit**

`cache.go:160-168` performs a linear scan of the `order` slice to move a key to the end on every cache hit:

```go
func (c *CachedEmbedder) touchLocked(key string) {
    for i, k := range c.order {
        if k == key {
            c.order = append(c.order[:i], c.order[i+1:]...)
            c.order = append(c.order, key)
            return
        }
    }
}
```

With `maxSize=1000`, this is 1000 iterations per embedding hit under the write lock. At high QPS, this becomes a contention bottleneck on the `sync.Mutex`.

**Recommendation**: Replace with `container/list` (doubly-linked list) + map for O(1) LRU operations, which is the standard Go LRU pattern (e.g., `hashicorp/golang-lru`).

**Impact**: Latency degradation under sustained embedding load; mutex contention.

---

**H2. Extractor regex for JSON extraction is too greedy**

`extractor.go:254`:
```go
re := regexp.MustCompile(`\{[\s\S]*"entities"[\s\S]*\}`)
```

The `[\s\S]*` pattern is greedy and will match from the first `{` to the last `}` in the entire LLM output, potentially capturing markdown fences, explanatory text, and multiple JSON objects. The Reflect engine's regex (`engine.go:425`) is better -- it uses `[^{}]` with one level of nesting. The Extractor should adopt the same pattern.

**Recommendation**: Use a non-greedy or bounded regex: `\{(?:[^{}]|\{[^{}]*\})*"entities"(?:[^{}]|\{[^{}]*\})*\}` to match only the innermost JSON object containing "entities".

**Impact**: L2 fallback parse may extract incorrect/corrupt JSON, causing silent data quality issues in the knowledge graph.

---

**H3. Graph channel depth scoring does not participate in normalized RRF correctly**

Graph results use a synthetic depth-decay score (`1.0/(depth+1)`) in `retriever.go:499`. This score has a fundamentally different distribution from FTS5 BM25 scores and Qdrant cosine similarity scores. RRF is rank-based (not score-based), so the absolute scores do not affect fusion. However, the graph results are later subjected to `ApplyKindAndClassWeights` and `ApplyStrengthWeighting` which multiply these synthetic scores. A graph result at depth 0 with score 1.0 gets multiplied by kind weight (up to 2.0) and strength weight, producing a final score up to 2.0+. This can unfairly dominate over FTS/vector results whose post-RRF scores are in the 0.01-0.03 range.

**Recommendation**: After RRF fusion, all results already have RRF-domain scores. The `Source: "graph"` results that come through single-channel path (when only graph channel returns results) should have their scores normalized to the same domain as RRF scores, or the kind/class/strength weighting should be applied as multiplicative adjustments relative to the channel's score distribution.

**Impact**: Graph-only results can have disproportionately high final scores, distorting ranking in mixed scenarios.

---

**H4. Reflect engine lacks convergence detection beyond query dedup**

The Reflect engine deduplicates exact query strings (`engine.go:177`), but does not detect semantic convergence. If the LLM generates slightly different queries that retrieve the same evidence, the engine will burn through all rounds without converging. For example: "What is X?" -> "Tell me about X" -> "Explain X" would all pass dedup but retrieve identical memories.

**Recommendation**: Track retrieved memory ID sets across rounds. If >80% of retrieved IDs in round N overlap with previous rounds' IDs, force conclusion. This is cheap to implement and prevents token waste.

**Impact**: Unnecessary LLM calls and token consumption in Reflect sessions.

---

### MEDIUM

**M1. HyDE weight is hardcoded at 0.8 multiplied by FTS weight**

`retriever.go:159`:
```go
hydeWeight := 0.8
if plan.Weights.FTS > 0 {
    hydeWeight *= plan.Weights.FTS
}
```

The HyDE channel weight is hardcoded rather than configurable. Since HyDE quality depends heavily on the LLM model used, this should be a config parameter.

**Recommendation**: Add `retrieval.hyde_weight` to `config.yaml` with default 0.8.

---

**M2. Temporal channel weight is hardcoded at 1.2**

`retriever.go:245`: `Weight: 1.2` is hardcoded for the temporal channel. This should follow the same pattern as other channels and be configurable.

---

**M3. `ApplyStrengthWeighting` mutates `SearchResult.Score` in place**

`scoring/strength.go:43`: `r.Score *= effective` modifies the score on the shared `SearchResult` pointer. If the same results slice is used elsewhere (e.g., cached, logged, or passed to multiple consumers), this mutation causes invisible side effects. This violates the project's immutability coding style rule.

**Recommendation**: Create new `SearchResult` copies with adjusted scores rather than mutating in place.

---

**M4. `EmbedBatch` in `CachedEmbedder` does not update LRU order for cache hits**

`cache.go:80-89`: When checking the cache during `EmbedBatch`, hits are read under `RLock` but `touchLocked` is never called for those hits. This means frequently accessed embeddings in batch mode are not promoted in the LRU, leading to premature eviction.

**Recommendation**: After the batch operation completes, acquire write lock and call `touchLocked` for all cache-hit keys.

---

**M5. Entity normalization N+1 query problem**

`extractor.go:347`: `ListEntities` is called once per entity for normalization candidates, and `FindEntitiesByName` is called once per entity for exact match. For a content block with 10 entities, this generates 20 DB queries plus up to 10 LLM calls. This is expensive for the common case.

**Recommendation**: Batch the `FindEntitiesByName` calls (search all entity names in one query), and cache the `ListEntities` result across entities within the same extraction scope.

---

**M6. No batch size limit for OpenAI embedding API**

`openai.go:76-92`: `EmbedBatch` passes all texts in a single API call. The OpenAI embeddings API has a limit of ~8191 tokens per input and practical limits on total request size. Very large batches (e.g., 100+ document chunks) could exceed API limits or timeout.

**Recommendation**: Chunk the batch into groups of 50-100 texts and parallelize the API calls.

---

### LOW

**L1. Ollama backoff formula differs from OpenAI**

OpenAI uses `1<<uint(attempt-1)` seconds (1s, 2s), while Ollama uses `attempt*attempt * 500ms` (500ms, 2s). This inconsistency is minor but makes maintenance harder. Consider extracting a shared backoff utility.

**L2. `dedup` function in Reflect uses `map[string]bool` instead of `map[string]struct{}`**

Minor memory optimization opportunity for large source lists.

**L3. Preprocessor `expandSynonyms` cap at 30 keywords is hardcoded**

Should be configurable via `retrieval.preprocess.max_expanded_keywords`.

**L4. `weightCap` constant (2.0) in kind/class weighting is not configurable**

The maximum combined kind+class weight multiplier is capped at 2.0. For deployments that want stronger kind-based boosting (e.g., heavily skill-oriented retrieval), this should be a config option.

---

## Improvement Recommendations (Priority Ordered)

| Priority | Item | Effort | Impact |
|----------|------|--------|--------|
| P0 | C2: Add vector similarity threshold + fix adaptive retry score domain | Small | High -- prevents noise in hybrid ranking |
| P0 | C1: Fix float64 equality in sort comparisons | Tiny | Medium -- deterministic ranking |
| P1 | H1: Replace O(n) LRU with O(1) linked-list implementation | Medium | High -- latency at scale |
| P1 | H2: Fix greedy regex in Extractor L2 fallback | Small | Medium -- knowledge graph data quality |
| P1 | H3: Normalize graph-channel scores in single-channel path | Medium | Medium -- ranking fairness |
| P1 | H4: Add evidence overlap convergence detection to Reflect | Small | Medium -- token cost savings |
| P2 | M3: Eliminate score mutation in `ApplyStrengthWeighting` | Small | Low -- code safety |
| P2 | M5: Batch entity normalization queries | Medium | Medium -- extraction performance |
| P2 | M6: Add batch size chunking for embedding API | Small | Medium -- reliability at scale |
| P2 | M1/M2: Make HyDE and temporal weights configurable | Tiny | Low -- flexibility |
| P3 | M4: Fix LRU touch in EmbedBatch | Small | Low -- cache efficiency |

---

## Highlights

**1. Four-channel parallel retrieval with weighted RRF is well-architected.** The `Retriever.Retrieve` method cleanly orchestrates FTS, vector, graph, and temporal channels using goroutines with `sync.Mutex`-protected result aggregation. The `RRFInput` struct with per-channel weights is a clean abstraction. The single-channel fast path (skip RRF when only one channel returns results) is a smart optimization.

**2. Intent-aware query preprocessing is sophisticated.** The `Preprocessor` implements a multi-stage pipeline: tokenization with stop-word filtering, synonym expansion, entity matching, rule-based intent classification with CJK-aware thresholds, dynamic channel weight computation, optional LLM enhancement, and HyDE document generation. The `intentMultipliers` map cleanly encodes the relationship between query intent and channel weights.

**3. Reflect engine has production-grade robustness.** Adaptive Top-K based on round number, token budget, and evidence quality demonstrates careful engineering. The 3-level fallback parse (JSON -> regex -> LLM retry -> raw) with `ParseMethod` tracking in trace metadata enables observability. Token budget management that combines LLM tokens and evidence tokens is thorough.

**4. Embedding dimension validation at startup is an excellent safety net.** The probe-and-verify pattern in `bootstrap/wiring.go:91-109` catches dimension mismatches between the embedding model and Qdrant collection before any data is written, preventing silent corruption. The `Fatal` level ensures the server refuses to start with misconfigured dimensions.

**5. Embedding LRU cache with batch awareness.** The `CachedEmbedder` correctly handles batch operations by checking the cache per-item and only calling the underlying embedder for misses, then backfilling the cache. Cache hit/miss statistics logging enables operational monitoring.

**6. Memory strength decay model is well-designed.** The exponential decay formula `strength * exp(-decayRate * hours)` with access frequency boost `1 + alpha * log2(accessCount + 1)` (capped at 3.0) provides a biologically-inspired forgetting curve. The tier-based decay rate system (permanent through ephemeral) with automatic crystallization (tier promotion based on reinforcement count, strength, and age) is a thoughtful lifecycle management approach.

**7. Circuit breaker on remote reranker prevents cascade failures.** The `RemoteReranker` wraps HTTP calls with a proper three-state circuit breaker (closed/open/half-open), gracefully falling back to original ranking when the remote service is unavailable.

**8. Backfill pattern for hybrid results is clean.** The `backfillMemories` method correctly identifies Qdrant-only results (Content == "") and enriches them from SQLite, with proper copy semantics to avoid mutating shared pointers. This enables the hybrid architecture where Qdrant stores only vectors and IDs.

---

## Architecture Summary

```
Query Input
    |
    v
[Preprocessor] -- tokenize, stopwords, synonyms, intent classify, entity match, LLM enhance + HyDE
    |
    v
[Parallel Channels]
  |-- FTS5 (BM25) + HyDE channel
  |-- Qdrant Vector channel
  |-- Graph Association channel (FTS->entities->traverse, LLM fallback)
  |-- Temporal channel (distance-decay scoring)
    |
    v
[Weighted RRF Fusion] (k=60, per-channel weights from intent)
    |
    v
[Backfill] -- enrich Qdrant-only results from SQLite
    |
    v
[Reranker] -- overlap-based or remote HTTP (with circuit breaker)
    |
    v
[Kind + Class Weighting] -- skill/procedural boost, capped at 2.0x
    |
    v
[Memory Class Filter] -- optional episodic/semantic/procedural filter
    |
    v
[Strength Weighting] -- exponential decay + access frequency boost
    |
    v
[Re-sort by final score]
    |
    v
[MMR Diversity Re-ranking] -- optional, requires vector store
    |
    v
[Adaptive Retry] -- relax time filters if confidence low
    |
    v
[Access Tracking] -- async hit recording
    |
    v
Final Results
```

---

*Report generated: 2026-04-05 by AI Engineer assessment pipeline*
