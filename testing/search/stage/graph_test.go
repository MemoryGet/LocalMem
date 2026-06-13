package stage_test

import (
	"context"
	"fmt"
	"testing"

	"iclude/internal/model"
	"iclude/internal/search/pipeline"
	"iclude/internal/search/stage"
)

// --- Mock implementations ---

// mockGraphRetriever 图检索 mock / Mock graph retriever
type mockGraphRetriever struct {
	entitiesByName map[string][]*model.Entity         // name → entities
	relations      map[string][]*model.EntityRelation // entityID → relations
	entityMemories map[string][]*model.Memory         // entityID → memories
	memoryEntities map[string][]*model.Entity         // memoryID → entities
}

func (m *mockGraphRetriever) FindEntitiesByName(_ context.Context, name, _ string, limit int) ([]*model.Entity, error) {
	entities := m.entitiesByName[name]
	if len(entities) > limit {
		entities = entities[:limit]
	}
	return entities, nil
}

func (m *mockGraphRetriever) GetEntityRelations(_ context.Context, entityID string) ([]*model.EntityRelation, error) {
	return m.relations[entityID], nil
}

func (m *mockGraphRetriever) GetEntityMemories(_ context.Context, entityID string, limit int) ([]*model.Memory, error) {
	memories := m.entityMemories[entityID]
	if len(memories) > limit {
		memories = memories[:limit]
	}
	return memories, nil
}

func (m *mockGraphRetriever) GetMemoryEntities(_ context.Context, memoryID string) ([]*model.Entity, error) {
	return m.memoryEntities[memoryID], nil
}

func (m *mockGraphRetriever) GetMemoriesEntities(_ context.Context, memoryIDs []string) (map[string][]*model.Entity, error) {
	result := make(map[string][]*model.Entity, len(memoryIDs))
	for _, id := range memoryIDs {
		if ents, ok := m.memoryEntities[id]; ok {
			result[id] = ents
		}
	}
	return result, nil
}

// ftsSearcherSpy 已在 fts_test.go 中定义 / Defined in fts_test.go

// --- Helper functions ---

func newEntity(id, name string) *model.Entity {
	return &model.Entity{ID: id, Name: name}
}

func newMemory(id, content string) *model.Memory {
	return &model.Memory{ID: id, Content: content}
}

func newRelation(id, sourceID, targetID string) *model.EntityRelation {
	return &model.EntityRelation{ID: id, SourceID: sourceID, TargetID: targetID}
}

// newRelationMC 构造带共现次数的关系 / Relation with mention count
func newRelationMC(id, sourceID, targetID string, mc int) *model.EntityRelation {
	return &model.EntityRelation{ID: id, SourceID: sourceID, TargetID: targetID, MentionCount: mc}
}

// scoreOf 返回指定记忆 ID 的候选分数，找不到返回 -1 / Score of a memory ID, -1 if absent
func scoreOf(results []*model.SearchResult, memID string) float64 {
	for _, r := range results {
		if r.Memory != nil && r.Memory.ID == memID {
			return r.Score
		}
	}
	return -1
}

func newState(query string) *pipeline.PipelineState {
	return pipeline.NewState(query, &model.Identity{TeamID: "team1", OwnerID: "owner1"})
}

// --- Tests ---

func TestGraphStage_Name(t *testing.T) {
	s := stage.NewGraphStage(nil, nil)
	if s.Name() != "graph" {
		t.Errorf("Name() = %q, want %q", s.Name(), "graph")
	}
}

func TestGraphStage_NilStore(t *testing.T) {
	// graphStore nil → 0 candidates, no error
	s := stage.NewGraphStage(nil, nil)
	state := newState("test query")

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}
	if len(result.Candidates) != 0 {
		t.Errorf("Candidates = %d, want 0", len(result.Candidates))
	}
	// 应有 skipped trace / Should have skipped trace
	if len(result.Traces) == 0 {
		t.Fatal("expected at least one trace")
	}
	if !result.Traces[len(result.Traces)-1].Skipped {
		t.Error("expected trace to be marked as skipped")
	}
}

func TestGraphStage_WithPreExtractedEntities(t *testing.T) {
	// Plan has entity names → FindEntitiesByName → traverse → collect memories
	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{
			"Go": {newEntity("ent-go", "Go")},
		},
		relations: map[string][]*model.EntityRelation{
			"ent-go": {newRelation("rel-1", "ent-go", "ent-concurrency")},
		},
		entityMemories: map[string][]*model.Memory{
			"ent-go":          {newMemory("mem-1", "Go is a language")},
			"ent-concurrency": {newMemory("mem-2", "Concurrency patterns")},
		},
		memoryEntities: map[string][]*model.Entity{},
	}

	s := stage.NewGraphStage(graph, nil)
	state := newState("Go concurrency")
	state.Plan = &pipeline.QueryPlan{
		Entities: []string{"Go"},
	}

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}
	if len(result.Candidates) < 1 {
		t.Fatalf("Candidates = %d, want >= 1", len(result.Candidates))
	}
	// 所有结果应标记为 graph source / All results should have graph source
	for _, c := range result.Candidates {
		if c.Source != "graph" {
			t.Errorf("Candidate source = %q, want %q", c.Source, "graph")
		}
	}
}

func TestGraphStage_WithFTSReverseLookup(t *testing.T) {
	// No plan entities, FTS finds memories → GetMemoryEntities → traverse → candidates
	ftsResults := []*model.SearchResult{
		{Memory: newMemory("fts-mem-1", "Found via FTS"), Score: 0.9},
	}
	fts := &ftsSearcherSpy{results: ftsResults}

	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{},
		relations: map[string][]*model.EntityRelation{
			"ent-fts": {newRelation("rel-fts", "ent-fts", "ent-related")},
		},
		entityMemories: map[string][]*model.Memory{
			"ent-fts":     {newMemory("mem-fts-1", "FTS entity memory")},
			"ent-related": {newMemory("mem-related", "Related memory")},
		},
		memoryEntities: map[string][]*model.Entity{
			"fts-mem-1": {newEntity("ent-fts", "FTS entity")},
		},
	}

	s := stage.NewGraphStage(graph, fts)
	state := newState("some query")

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}
	if len(result.Candidates) < 1 {
		t.Fatalf("Candidates = %d, want >= 1", len(result.Candidates))
	}
	for _, c := range result.Candidates {
		if c.Source != "graph" {
			t.Errorf("Candidate source = %q, want %q", c.Source, "graph")
		}
	}
}

func TestGraphStage_NoEntitiesFound(t *testing.T) {
	// Neither plan nor FTS finds entities → 0 candidates
	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{},
		relations:      map[string][]*model.EntityRelation{},
		entityMemories: map[string][]*model.Memory{},
		memoryEntities: map[string][]*model.Entity{},
	}

	// FTS returns results but no entities linked to them
	fts := &ftsSearcherSpy{
		results: []*model.SearchResult{
			{Memory: newMemory("orphan", "No entities"), Score: 0.5},
		},
	}

	s := stage.NewGraphStage(graph, fts)
	state := newState("orphan query")

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}
	if len(result.Candidates) != 0 {
		t.Errorf("Candidates = %d, want 0", len(result.Candidates))
	}
}

func TestGraphStage_DepthDecayScoring(t *testing.T) {
	// Verify depth 0 → score 1.0, depth 1 → score 0.5
	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{
			"root": {newEntity("ent-root", "root")},
		},
		relations: map[string][]*model.EntityRelation{
			"ent-root": {newRelation("rel-1", "ent-root", "ent-child")},
		},
		entityMemories: map[string][]*model.Memory{
			"ent-root":  {newMemory("mem-depth0", "Root memory")},
			"ent-child": {newMemory("mem-depth1", "Child memory")},
		},
		memoryEntities: map[string][]*model.Entity{},
	}

	s := stage.NewGraphStage(graph, nil, stage.WithMaxDepth(2))
	state := newState("root query")
	state.Plan = &pipeline.QueryPlan{
		Entities: []string{"root"},
	}

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}

	// 收集 score 按 memory ID / Collect scores by memory ID
	scores := make(map[string]float64)
	for _, c := range result.Candidates {
		scores[c.Memory.ID] = c.Score
	}

	// depth 0 → 1/(0+1) = 1.0
	if s0, ok := scores["mem-depth0"]; !ok {
		t.Error("missing mem-depth0")
	} else if s0 != 1.0 {
		t.Errorf("mem-depth0 score = %f, want 1.0", s0)
	}

	// depth 1 → 1/(1+1) = 0.5
	if s1, ok := scores["mem-depth1"]; !ok {
		t.Error("missing mem-depth1")
	} else if s1 != 0.5 {
		t.Errorf("mem-depth1 score = %f, want 0.5", s1)
	}
}

func TestGraphStage_FanOutCap(t *testing.T) {
	// Many relations → capped at maxVisitedEntities (50)
	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{
			"hub": {newEntity("ent-hub", "hub")},
		},
		relations:      map[string][]*model.EntityRelation{},
		entityMemories: map[string][]*model.Memory{},
		memoryEntities: map[string][]*model.Entity{},
	}

	// ent-hub 有 60 个直接关系目标 / ent-hub has 60 direct relation targets
	hubRelations := make([]*model.EntityRelation, 60)
	for i := 0; i < 60; i++ {
		targetID := fmt.Sprintf("ent-fan-%d", i)
		hubRelations[i] = newRelation(fmt.Sprintf("rel-%d", i), "ent-hub", targetID)
		graph.entityMemories[targetID] = []*model.Memory{
			newMemory(fmt.Sprintf("mem-fan-%d", i), fmt.Sprintf("Fan-out memory %d", i)),
		}
	}
	graph.relations["ent-hub"] = hubRelations
	graph.entityMemories["ent-hub"] = []*model.Memory{
		newMemory("mem-hub", "Hub memory"),
	}

	s := stage.NewGraphStage(graph, nil, stage.WithMaxDepth(1))
	state := newState("fan-out query")
	state.Plan = &pipeline.QueryPlan{
		Entities: []string{"hub"},
	}

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}

	// 总共不能超过 maxVisitedEntities(50) 个实体产出的记忆
	// Total should not exceed memories from maxVisitedEntities(50) entities
	// hub(1) + up to 49 fan-out = 50 entities max
	if len(result.Candidates) > 50 {
		t.Errorf("Candidates = %d, want <= 50 (fan-out cap)", len(result.Candidates))
	}
	// 但应该至少有 hub 自身的记忆 / At least hub's own memory
	if len(result.Candidates) < 1 {
		t.Error("Candidates = 0, expected at least hub memory")
	}
}

func TestGraphStage_LimitResults(t *testing.T) {
	// 结果超过 limit 时截断 / Truncate when results exceed limit
	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{
			"seed": {newEntity("ent-seed", "seed")},
		},
		relations: map[string][]*model.EntityRelation{},
		entityMemories: map[string][]*model.Memory{
			"ent-seed": make([]*model.Memory, 20),
		},
		memoryEntities: map[string][]*model.Entity{},
	}
	for i := 0; i < 20; i++ {
		graph.entityMemories["ent-seed"][i] = newMemory(fmt.Sprintf("mem-%d", i), fmt.Sprintf("Memory %d", i))
	}

	s := stage.NewGraphStage(graph, nil, stage.WithLimit(5))
	state := newState("limit test")
	state.Plan = &pipeline.QueryPlan{
		Entities: []string{"seed"},
	}

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}
	if len(result.Candidates) > 5 {
		t.Errorf("Candidates = %d, want <= 5", len(result.Candidates))
	}
}

func TestGraphStage_DeduplicatesMemories(t *testing.T) {
	// 同一 memory 被多个实体引用时应去重 / Same memory referenced by multiple entities should be deduplicated
	sharedMem := newMemory("mem-shared", "Shared memory")
	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{
			"alpha": {newEntity("ent-a", "alpha")},
		},
		relations: map[string][]*model.EntityRelation{
			"ent-a": {newRelation("rel-1", "ent-a", "ent-b")},
		},
		entityMemories: map[string][]*model.Memory{
			"ent-a": {sharedMem},
			"ent-b": {sharedMem},
		},
		memoryEntities: map[string][]*model.Entity{},
	}

	s := stage.NewGraphStage(graph, nil, stage.WithMaxDepth(1))
	state := newState("dedup test")
	state.Plan = &pipeline.QueryPlan{
		Entities: []string{"alpha"},
	}

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}

	// 同一 memory 只应出现一次 / Same memory should appear only once
	seen := make(map[string]bool)
	for _, c := range result.Candidates {
		if seen[c.Memory.ID] {
			t.Errorf("duplicate memory ID %q in candidates", c.Memory.ID)
		}
		seen[c.Memory.ID] = true
	}
}

func TestGraphStage_AppendsToCandidates(t *testing.T) {
	// 已有 candidates 时 graph 结果应追加，而非覆盖 / Graph results append, not overwrite
	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{
			"entity": {newEntity("ent-1", "entity")},
		},
		relations: map[string][]*model.EntityRelation{},
		entityMemories: map[string][]*model.Memory{
			"ent-1": {newMemory("graph-mem", "From graph")},
		},
		memoryEntities: map[string][]*model.Entity{},
	}

	s := stage.NewGraphStage(graph, nil)
	state := newState("append test")
	state.Plan = &pipeline.QueryPlan{Entities: []string{"entity"}}
	// 预先添加一个 candidate / Pre-add one candidate
	state.Candidates = []*model.SearchResult{
		{Memory: newMemory("existing", "Existing"), Score: 0.8, Source: "fts"},
	}

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}
	if len(result.Candidates) < 2 {
		t.Errorf("Candidates = %d, want >= 2 (1 existing + graph results)", len(result.Candidates))
	}
	// 第一个应是原有的 / First should be the existing one
	if result.Candidates[0].Source != "fts" {
		t.Errorf("First candidate source = %q, want %q", result.Candidates[0].Source, "fts")
	}
}

// mockEmbedder 固定返回预设向量的 Embedder mock / Embedder mock that returns a preset vector
type mockEmbedder struct {
	vec []float32
	err error
}

func (m *mockEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return m.vec, m.err
}

// mockVectorSearcher 按预设结果返回的 VectorSearcher mock / VectorSearcher mock with preset results
type mockVectorSearcher struct {
	results []*model.SearchResult
	err     error
}

func (m *mockVectorSearcher) Search(_ context.Context, _ []float32, _ *model.Identity, _ int) ([]*model.SearchResult, error) {
	return m.results, m.err
}

func (m *mockVectorSearcher) SearchFiltered(_ context.Context, _ []float32, _ *model.SearchFilters, _ int) ([]*model.SearchResult, error) {
	return m.results, m.err
}

func (m *mockVectorSearcher) GetVectors(_ context.Context, _ []string) (map[string][]float32, error) {
	return nil, nil
}

// TestGraphStage_VectorEvidenceFilter 验证 Phase 2：向量证据过滤
// Verifies Phase 2: edges whose source_memory_id is not in the vector matched set are skipped.
func TestGraphStage_VectorEvidenceFilter(t *testing.T) {
	aliceID := "ent-alice"
	bobID := "ent-bob"
	dentistID := "ent-dentist"
	memGymID := "mem-gym"
	memDentistID := "mem-dentist"

	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{
			"Alice": {newEntity(aliceID, "Alice")},
		},
		relations: map[string][]*model.EntityRelation{
			aliceID: {
				// 边 1：证据记忆与 query 语义匹配（gym）/ Edge whose evidence is semantically relevant
				{ID: "rel-gym", SourceID: aliceID, TargetID: bobID, RelationType: "met", SourceMemoryID: memGymID},
				// 边 2：证据记忆与 query 无关（dentist）/ Edge whose evidence is irrelevant
				{ID: "rel-dentist", SourceID: aliceID, TargetID: dentistID, RelationType: "visited", SourceMemoryID: memDentistID},
			},
		},
		entityMemories: map[string][]*model.Memory{
			aliceID:   {newMemory("mem-alice", "Alice content")},
			bobID:     {newMemory("mem-bob", "Bob at gym content")},
			dentistID: {newMemory("mem-dentist-node", "Dentist content")},
		},
		memoryEntities: map[string][]*model.Entity{},
	}

	// 向量搜索只返回 gym 记忆（语义匹配）/ Vector search returns only the gym memory (semantic match)
	embedder := &mockEmbedder{vec: []float32{0.1, 0.2, 0.3}}
	vecSearcher := &mockVectorSearcher{
		results: []*model.SearchResult{
			{Memory: newMemory(memGymID, "Alice met Bob at the gym"), Score: 0.92},
		},
	}

	s := stage.NewGraphStage(graph, nil,
		stage.WithMaxDepth(2),
		stage.WithVectorEvidence(embedder, vecSearcher),
	)
	state := newState("gym workout")
	state.Plan = &pipeline.QueryPlan{Entities: []string{"Alice"}}

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}

	found := make(map[string]bool)
	for _, c := range result.Candidates {
		found[c.Memory.ID] = true
	}

	// gym 边通过过滤，Bob 的记忆应出现 / gym edge passes filter → Bob's memory present
	if !found["mem-bob"] {
		t.Error("expected mem-bob in results (gym edge evidence matched), but it was absent")
	}
	// dentist 边被过滤，Dentist 的记忆不应出现 / dentist edge filtered → Dentist memory absent
	if found["mem-dentist-node"] {
		t.Error("expected mem-dentist-node to be absent (dentist edge evidence not matched)")
	}
}

// TestGraphStage_VectorEvidenceFilter_NoVector 无向量支持时退回原始 BFS 行为
// Verifies that without vector support, all edges are traversed (original BFS behavior).
func TestGraphStage_VectorEvidenceFilter_NoVector(t *testing.T) {
	aliceID := "ent-alice"
	bobID := "ent-bob"
	dentistID := "ent-dentist"

	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{
			"Alice": {newEntity(aliceID, "Alice")},
		},
		relations: map[string][]*model.EntityRelation{
			aliceID: {
				{ID: "rel-gym", SourceID: aliceID, TargetID: bobID, RelationType: "met", SourceMemoryID: "mem-gym"},
				{ID: "rel-dentist", SourceID: aliceID, TargetID: dentistID, RelationType: "visited", SourceMemoryID: "mem-dentist"},
			},
		},
		entityMemories: map[string][]*model.Memory{
			aliceID:   {newMemory("mem-alice", "Alice")},
			bobID:     {newMemory("mem-bob", "Bob")},
			dentistID: {newMemory("mem-dentist-node", "Dentist")},
		},
		memoryEntities: map[string][]*model.Entity{},
	}

	// 无向量支持（不传 WithVectorEvidence）/ No vector support — no WithVectorEvidence
	s := stage.NewGraphStage(graph, nil, stage.WithMaxDepth(2))
	state := newState("gym workout")
	state.Plan = &pipeline.QueryPlan{Entities: []string{"Alice"}}

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}

	found := make(map[string]bool)
	for _, c := range result.Candidates {
		found[c.Memory.ID] = true
	}

	// 无过滤时两条边都应展开 / Without filtering, both edges should be traversed
	if !found["mem-bob"] {
		t.Error("expected mem-bob in results (no filtering)")
	}
	if !found["mem-dentist-node"] {
		t.Error("expected mem-dentist-node in results (no filtering)")
	}
}

// mockSalienceChecker 返回预设 hub 集合的 SalienceChecker mock / SalienceChecker mock with preset hub set
type mockSalienceChecker struct {
	hubs map[string]struct{}
}

func (m *mockSalienceChecker) GetHighFrequencyEntities(_ context.Context, _ int) (map[string]struct{}, error) {
	return m.hubs, nil
}

// TestGraphStage_SalienceFilter 验证 hub 实体跳过扩张但仍被访问
// Hub entities skip BFS expansion but are still visited (their memories are collected).
func TestGraphStage_SalienceFilter(t *testing.T) {
	// Alice 是 hub（超级节点），Bob 和 Gym 是普通节点
	// Alice is a hub (super-node); Bob and Gym are normal nodes.
	aliceID := "ent-alice"
	bobID := "ent-bob"
	gymID := "ent-gym"

	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{
			"Alice": {newEntity(aliceID, "Alice")},
		},
		relations: map[string][]*model.EntityRelation{
			// Alice 连接到 Bob 和 Gym（但 Alice 是 hub，不应从她扩张）
			aliceID: {
				{ID: "rel-ab", SourceID: aliceID, TargetID: bobID, RelationType: "knows"},
				{ID: "rel-ag", SourceID: aliceID, TargetID: gymID, RelationType: "visits"},
			},
			// Bob 连接到 Gym（Bob 不是 hub，应从他扩张）
			bobID: {
				{ID: "rel-bg", SourceID: bobID, TargetID: gymID, RelationType: "trains_at"},
			},
		},
		entityMemories: map[string][]*model.Memory{
			aliceID: {newMemory("mem-alice", "Alice general memory")},
			bobID:   {newMemory("mem-bob", "Bob at gym")},
			gymID:   {newMemory("mem-gym", "Gym sessions")},
		},
		memoryEntities: map[string][]*model.Entity{},
	}

	// Alice 是 hub（出现在 >N 条记忆中），跳过从她扩张
	// Alice is a hub, so BFS should not expand from her.
	salience := &mockSalienceChecker{
		hubs: map[string]struct{}{aliceID: {}},
	}

	s := stage.NewGraphStage(graph, nil,
		stage.WithMaxDepth(2),
		stage.WithSalienceFilter(salience, 50),
	)
	state := newState("gym workout")
	state.Plan = &pipeline.QueryPlan{Entities: []string{"Alice"}}

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() unexpected error: %v", err)
	}

	found := make(map[string]bool)
	for _, c := range result.Candidates {
		found[c.Memory.ID] = true
	}

	// Alice 的记忆仍应出现（hub 节点本身被访问，只是不向外扩张）
	// Alice's memory should still appear (hub is visited, just not expanded).
	if !found["mem-alice"] {
		t.Error("expected mem-alice (hub is visited but not expanded from)")
	}
	// Alice 是 hub → 不从她扩张 → Bob 和 Gym 不应通过 Alice 的边到达
	// Alice is hub → no expansion → Bob and Gym should NOT be reached via Alice's edges.
	if found["mem-bob"] {
		t.Error("mem-bob should NOT be reached (Alice is hub, no expansion from her)")
	}
	if found["mem-gym"] {
		t.Errorf("mem-gym should NOT be reached via Alice (hub expansion blocked); found=%v", found)
	}
}

// 强边路径得分 > 弱边路径（同深度，alpha=1）
func TestGraphStage_PathConfidence_StrongBeatsWeak(t *testing.T) {
	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{
			"Seed": {newEntity("S", "Seed")},
		},
		relations: map[string][]*model.EntityRelation{
			"S": {
				newRelationMC("r-strong", "S", "A", 9), // edgeConf 0.9
				newRelationMC("r-weak", "S", "B", 1),   // edgeConf 0.5
			},
		},
		entityMemories: map[string][]*model.Memory{
			"A": {newMemory("memA", "via strong edge")},
			"B": {newMemory("memB", "via weak edge")},
		},
		memoryEntities: map[string][]*model.Entity{},
	}

	s := stage.NewGraphStage(graph, nil, stage.WithPathConfidence(1.0))
	state := newState("seed query")
	state.Plan = &pipeline.QueryPlan{Entities: []string{"Seed"}}

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	scoreA, scoreB := scoreOf(result.Candidates, "memA"), scoreOf(result.Candidates, "memB")
	if !(scoreA > scoreB) {
		t.Errorf("strong-edge memA (%.4f) should outscore weak-edge memB (%.4f)", scoreA, scoreB)
	}
}

// 默认禁用（alpha=0）：强边/弱边得分相同
func TestGraphStage_PathConfidence_DisabledByDefault(t *testing.T) {
	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{"Seed": {newEntity("S", "Seed")}},
		relations: map[string][]*model.EntityRelation{
			"S": {
				newRelationMC("r-strong", "S", "A", 9),
				newRelationMC("r-weak", "S", "B", 1),
			},
		},
		entityMemories: map[string][]*model.Memory{
			"A": {newMemory("memA", "strong")},
			"B": {newMemory("memB", "weak")},
		},
		memoryEntities: map[string][]*model.Entity{},
	}

	s := stage.NewGraphStage(graph, nil) // no WithPathConfidence → alpha=0
	state := newState("seed query")
	state.Plan = &pipeline.QueryPlan{Entities: []string{"Seed"}}

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	scoreA, scoreB := scoreOf(result.Candidates, "memA"), scoreOf(result.Candidates, "memB")
	if scoreA != scoreB {
		t.Errorf("with alpha=0 scores must be equal: memA=%.4f memB=%.4f", scoreA, scoreB)
	}
}

// min/瓶颈聚合：2-hop 路径 conf = 最弱边（非乘积）
func TestGraphStage_PathConfidence_MinAggregation(t *testing.T) {
	// S --mc1(0.5)--> A --mc9(0.9)--> C : conf = min(1.0,0.5,0.9)=0.5 (not product 0.45)
	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{"Seed": {newEntity("S", "Seed")}},
		relations: map[string][]*model.EntityRelation{
			"S": {newRelationMC("r1", "S", "A", 1)}, // weak first hop, edgeConf 0.5
			"A": {newRelationMC("r2", "A", "C", 9)}, // strong second hop, edgeConf 0.9
		},
		entityMemories: map[string][]*model.Memory{
			"C": {newMemory("memC", "two hops")},
		},
		memoryEntities: map[string][]*model.Entity{},
	}

	s := stage.NewGraphStage(graph, nil, stage.WithPathConfidence(1.0))
	state := newState("seed query")
	state.Plan = &pipeline.QueryPlan{Entities: []string{"Seed"}}

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	// memC at depth 2: depthScore=1/3, timeDecay=1.0 (lambda=0), pathConf=min=0.5
	// expected score = (1/3)*0.5 = 0.16667 ; product would be (1/3)*0.45 = 0.15
	got := scoreOf(result.Candidates, "memC")
	want := (1.0 / 3.0) * 0.5
	if diff := got - want; diff > 0.001 || diff < -0.001 {
		t.Errorf("memC score = %.5f, want %.5f (min aggregation, not product)", got, want)
	}
}

// 种子（depth 0）conf=1.0，不被路径置信度降权
func TestGraphStage_PathConfidence_SeedNotDownweighted(t *testing.T) {
	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{"Seed": {newEntity("S", "Seed")}},
		relations:      map[string][]*model.EntityRelation{},
		entityMemories: map[string][]*model.Memory{
			"S": {newMemory("memS", "seed memory")},
		},
		memoryEntities: map[string][]*model.Entity{},
	}

	s := stage.NewGraphStage(graph, nil, stage.WithPathConfidence(1.0))
	state := newState("seed query")
	state.Plan = &pipeline.QueryPlan{Entities: []string{"Seed"}}

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	// seed at depth 0: depthScore=1.0, timeDecay=1.0, pathConfFactor=(1-1)+1*1.0=1.0 → score 1.0
	got := scoreOf(result.Candidates, "memS")
	if diff := got - 1.0; diff > 0.001 || diff < -0.001 {
		t.Errorf("seed memS score = %.5f, want 1.0 (not downweighted)", got)
	}
}

// 多路径到达同一实体：取最高 conf
func TestGraphStage_PathConfidence_MultiPathTakesMax(t *testing.T) {
	// Two seeds reach X at depth 1: S1--mc1(0.5)-->X and S2--mc9(0.9)-->X. Max → 0.9.
	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{
			"S1": {newEntity("S1", "S1")},
			"S2": {newEntity("S2", "S2")},
		},
		relations: map[string][]*model.EntityRelation{
			"S1": {newRelationMC("r1", "S1", "X", 1)}, // edgeConf 0.5
			"S2": {newRelationMC("r2", "S2", "X", 9)}, // edgeConf 0.9
		},
		entityMemories: map[string][]*model.Memory{
			"X": {newMemory("memX", "multi-path")},
		},
		memoryEntities: map[string][]*model.Entity{},
	}

	s := stage.NewGraphStage(graph, nil, stage.WithPathConfidence(1.0))
	state := newState("seed query")
	state.Plan = &pipeline.QueryPlan{Entities: []string{"S1", "S2"}}

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	// X at depth 1: depthScore=0.5, best conf=0.9 → score 0.5*0.9=0.45 (not 0.5*0.5=0.25)
	got := scoreOf(result.Candidates, "memX")
	want := 0.5 * 0.9
	if diff := got - want; diff > 0.001 || diff < -0.001 {
		t.Errorf("memX score = %.5f, want %.5f (max conf across paths)", got, want)
	}
}

// alpha 钳制：1.5 → 1.0
func TestGraphStage_PathConfidence_AlphaClampHigh(t *testing.T) {
	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{"Seed": {newEntity("S", "Seed")}},
		relations:      map[string][]*model.EntityRelation{"S": {newRelationMC("r", "S", "A", 1)}},
		entityMemories: map[string][]*model.Memory{"A": {newMemory("memA", "x")}},
		memoryEntities: map[string][]*model.Entity{},
	}

	s := stage.NewGraphStage(graph, nil, stage.WithPathConfidence(1.5)) // clamps to 1.0
	state := newState("seed query")
	state.Plan = &pipeline.QueryPlan{Entities: []string{"Seed"}}

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	// depth1, edgeConf(1)=0.5, alpha clamped to 1.0 → score 0.5*0.5=0.25
	got := scoreOf(result.Candidates, "memA")
	want := 0.5 * 0.5
	if diff := got - want; diff > 0.001 || diff < -0.001 {
		t.Errorf("memA score = %.5f, want %.5f (alpha clamped to 1.0)", got, want)
	}
}

// alpha 钳制：-0.5 → 0（禁用，得分回退到纯深度衰减，conf 被忽略）
// Alpha clamp low: -0.5 clamps to 0, disabling weighting — score falls back to depth decay.
func TestGraphStage_PathConfidence_AlphaClampLow(t *testing.T) {
	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{"Seed": {newEntity("S", "Seed")}},
		relations:      map[string][]*model.EntityRelation{"S": {newRelationMC("r", "S", "A", 1)}}, // edgeConf 0.5
		entityMemories: map[string][]*model.Memory{"A": {newMemory("memA", "x")}},
		memoryEntities: map[string][]*model.Entity{},
	}

	s := stage.NewGraphStage(graph, nil, stage.WithPathConfidence(-0.5)) // clamps to 0 (disabled)
	state := newState("seed query")
	state.Plan = &pipeline.QueryPlan{Entities: []string{"Seed"}}

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	// depth1, alpha clamped to 0 → pathConfFactor=1.0 → score = depthScore 0.5 (edgeConf ignored)
	got := scoreOf(result.Candidates, "memA")
	want := 0.5
	if diff := got - want; diff > 0.001 || diff < -0.001 {
		t.Errorf("memA score = %.5f, want %.5f (alpha clamped to 0, conf ignored)", got, want)
	}
}

// 边置信度中间值：mc=3 → edgeConf 0.75（通过 Execute 分数间接验证）
// Edge confidence mid value: mc=3 yields edgeConf 0.75, verified via Execute score.
func TestGraphStage_PathConfidence_EdgeConfMidValue(t *testing.T) {
	graph := &mockGraphRetriever{
		entitiesByName: map[string][]*model.Entity{"Seed": {newEntity("S", "Seed")}},
		relations:      map[string][]*model.EntityRelation{"S": {newRelationMC("r", "S", "A", 3)}}, // edgeConf 0.75
		entityMemories: map[string][]*model.Memory{"A": {newMemory("memA", "x")}},
		memoryEntities: map[string][]*model.Entity{},
	}

	s := stage.NewGraphStage(graph, nil, stage.WithPathConfidence(1.0))
	state := newState("seed query")
	state.Plan = &pipeline.QueryPlan{Entities: []string{"Seed"}}

	result, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	// depth1, edgeConf(3)=1-1/4=0.75, alpha=1 → score 0.5*0.75=0.375
	got := scoreOf(result.Candidates, "memA")
	want := 0.5 * 0.75
	if diff := got - want; diff > 0.001 || diff < -0.001 {
		t.Errorf("memA score = %.5f, want %.5f (edgeConf(3)=0.75)", got, want)
	}
}
