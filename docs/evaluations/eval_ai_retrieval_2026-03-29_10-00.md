# AI/Retrieval Specialist Evaluation Report

**Date**: 2026-03-29
**Scope**: Vector retrieval quality, RRF fusion, Reflect engine, Extractor, Document chunker, Embedding pipeline, Three-way retrieval weight configuration
**Evaluator**: AI Engineer (Opus 4.6)

---

## Total Score: 7.8 / 10

The IClude retrieval system is well-architected with thoughtful layering, solid fallback strategies, and production-grade defensive coding. The three-way retrieval with weighted RRF is a strong differentiator. However, several issues in the document chunking pipeline, embedding flow, and entity extraction scalability limit the system from reaching its full potential.

---

## Key Findings

### CRITICAL

**C1. Document chunker does NOT generate embeddings for chunks**

File: `/root/LocalMem/internal/document/processor.go` (lines 180-210)

The `processDocument` pipeline creates `model.Memory` objects from chunks and writes them to `memStore.Create()`, but this bypasses the `Manager.Create()` path entirely. As a result:
- No embedding is generated or stored in Qdrant for document chunks
- No vector dedup is performed
- No entity extraction is triggered
- No abstract generation occurs

Document chunks are effectively invisible to vector search -- they only participate in FTS5. This is a fundamental gap: the entire document ingestion pipeline promises semantic search but delivers keyword-only search.

**C2. Token estimation in chunker uses byte-length heuristic (`len(s)/3`), not rune-aware**

File: `/root/LocalMem/internal/document/chunker.go` (lines 36-41)

```go
func estimateTokens(s string) int {
    return (len(s) + 2) / 3
}
```

This uses `len(s)` (byte count), not rune count. For CJK text where one character is 3 bytes UTF-8, this estimates 1 token per CJK character -- which happens to be roughly correct. But the `retriever.go` has a proper `EstimateTokens()` function (line 443) that uses a hybrid CJK/English strategy with much better accuracy. The chunker should reuse it instead of having a divergent, lower-quality implementation.

The overlap calculation (`overlapChars`) also uses byte-based `len(prev)` which will cut multi-byte CJK characters mid-rune, producing invalid UTF-8 in overlap regions.

### HIGH

**H1. Qdrant `SearchFiltered` does not support time-range filters (`HappenedAfter`/`HappenedBefore`)**

File: `/root/LocalMem/internal/store/qdrant.go` (lines 150-222)

The `SearchFiltered` method maps `Scope`, `Kind`, `ContextID`, `SourceType`, `RetentionTier`, and `MessageRole` to Qdrant filter conditions, but completely ignores `HappenedAfter` and `HappenedBefore` from `model.SearchFilters`. When the preprocessor detects a temporal intent and injects a time filter, the Qdrant channel silently returns unfiltered results by time. This makes temporal queries unreliable in hybrid mode since the Qdrant channel will pollute the RRF fusion with time-irrelevant results.

**H2. Graph channel N+1 query problem in `graphTraverseAndCollect`**

File: `/root/LocalMem/internal/search/retriever.go` (lines 307-395)

For each entity in the visited set, `GetEntityRelations` and `GetEntityMemories` are called individually. With `graph_entity_limit=10` entities each having relations, this can produce 20+ sequential SQLite queries per graph retrieval. For depth=2, this compounds further. No batching API exists on `GraphStore`.

**H3. Entity extraction `resolveRelation` does O(N) full scan for dedup**

File: `/root/LocalMem/internal/memory/extractor.go` (lines 399-442)

`resolveRelation` calls `GetEntityRelations(ctx, sourceID)` to fetch ALL relations for the source entity, then iterates to find a matching `targetID + relationType`. For entities with many relations, this becomes increasingly expensive. An indexed lookup by `(sourceID, targetID, relationType)` would be O(1).

**H4. Consolidation candidate selection loads memories without vector filter**

File: `/root/LocalMem/internal/memory/consolidation.go` (lines 88-114)

`selectCandidates` uses `memStore.List()` with a fixed `MaxMemoriesPerRun` limit. This returns the first N memories by whatever the List ordering is (likely creation time), not the most consolidation-worthy ones. Memories without vectors are silently included and will have `distance=1.0` in clustering, never forming clusters but still consuming the budget.

### MEDIUM

**M1. RRF fusion ignores original score magnitude from different channels**

File: `/root/LocalMem/internal/search/rrf.go`

The RRF algorithm uses rank position only (`1/(k+rank+1)`), discarding the original scores from each channel. This is by design in standard RRF, but the project's FTS5 returns BM25 scores and Qdrant returns cosine similarity scores. The graph channel synthesizes depth-based scores. Since the channels have wildly different score distributions, a score-aware fusion (e.g., CombSUM with Z-score normalization) could produce better results. This is not a bug but an optimization opportunity.

**M2. Preprocessor intent classification has no cross-lingual coverage for mixed-language queries**

File: `/root/LocalMem/internal/search/preprocess.go`

The temporal, relational, and exploratory regex patterns handle English and Chinese separately, but mixed-language queries (e.g., "show me the recent LLM 调研报告") may not match correctly if the trigger keyword is in the Chinese portion but the overall rune count / CJK ratio pushes it into a different intent bucket.

**M3. Reflect engine does not carry conversation history across rounds**

File: `/root/LocalMem/internal/reflect/engine.go` (lines 182-189)

Each round sends only the system prompt + current round's user message to the LLM. Previous rounds' reasoning, retrieved memories, and LLM responses are discarded. The LLM has no memory of what it already concluded in prior rounds, making multi-round reasoning less effective. The `round X/Y` label in the prompt is the only context.

**M4. OpenAI embedder hardcodes endpoint URL**

File: `/root/LocalMem/internal/embed/openai.go` (line 39)

```go
const openaiEmbeddingsURL = "https://api.openai.com/v1/embeddings"
```

The LLM config has `base_url` for API-compatible providers (DeepSeek, Azure, etc.), but the embedding client does not respect this. Users who set `llm.openai.base_url` to a compatible endpoint will find that LLM calls go to the custom endpoint but embeddings always go to OpenAI's server. This breaks self-hosted / proxy setups.

**M5. Qdrant visibility filter uses OR-style `Should` but does not properly combine team+private logic**

File: `/root/LocalMem/internal/store/qdrant.go` (lines 127-148)

The `buildVisibilityFilter` uses Qdrant's `Should` (OR) with conditions:
1. `visibility=public`
2. `team_id=X` (if team_id set)
3. `owner_id=Y` (if owner_id set)

Condition 2 matches ANY memory belonging to the same team regardless of visibility. A `team_id=X AND visibility=team` compound condition is needed, but Qdrant's `Should` does not support compound per-clause conditions in this form. This means private memories from the same team are visible to all team members.

**M6. Chunker overlap is character-based, not sentence-boundary aligned**

File: `/root/LocalMem/internal/document/chunker.go` (lines 88-99)

The overlap mechanism takes the last N characters of the previous chunk and prepends them to the next. This can cut mid-sentence or mid-word, creating fragments that degrade both FTS quality and embedding quality. Sentence-boundary or paragraph-boundary overlap would produce more coherent chunks.

### LOW

**L1. Duplicate `cosineSimilarity` implementations**

`search/mmr.go:cosineSimilarity` and `memory/consolidation.go:cosineSimFloat32` are identical functions. Should be extracted to a shared `pkg/mathutil` or similar.

**L2. Extractor entity type whitelist is hardcoded and limited**

File: `/root/LocalMem/internal/memory/extractor.go` (lines 30-37)

Only 5 entity types (`person, org, concept, tool, location`) and 4 relation types (`uses, knows, belongs_to, related_to`) are valid. The LLM may extract richer types (e.g., `event`, `project`, `technology`, `causes`, `precedes`) that are silently discarded. This limits knowledge graph expressiveness.

**L3. Ollama embedder has no retry logic**

File: `/root/LocalMem/internal/embed/ollama.go`

The OpenAI embedder has 3-retry with exponential backoff for 429/5xx errors, but the Ollama embedder has zero retry logic. Local Ollama instances can have transient failures (model loading, GPU OOM) that would benefit from at least 1 retry.

**L4. MMR lambda default 0.7 may over-prioritize relevance**

File: `/root/LocalMem/config.yaml` (line 78)

Lambda=0.7 means 70% relevance, 30% diversity. For a memory system where users often query broad topics, a slightly lower lambda (0.5-0.6) might surface more diverse and useful memories. This is subjective but worth A/B testing.

**L5. Reflect auto-save does not deduplicate against existing mental models**

File: `/root/LocalMem/internal/reflect/engine.go` (lines 286-310)

When `auto_save=true`, the conclusion is saved as a new `mental_model` memory without checking if a similar mental model already exists. Repeated questions will accumulate near-duplicate mental models. The hash dedup in `Manager.Create` helps for exact matches, but semantically similar conclusions from slightly different phrasing will slip through.

---

## Improvement Recommendations (Priority Ordered)

### P0 -- Critical Path

1. **Route document chunks through `Manager.Create()` or add explicit embedding generation in the document processor pipeline.** This is the most impactful fix -- without it, document ingestion cannot participate in vector search. Either refactor `processDocument` to use `Manager.Create()` for each chunk (gaining dedup, embedding, extraction), or add an explicit embedding step in the processor after chunk creation.

2. **Replace `estimateTokens` in chunker with the rune-aware `EstimateTokens` from `search/retriever.go`**, or extract it to a shared `pkg/tokenutil` package. Fix the overlap calculation to use rune-based slicing to prevent invalid UTF-8.

### P1 -- High Impact

3. **Add time-range filter support to `QdrantVectorStore.SearchFiltered`** by mapping `HappenedAfter`/`HappenedBefore` to Qdrant range conditions on a `created_at` or `happened_at` numeric payload field.

4. **Fix Qdrant visibility filter** -- replace the flat `Should` with proper compound conditions: `visibility=public OR (team_id=X AND visibility=team) OR (owner_id=Y AND visibility=private)`. This requires nested Qdrant filter conditions.

5. **Make the OpenAI embedder respect `base_url` configuration** so that users of API-compatible providers get consistent behavior for both LLM and embedding calls.

6. **Add batch graph queries** (`GetEntityRelationsBatch`, `GetEntityMemoriesBatch`) to eliminate the N+1 problem in graph retrieval.

### P2 -- Quality Improvements

7. **Carry conversation history in Reflect engine rounds** -- append each round's retrieved memories and LLM response to the message history so subsequent rounds have full context.

8. **Add sentence-boundary-aware overlap** in the chunker, or at minimum use rune-boundary overlap instead of byte-boundary.

9. **Add indexed dedup check for relations** in the Extractor, replacing the O(N) scan with a direct `(source_id, target_id, relation_type)` lookup.

10. **Make entity types and relation types configurable** via `config.yaml` so the knowledge graph can be extended without code changes.

### P3 -- Nice to Have

11. Add retry logic to the Ollama embedder (match OpenAI's 3-retry with backoff pattern).
12. Extract `cosineSimilarity` to a shared utility package.
13. A/B test RRF vs score-aware fusion (CombSUM) on real query workloads.
14. Add dedup check for Reflect auto-save conclusions.

---

## Highlights

1. **Three-way retrieval architecture is excellent.** The combination of FTS5 (BM25), Qdrant (vector), and graph association with weighted RRF fusion is a sophisticated and well-designed approach. The `MergeWeightedRRF` function is clean and correct.

2. **Intent-aware query preprocessing is production-quality.** The `Preprocessor` with CJK-aware intent classification, dynamic weight adjustment per intent, entity pre-matching, and optional LLM enhancement is a well-layered design. The language-aware thresholds (8 runes CJK vs 20 runes English for short queries) show real-world tuning.

3. **3-level fallback parsing pattern is robust.** Both the Reflect engine and Extractor use JSON -> regex extract -> LLM retry -> raw fallback. This defensive pattern handles LLM output variability gracefully and is a strong production practice.

4. **MMR diversity re-ranking is a valuable addition.** Post-RRF MMR reranking with configurable lambda and per-request override provides fine-grained control over result diversity. The greedy selection algorithm is correctly implemented.

5. **Dual-write with best-effort Qdrant is pragmatic.** SQLite as primary with non-blocking Qdrant writes is the right architectural choice for a local-first system. Failures are logged but do not roll back the primary store.

6. **Memory lifecycle system is well-designed.** Retention tiers with configurable decay, strength weighting, reinforcement, crystallization (auto-promotion), and consolidation (LLM-powered merging) form a complete memory lifecycle. The `CalculateEffectiveStrength` integration into retrieval scoring is a good touch.

7. **Vector dedup with dual thresholds (skip/merge) is thoughtful.** The two-threshold approach allows for exact duplicate rejection while flagging near-duplicates for potential future merging. Combined with hash dedup, this provides multi-layer dedup coverage.

8. **Markdown-aware 3-layer chunking is well-designed.** Structure splitting (headings/code/tables) -> recursive size splitting -> context prefix enrichment is a solid pipeline. The `KeepTableIntact` and `KeepCodeIntact` options preserve semantic boundaries.

9. **LLM fallback in graph retrieval is clever.** When FTS5 finds no matching entities for graph traversal, falling back to LLM entity extraction from the query ensures the graph channel degrades gracefully rather than returning empty.

10. **Config-driven feature gating via nil checks** allows the system to run in any combination of backends (SQLite-only, Qdrant-only, hybrid) without code changes, with clean nil-check guards throughout.
