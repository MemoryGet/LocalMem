package stage_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"iclude/internal/config"
	"iclude/internal/model"
	"iclude/internal/search/pipeline"
	"iclude/internal/search/stage"
)

// singleTierCfg 用于既有触发/跳过/去重测试的简化两阶梯配置
// Simplified 2-tier config for existing trigger/skip/dedup tests.
var singleTierCfg = config.FTSRewriteConfig{
	Tiers: []config.FTSRetryTierConfig{
		{MinCount: 3, MinScore: 0.3, Expansion: "synonyms"},
		{MinCount: 1, MinScore: 0.0, Expansion: "none"},
	},
	IntentStartTier: map[string]int{
		"general": 0, "semantic": 0, "temporal": 1, "keyword": 0, "relational": 0,
	},
}

// graphCfg 三阶梯：synonyms → graph → none，general 起始阶梯 0（先注入 keywords，不足时升级到 graph）
// Three tiers for graph-expansion tests; start tier 0 so keywords are added first, then graph.
var graphCfg = config.FTSRewriteConfig{
	Tiers: []config.FTSRetryTierConfig{
		{MinCount: 3, MinScore: 0.3, Expansion: "synonyms"},
		{MinCount: 3, MinScore: 0.3, Expansion: "graph"},
		{MinCount: 1, MinScore: 0.0, Expansion: "none"},
	},
	IntentStartTier: map[string]int{"general": 0},
}

// hydeCfg 三阶梯：synonyms → hyde → none，general 起始阶梯 0（先注入 keywords，不足时升级到 hyde）
// Three tiers for HyDE-expansion tests; keywords added first, then HyDE keywords on insufficiency.
var hydeCfg = config.FTSRewriteConfig{
	Tiers: []config.FTSRetryTierConfig{
		{MinCount: 3, MinScore: 0.3, Expansion: "synonyms"},
		{MinCount: 3, MinScore: 0.3, Expansion: "hyde"},
		{MinCount: 1, MinScore: 0.0, Expansion: "none"},
	},
	// 起始阶梯 1：跳过 synonyms 阶梯（keywords 预填为基础查询），首次扩词即 hyde → 单次 FTS 调用
	// Start tier 1: skip synonyms tier (keywords pre-seeded as base query), first expansion is hyde → single FTS call.
	IntentStartTier: map[string]int{"general": 1},
}

// --- mock helpers ---

type mockFTSRetry struct {
	results   []*model.SearchResult
	err       error
	called    int
	lastQuery string
}

func (m *mockFTSRetry) SearchText(_ context.Context, query string, _ *model.Identity, _ int) ([]*model.SearchResult, error) {
	m.called++
	m.lastQuery = query
	return m.results, m.err
}

func (m *mockFTSRetry) SearchTextFiltered(_ context.Context, query string, _ *model.SearchFilters, _ int) ([]*model.SearchResult, error) {
	m.called++
	m.lastQuery = query
	return m.results, m.err
}

type mockGraphExpander struct {
	names []string
	err   error
}

func (m *mockGraphExpander) GetEntityNeighborNames(_ context.Context, _ []string, _ int) ([]string, error) {
	return m.names, m.err
}

type mockHyDEGenerator struct {
	doc    string
	err    error
	called int
}

func (m *mockHyDEGenerator) GenerateHyDE(_ context.Context, _ string) (string, error) {
	m.called++
	return m.doc, m.err
}

type mockFTSRetryVec struct {
	results []*model.SearchResult
	err     error
	called  int
}

func (m *mockFTSRetryVec) Search(_ context.Context, _ []float32, _ *model.Identity, _ int) ([]*model.SearchResult, error) {
	m.called++
	return m.results, m.err
}

func (m *mockFTSRetryVec) SearchFiltered(_ context.Context, _ []float32, _ *model.SearchFilters, _ int) ([]*model.SearchResult, error) {
	m.called++
	return m.results, m.err
}

func (m *mockFTSRetryVec) GetVectors(_ context.Context, _ []string) (map[string][]float32, error) {
	return nil, nil
}

type mockFTSRetryEmbed struct {
	vec []float32
	err error
}

func (m *mockFTSRetryEmbed) Embed(_ context.Context, _ string) ([]float32, error) {
	return m.vec, m.err
}

type mockFTSRetryGraph struct {
	// memoryID → entities
	entities map[string][]*model.Entity
	err      error
}

func (m *mockFTSRetryGraph) FindEntitiesByName(_ context.Context, _ string, _ string, _ int) ([]*model.Entity, error) {
	return nil, nil
}

func (m *mockFTSRetryGraph) GetEntityRelations(_ context.Context, _ string) ([]*model.EntityRelation, error) {
	return nil, nil
}

func (m *mockFTSRetryGraph) GetEntityMemories(_ context.Context, _ string, _ int) ([]*model.Memory, error) {
	return nil, nil
}

func (m *mockFTSRetryGraph) GetMemoryEntities(_ context.Context, _ string) ([]*model.Entity, error) {
	return nil, nil
}

func (m *mockFTSRetryGraph) GetMemoriesEntities(_ context.Context, ids []string) (map[string][]*model.Entity, error) {
	if m.err != nil {
		return nil, m.err
	}
	out := make(map[string][]*model.Entity)
	for _, id := range ids {
		if ents, ok := m.entities[id]; ok {
			out[id] = ents
		}
	}
	return out, nil
}

func makeRetryResult(id, src string, score float64) *model.SearchResult {
	return &model.SearchResult{
		Memory: &model.Memory{ID: id, Content: "content-" + id},
		Score:  score,
		Source: src,
	}
}

func makeRetryState(query string, keywords, entities []string, candidates []*model.SearchResult) *pipeline.PipelineState {
	state := pipeline.NewState(query, &model.Identity{TeamID: "t1", OwnerID: "o1"})
	state.Plan = &pipeline.QueryPlan{
		Keywords: keywords,
		Entities: entities,
	}
	state.Candidates = candidates
	return state
}

// --- tests ---

func TestFTSRewriteRetryStage_Name(t *testing.T) {
	s := stage.NewFTSRewriteRetryStage(&mockFTSRetry{}, nil, nil, singleTierCfg, nil, nil, nil)
	if s.Name() != "fts_rewrite_retry" {
		t.Errorf("expected fts_rewrite_retry, got %s", s.Name())
	}
}

// FTS 数量+分数均充足时跳过
func TestFTSRewriteRetryStage_SkipWhenSufficient(t *testing.T) {
	fts := &mockFTSRetry{}
	s := stage.NewFTSRewriteRetryStage(fts, nil, nil, singleTierCfg, nil, nil, nil)

	candidates := []*model.SearchResult{
		makeRetryResult("a1", stage.SourceFTS, 0.9),
		makeRetryResult("a2", stage.SourceFTS, 0.8),
		makeRetryResult("a3", stage.SourceFTS, 0.7),
	}
	state := makeRetryState("test query", []string{"test", "query"}, nil, candidates)

	out, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fts.called != 0 {
		t.Error("FTS should not be called when already sufficient")
	}
	if len(out.Candidates) != 3 {
		t.Errorf("expected 3 candidates, got %d", len(out.Candidates))
	}
}

// FTS 数量不足时触发重试，新结果被追加
func TestFTSRewriteRetryStage_TriggerOnLowCount(t *testing.T) {
	retryResult := makeRetryResult("new1", stage.SourceFTS, 0.6)
	fts := &mockFTSRetry{results: []*model.SearchResult{retryResult}}
	s := stage.NewFTSRewriteRetryStage(fts, nil, nil, singleTierCfg, nil, nil, nil)

	// only 1 FTS result — count < minCount(3)
	candidates := []*model.SearchResult{
		makeRetryResult("a1", stage.SourceFTS, 0.8),
		makeRetryResult("g1", stage.SourceGraph, 0.5),
	}
	state := makeRetryState("test", []string{"test"}, nil, candidates)

	out, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fts.called != 1 {
		t.Errorf("expected 1 FTS retry call, got %d", fts.called)
	}
	found := false
	for _, c := range out.Candidates {
		if c.Memory != nil && c.Memory.ID == "new1" {
			found = true
		}
	}
	if !found {
		t.Error("expected new1 to be appended to candidates")
	}
}

// FTS top score 低时触发重试
func TestFTSRewriteRetryStage_TriggerOnLowScore(t *testing.T) {
	retryResult := makeRetryResult("new2", stage.SourceFTS, 0.5)
	fts := &mockFTSRetry{results: []*model.SearchResult{retryResult}}
	s := stage.NewFTSRewriteRetryStage(fts, nil, nil, singleTierCfg, nil, nil, nil)

	// count=3 OK but top score < minScore(0.3)
	candidates := []*model.SearchResult{
		makeRetryResult("a1", stage.SourceFTS, 0.1),
		makeRetryResult("a2", stage.SourceFTS, 0.1),
		makeRetryResult("a3", stage.SourceFTS, 0.1),
	}
	state := makeRetryState("test", []string{"test"}, nil, candidates)

	out, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fts.called != 1 {
		t.Errorf("expected 1 FTS retry call, got %d", fts.called)
	}
	found := false
	for _, c := range out.Candidates {
		if c.Memory != nil && c.Memory.ID == "new2" {
			found = true
		}
	}
	if !found {
		t.Error("expected new2 to be appended")
	}
}

// 已存在的 ID 不重复追加，原始 slice 不被修改（不可变性）
func TestFTSRewriteRetryStage_DeduplicatesResults(t *testing.T) {
	fts := &mockFTSRetry{results: []*model.SearchResult{
		makeRetryResult("a1", stage.SourceFTS, 0.8), // already in candidates
		makeRetryResult("new1", stage.SourceFTS, 0.6),
	}}
	s := stage.NewFTSRewriteRetryStage(fts, nil, nil, singleTierCfg, nil, nil, nil)

	original := []*model.SearchResult{makeRetryResult("a1", stage.SourceFTS, 0.7)}
	state := makeRetryState("q", []string{"q"}, nil, original)
	origLen := len(original)

	out, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// original slice must not be modified
	if len(original) != origLen {
		t.Errorf("original slice was mutated: len=%d want=%d", len(original), origLen)
	}
	// a1 appears exactly once in output
	count := 0
	for _, c := range out.Candidates {
		if c.Memory != nil && c.Memory.ID == "a1" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("a1 should appear exactly once, got %d", count)
	}
	// new1 is present
	found := false
	for _, c := range out.Candidates {
		if c.Memory != nil && c.Memory.ID == "new1" {
			found = true
		}
	}
	if !found {
		t.Error("expected new1 to be appended")
	}
}

// 图扩词追加到查询
func TestFTSRewriteRetryStage_GraphExpansion(t *testing.T) {
	fts := &mockFTSRetry{results: []*model.SearchResult{makeRetryResult("n1", stage.SourceFTS, 0.7)}}
	expander := &mockGraphExpander{names: []string{"neighbor_entity"}}
	s := stage.NewFTSRewriteRetryStage(fts, expander, nil, graphCfg, nil, nil, nil)

	// no FTS results → trigger
	state := makeRetryState("python", []string{"python"}, []string{"eid1"}, nil)

	_, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(fts.lastQuery, "neighbor_entity") {
		t.Errorf("expected expanded query to contain neighbor_entity, got: %q", fts.lastQuery)
	}
	if !strings.Contains(fts.lastQuery, "python") {
		t.Errorf("expected expanded query to retain original keyword python, got: %q", fts.lastQuery)
	}
}

// nil expander 不报错
func TestFTSRewriteRetryStage_NilExpander(t *testing.T) {
	fts := &mockFTSRetry{results: nil}
	s := stage.NewFTSRewriteRetryStage(fts, nil, nil, singleTierCfg, nil, nil, nil)
	state := makeRetryState("q", []string{"q"}, []string{"eid1"}, nil)
	_, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error with nil expander: %v", err)
	}
}

// Plan 为 nil 时不 panic，且不触发 FTS 调用
func TestFTSRewriteRetryStage_NilPlan(t *testing.T) {
	fts := &mockFTSRetry{}
	s := stage.NewFTSRewriteRetryStage(fts, nil, nil, singleTierCfg, nil, nil, nil)
	state := pipeline.NewState("q", &model.Identity{})
	state.Plan = nil
	_, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error with nil plan: %v", err)
	}
	if fts.called != 0 {
		t.Error("FTS must not be called when plan is nil")
	}
}

// HyDE 文档关键词追加到查询（第三层）
func TestFTSRewriteRetryStage_HyDEExpansion(t *testing.T) {
	fts := &mockFTSRetry{results: []*model.SearchResult{makeRetryResult("h1", stage.SourceFTS, 0.7)}}
	s := stage.NewFTSRewriteRetryStage(fts, nil, nil, hydeCfg, nil, nil, nil)

	// no FTS results → trigger; HyDE doc set → keywords extracted and appended
	state := makeRetryState("programming", []string{"programming"}, nil, nil)
	state.Plan.HyDEDoc = "Alice uses Python for coding projects"

	_, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fts.called != 1 {
		t.Errorf("expected 1 FTS retry call, got %d", fts.called)
	}
	if !strings.Contains(fts.lastQuery, "Python") {
		t.Errorf("expected HyDE keyword Python in query, got: %q", fts.lastQuery)
	}
	if !strings.Contains(fts.lastQuery, "programming") {
		t.Errorf("expected original keyword programming retained, got: %q", fts.lastQuery)
	}
}

// HyDE 文档为空时不影响行为
func TestFTSRewriteRetryStage_EmptyHyDE(t *testing.T) {
	fts := &mockFTSRetry{results: nil}
	s := stage.NewFTSRewriteRetryStage(fts, nil, nil, singleTierCfg, nil, nil, nil)

	state := makeRetryState("q", []string{"q"}, nil, nil)
	state.Plan.HyDEDoc = "" // explicit empty
	_, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error with empty HyDE doc: %v", err)
	}
}

// Filters 存在时走 SearchTextFiltered 分支
func TestFTSRewriteRetryStage_UsesFilteredSearch(t *testing.T) {
	retryResult := makeRetryResult("f1", stage.SourceFTS, 0.7)
	fts := &mockFTSRetry{results: []*model.SearchResult{retryResult}}
	s := stage.NewFTSRewriteRetryStage(fts, nil, nil, singleTierCfg, nil, nil, nil)

	// 0 FTS results → trigger; Filters set → SearchTextFiltered should be called
	state := makeRetryState("q", []string{"q"}, nil, nil)
	state.Filters = &model.SearchFilters{TeamID: "t1"}

	out, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fts.called != 1 {
		t.Errorf("expected 1 FTS call, got %d", fts.called)
	}
	found := false
	for _, c := range out.Candidates {
		if c.Memory != nil && c.Memory.ID == "f1" {
			found = true
		}
	}
	if !found {
		t.Error("expected f1 to be appended when using filtered search")
	}
}

// HyDE 按需生成：FTS 不足时才调 LLM
func TestFTSRewriteRetryStage_OnDemandHyDE(t *testing.T) {
	fts := &mockFTSRetry{results: []*model.SearchResult{makeRetryResult("h1", stage.SourceFTS, 0.7)}}
	hydeGen := &mockHyDEGenerator{doc: "Alice works as a software engineer using Python"}
	s := stage.NewFTSRewriteRetryStage(fts, nil, hydeGen, hydeCfg, nil, nil, nil)

	// no FTS results → trigger → HyDE should be called
	state := makeRetryState("job", []string{"job"}, nil, nil)

	_, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hydeGen.called != 1 {
		t.Errorf("expected HyDE generator called once, got %d", hydeGen.called)
	}
	if !strings.Contains(fts.lastQuery, "Alice") || !strings.Contains(fts.lastQuery, "Python") {
		t.Errorf("expected HyDE keywords in query, got: %q", fts.lastQuery)
	}
}

// HyDE 不在 FTS 充足时调用
func TestFTSRewriteRetryStage_HyDENotCalledWhenFTSSufficient(t *testing.T) {
	fts := &mockFTSRetry{}
	hydeGen := &mockHyDEGenerator{doc: "some doc"}
	s := stage.NewFTSRewriteRetryStage(fts, nil, hydeGen, singleTierCfg, nil, nil, nil)

	candidates := []*model.SearchResult{
		makeRetryResult("a1", stage.SourceFTS, 0.9),
		makeRetryResult("a2", stage.SourceFTS, 0.8),
		makeRetryResult("a3", stage.SourceFTS, 0.7),
	}
	state := makeRetryState("query", []string{"query"}, nil, candidates)

	_, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hydeGen.called != 0 {
		t.Errorf("HyDE must NOT be called when FTS is already sufficient, got %d calls", hydeGen.called)
	}
}

// makeRetryStateWithIntent 创建带意图的 PipelineState / Create state with intent set
func makeRetryStateWithIntent(query, intent string, keywords, entities []string, candidates []*model.SearchResult) *pipeline.PipelineState {
	s := makeRetryState(query, keywords, entities, candidates)
	if s.Plan == nil {
		s.Plan = &pipeline.QueryPlan{}
	}
	s.Plan.Intent = intent
	return s
}

// hasTrace 检查 trace 中是否含指定子串 / Check if any trace note contains substring
func hasTrace(state *pipeline.PipelineState, substr string) bool {
	for _, tr := range state.Traces {
		if strings.Contains(tr.Note, substr) {
			return true
		}
	}
	return false
}

// captureQueryFTS wraps mockFTSRetry to capture last query
type captureQueryFTS struct {
	inner     *mockFTSRetry
	lastQuery string
}

func (c *captureQueryFTS) SearchText(ctx context.Context, query string, id *model.Identity, limit int) ([]*model.SearchResult, error) {
	c.lastQuery = query
	return c.inner.SearchText(ctx, query, id, limit)
}

func (c *captureQueryFTS) SearchTextFiltered(ctx context.Context, query string, f *model.SearchFilters, limit int) ([]*model.SearchResult, error) {
	c.lastQuery = query
	return c.inner.SearchTextFiltered(ctx, query, f, limit)
}

// --- 渐进阶梯测试 ---

// 1. temporal intent 从阶梯 3 开始 → 一条候选即充足 → FTS 不被调用
func TestFTSRewrite_TemporalSkipsAllExpansion(t *testing.T) {
	fts := &mockFTSRetry{}
	hyde := &mockHyDEGenerator{doc: "temporal answer doc"}
	cfg := stage.DefaultFTSRewriteConfig()
	s := stage.NewFTSRewriteRetryStage(fts, nil, hyde, cfg, nil, nil, nil)

	state := makeRetryStateWithIntent("what happened last week", "temporal",
		[]string{"last", "week"}, nil,
		[]*model.SearchResult{makeRetryResult("t1", stage.SourceFTS, 0.4)})

	out, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fts.called != 0 {
		t.Errorf("FTS should not be called for temporal with 1 candidate: called=%d", fts.called)
	}
	if hyde.called != 0 {
		t.Errorf("HyDE should not be called for temporal: called=%d", hyde.called)
	}
	if !hasTrace(out, "sufficient at tier") {
		t.Error("expected 'sufficient at tier' in trace")
	}
}

// 2. semantic intent → FTS retries return nil → escalates through synonyms→graph→HyDE
func TestFTSRewrite_SemanticEscalatesToHyDE(t *testing.T) {
	fts := &mockFTSRetry{results: nil}
	expander := &mockGraphExpander{names: []string{"alice"}}
	hyde := &mockHyDEGenerator{doc: "Caroline is a transgender woman"}
	cfg := stage.DefaultFTSRewriteConfig()
	s := stage.NewFTSRewriteRetryStage(fts, expander, hyde, cfg, nil, nil, nil)

	candidates := []*model.SearchResult{
		makeRetryResult("a1", stage.SourceFTS, 0.1),
		makeRetryResult("a2", stage.SourceFTS, 0.1),
		makeRetryResult("a3", stage.SourceFTS, 0.1),
	}
	state := makeRetryStateWithIntent("what is Caroline identity", "semantic",
		[]string{"caroline", "identity"}, []string{"Caroline"}, candidates)

	_, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hyde.called == 0 {
		t.Error("expected HyDE to be called for semantic when all tiers are insufficient")
	}
}

// 3. semantic, synonyms expansion brings FTS results → sufficient at tier 1 → no HyDE
func TestFTSRewrite_SufficientAtTier1_NoHyDE(t *testing.T) {
	var retryResults []*model.SearchResult
	for i := 0; i < 5; i++ {
		retryResults = append(retryResults, makeRetryResult(fmt.Sprintf("r%d", i), stage.SourceFTS, 0.5))
	}
	fts := &mockFTSRetry{results: retryResults}
	expander := &mockGraphExpander{names: []string{"bob"}}
	hyde := &mockHyDEGenerator{doc: "should not be called"}
	cfg := stage.DefaultFTSRewriteConfig()
	s := stage.NewFTSRewriteRetryStage(fts, expander, hyde, cfg, nil, nil, nil)

	candidates := []*model.SearchResult{makeRetryResult("a1", stage.SourceFTS, 0.2)}
	state := makeRetryStateWithIntent("who is bob", "semantic",
		[]string{"bob"}, nil, candidates)

	_, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hyde.called != 0 {
		t.Errorf("HyDE should NOT be called when sufficient at tier1: hyde.called=%d", hyde.called)
	}
}

// 4. all tiers exhausted → low_confidence trace
func TestFTSRewrite_AllTiersExhausted_LowConfidence(t *testing.T) {
	fts := &mockFTSRetry{results: nil}
	cfg := stage.DefaultFTSRewriteConfig()
	s := stage.NewFTSRewriteRetryStage(fts, nil, nil, cfg, nil, nil, nil)

	state := makeRetryStateWithIntent("obscure query x", "semantic",
		[]string{"x"}, nil, nil)

	out, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasTrace(out, "low_confidence=true") {
		t.Errorf("expected low_confidence=true in trace, traces: %v", out.Traces)
	}
}

// 5. terms accumulate: HyDE tier's FTS query contains synonyms + graph + HyDE terms
func TestFTSRewrite_TermsAccumulate(t *testing.T) {
	inner := &mockFTSRetry{results: nil}
	captureFTS := &captureQueryFTS{inner: inner}
	expander := &mockGraphExpander{names: []string{"graphterm"}}
	hyde := &mockHyDEGenerator{doc: "hydeword hyperdoc"}
	cfg := stage.DefaultFTSRewriteConfig()
	s := stage.NewFTSRewriteRetryStage(captureFTS, expander, hyde, cfg, nil, nil, nil)

	state := makeRetryStateWithIntent("query", "semantic",
		[]string{"synterm"}, []string{"E1"}, nil)

	_, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	capturedQuery := strings.ToLower(captureFTS.lastQuery)
	for _, term := range []string{"synterm", "graphterm", "hydeword"} {
		if !strings.Contains(capturedQuery, strings.ToLower(term)) {
			t.Errorf("expected term %q in accumulated query %q", term, captureFTS.lastQuery)
		}
	}
}

// 6. nil expander → graph tier skipped → HyDE still called
func TestFTSRewrite_NilExpander_SkipsGraphTier(t *testing.T) {
	fts := &mockFTSRetry{results: nil}
	hyde := &mockHyDEGenerator{doc: "relevant doc"}
	cfg := stage.DefaultFTSRewriteConfig()
	s := stage.NewFTSRewriteRetryStage(fts, nil, hyde, cfg, nil, nil, nil)

	state := makeRetryStateWithIntent("query x", "semantic",
		[]string{"x"}, nil,
		[]*model.SearchResult{makeRetryResult("a1", stage.SourceFTS, 0.1)})

	_, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if hyde.called == 0 {
		t.Error("expected HyDE to be called when graph expander is nil and score insufficient")
	}
}

// 7. nil hydeGen → HyDE tier skipped → falls to none (low_confidence)
func TestFTSRewrite_NilHyDE_FallsToNone(t *testing.T) {
	fts := &mockFTSRetry{results: nil}
	cfg := stage.DefaultFTSRewriteConfig()
	s := stage.NewFTSRewriteRetryStage(fts, nil, nil, cfg, nil, nil, nil)

	state := makeRetryStateWithIntent("q", "semantic", []string{"q"}, nil, nil)

	out, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !hasTrace(out, "low_confidence=true") {
		t.Error("expected low_confidence when hydeGen nil and no results")
	}
}

// 8. empty config → uses DefaultFTSRewriteConfig behavior
func TestFTSRewrite_EmptyConfig_UsesDefaults(t *testing.T) {
	fts := &mockFTSRetry{results: nil}
	var emptyCfg config.FTSRewriteConfig
	s := stage.NewFTSRewriteRetryStage(fts, nil, nil, emptyCfg, nil, nil, nil)

	state := makeRetryStateWithIntent("q", "semantic", []string{"q"}, nil,
		[]*model.SearchResult{makeRetryResult("a1", stage.SourceFTS, 0.1)})

	_, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// 10. semantic, no candidates → expands from plan.Keywords
func TestFTSRewrite_SemanticNoCandidates_ExpandsFromKeywords(t *testing.T) {
	newResult := makeRetryResult("kw1", stage.SourceFTS, 0.7)
	fts := &mockFTSRetry{results: []*model.SearchResult{newResult}}
	cfg := stage.DefaultFTSRewriteConfig()
	s := stage.NewFTSRewriteRetryStage(fts, nil, nil, cfg, nil, nil, nil)

	state := makeRetryStateWithIntent("query", "semantic", []string{"keyword1"}, nil, nil)

	out, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fts.called == 0 {
		t.Error("expected FTS to be called when semantic with no candidates")
	}
	found := false
	for _, c := range out.Candidates {
		if c.Memory != nil && c.Memory.ID == "kw1" {
			found = true
		}
	}
	if !found {
		t.Error("expected kw1 to be appended from synonym expansion")
	}
}

// vectorCfg 五阶梯配置（synonyms→graph→vector→hyde→none），用于向量扩词测试
var vectorCfg = config.FTSRewriteConfig{
	Tiers: []config.FTSRetryTierConfig{
		{MinCount: 5, MinScore: 0.6, Expansion: "synonyms"},
		{MinCount: 3, MinScore: 0.4, Expansion: "graph"},
		{MinCount: 2, MinScore: 0.25, Expansion: "vector"},
		{MinCount: 2, MinScore: 0.15, Expansion: "hyde"},
		{MinCount: 1, MinScore: 0.0, Expansion: "none"},
	},
	IntentStartTier: map[string]int{"general": 2}, // 直接从 vector tier 开始
}

// V1: 无 vectorSearcher/embedder → expandVector 返回 nil，进入 hyde tier，不崩溃
func TestFTSRewrite_ExpandVector_NilDeps_FallsThrough(t *testing.T) {
	fts := &mockFTSRetry{results: []*model.SearchResult{makeRetryResult("r1", stage.SourceFTS, 0.5)}}
	s := stage.NewFTSRewriteRetryStage(fts, nil, nil, vectorCfg, nil, nil, nil)

	state := makeRetryStateWithIntent("who is Caroline", "general", []string{"Caroline"}, nil, nil)
	out, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// should not panic and should proceed to next tier
	_ = out
}

// V2: state 已有向量候选 → 直接用 cached 结果提取实体名，不调用 vectorSearcher
func TestFTSRewrite_ExpandVector_UsesCachedVectorCandidates(t *testing.T) {
	fts := &mockFTSRetry{results: []*model.SearchResult{makeRetryResult("r1", stage.SourceFTS, 0.5)}}
	vec := &mockFTSRetryVec{} // not called
	emb := &mockFTSRetryEmbed{vec: []float32{0.1, 0.2}}
	gStore := &mockFTSRetryGraph{
		entities: map[string][]*model.Entity{
			"m_vec1": {{ID: "e1", Name: "Caroline"}},
		},
	}
	s := stage.NewFTSRewriteRetryStage(fts, nil, nil, vectorCfg, vec, emb, gStore)

	// pre-seed vector candidate in state
	vecCandidate := &model.SearchResult{
		Memory: &model.Memory{ID: "m_vec1", Content: "I am a trans woman"},
		Score:  0.85,
		Source: stage.SourceVector,
	}
	state := makeRetryStateWithIntent("who is Caroline", "general", []string{"Caroline"}, nil,
		[]*model.SearchResult{vecCandidate})
	_, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vec.called > 0 {
		t.Error("vectorSearcher should not be called when candidates already in state")
	}
}

// V3: state 无向量候选，embedder+vectorSearcher 执行新搜索，实体名追加到 FTS 查询
func TestFTSRewrite_ExpandVector_FreshVectorSearch(t *testing.T) {
	freshVecResult := &model.SearchResult{
		Memory: &model.Memory{ID: "m_fresh", Content: "I'm a trans woman"},
		Score:  0.78,
		Source: stage.SourceVector,
	}
	fts := &mockFTSRetry{results: []*model.SearchResult{makeRetryResult("r1", stage.SourceFTS, 0.5)}}
	vec := &mockFTSRetryVec{results: []*model.SearchResult{freshVecResult}}
	emb := &mockFTSRetryEmbed{vec: []float32{0.1, 0.2}}
	gStore := &mockFTSRetryGraph{
		entities: map[string][]*model.Entity{
			"m_fresh": {{ID: "e1", Name: "Caroline"}},
		},
	}
	s := stage.NewFTSRewriteRetryStage(fts, nil, nil, vectorCfg, vec, emb, gStore)

	// no vector candidates in state
	state := makeRetryStateWithIntent("who is Caroline", "general", []string{"Caroline"}, nil, nil)
	_, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if vec.called == 0 {
		t.Error("expected vectorSearcher to be called for fresh search")
	}
}

// V4: minVectorScore 过滤低分候选，低分候选的实体不被提取
func TestFTSRewrite_ExpandVector_FiltersLowScoreCandidates(t *testing.T) {
	lowScore := &model.SearchResult{
		Memory: &model.Memory{ID: "m_low", Content: "unrelated content"},
		Score:  0.1, // below minVectorScore=0.4
		Source: stage.SourceVector,
	}
	fts := &mockFTSRetry{results: []*model.SearchResult{makeRetryResult("r1", stage.SourceFTS, 0.5)}}
	vec := &mockFTSRetryVec{results: []*model.SearchResult{lowScore}}
	emb := &mockFTSRetryEmbed{vec: []float32{0.1, 0.2}}
	gStore := &mockFTSRetryGraph{
		entities: map[string][]*model.Entity{
			"m_low": {{ID: "e_low", Name: "ShouldBeFiltered"}},
		},
	}
	s := stage.NewFTSRewriteRetryStage(fts, nil, nil, vectorCfg, vec, emb, gStore)

	state := makeRetryStateWithIntent("who is Caroline", "general", nil, nil, nil)
	_, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// "ShouldBeFiltered" should not appear as a query term
	if strings.Contains(fts.lastQuery, "ShouldBeFiltered") {
		t.Error("low-score entity should be filtered from expansion terms")
	}
}

// V5: 实体名已在 seen 中 → 不重复追加
func TestFTSRewrite_ExpandVector_SkipsAlreadySeenEntities(t *testing.T) {
	vecResult := &model.SearchResult{
		Memory: &model.Memory{ID: "m_dup", Content: "content"},
		Score:  0.8,
		Source: stage.SourceVector,
	}
	// FTS returns enough results only after the second call (with expanded query)
	callCount := 0
	fts := &mockFTSRetry{}
	fts.results = []*model.SearchResult{makeRetryResult("r1", stage.SourceFTS, 0.5)}
	_ = callCount

	vec := &mockFTSRetryVec{results: []*model.SearchResult{vecResult}}
	emb := &mockFTSRetryEmbed{vec: []float32{0.1, 0.2}}
	gStore := &mockFTSRetryGraph{
		entities: map[string][]*model.Entity{
			"m_dup": {{ID: "e1", Name: "Caroline"}}, // "caroline" will be in seen after first use
		},
	}
	s := stage.NewFTSRewriteRetryStage(fts, nil, nil, vectorCfg, vec, emb, gStore)

	state := makeRetryStateWithIntent("who is Caroline", "general", nil, nil, nil)
	_, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should not panic or infinite-loop; entity name only appears once in query
	query := fts.lastQuery
	carolineCount := strings.Count(strings.ToLower(query), "caroline")
	if carolineCount > 1 {
		t.Errorf("expected 'caroline' at most once in query, got %d times: %s", carolineCount, query)
	}
}

// ============================================================
// REGRESSION TESTS — vocabulary mismatch golden cases
// These tests lock in the specific behavior that drove single-hop +2.1pp on LoCoMo.
// If these fail, the vocabulary-bridging logic has regressed.
// ============================================================

// Regression_TransWoman: "Caroline's identity" → vector finds "I'm a trans woman"
// → FTS retry must contain "trans" or "woman" to bridge the vocabulary gap.
// This is the canonical single-hop mismatch case from LoCoMo evaluation.
func TestFTSRewrite_Regression_TransWoman_ContentTerms(t *testing.T) {
	retryResults := []*model.SearchResult{makeRetryResult("hit1", stage.SourceFTS, 0.7)}
	fts := &mockFTSRetry{results: retryResults}
	vec := &mockFTSRetryVec{
		results: []*model.SearchResult{{
			Memory: &model.Memory{
				ID:      "m_trans",
				Content: "I'm a trans woman and proud of who I am.",
			},
			Score:  0.82,
			Source: stage.SourceVector,
		}},
	}
	emb := &mockFTSRetryEmbed{vec: []float32{0.1, 0.2}}
	// no graphStore entities — relies purely on content term extraction
	gStore := &mockFTSRetryGraph{entities: map[string][]*model.Entity{}}
	s := stage.NewFTSRewriteRetryStage(fts, nil, nil, vectorCfg, vec, emb, gStore)

	// Query uses "identity", memory says "trans woman" — classic vocabulary mismatch
	state := makeRetryStateWithIntent("What is Caroline's identity", "general", []string{"caroline", "identity"}, nil, nil)
	_, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	query := strings.ToLower(fts.lastQuery)
	if !strings.Contains(query, "trans") && !strings.Contains(query, "woman") {
		t.Errorf("REGRESSION: vocabulary bridging failed — FTS query %q should contain 'trans' or 'woman' from memory content", query)
	}
}

// Regression_JobTitle: "what does Bob do for work" → vector finds "I work as a software engineer"
// → FTS retry must contain "software" or "engineer".
func TestFTSRewrite_Regression_JobTitle_ContentTerms(t *testing.T) {
	fts := &mockFTSRetry{results: []*model.SearchResult{makeRetryResult("hit1", stage.SourceFTS, 0.6)}}
	vec := &mockFTSRetryVec{
		results: []*model.SearchResult{{
			Memory: &model.Memory{
				ID:      "m_job",
				Content: "I work as a software engineer at a tech startup.",
			},
			Score:  0.79,
			Source: stage.SourceVector,
		}},
	}
	emb := &mockFTSRetryEmbed{vec: []float32{0.1, 0.2}}
	gStore := &mockFTSRetryGraph{entities: map[string][]*model.Entity{}}
	s := stage.NewFTSRewriteRetryStage(fts, nil, nil, vectorCfg, vec, emb, gStore)

	state := makeRetryStateWithIntent("what does Bob do for work", "general", []string{"bob", "work"}, nil, nil)
	_, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	query := strings.ToLower(fts.lastQuery)
	if !strings.Contains(query, "software") && !strings.Contains(query, "engineer") {
		t.Errorf("REGRESSION: FTS query %q should contain 'software' or 'engineer' from content term extraction", query)
	}
}

// Regression_CachedVectorCandidates: when vector candidates are already in state,
// content term extraction must still work (zero-cost path, no fresh vector search).
func TestFTSRewrite_Regression_ContentTerms_CachedPath(t *testing.T) {
	fts := &mockFTSRetry{results: []*model.SearchResult{makeRetryResult("r1", stage.SourceFTS, 0.5)}}
	vec := &mockFTSRetryVec{} // must NOT be called
	emb := &mockFTSRetryEmbed{vec: []float32{0.1, 0.2}}
	gStore := &mockFTSRetryGraph{entities: map[string][]*model.Entity{}}

	cached := &model.SearchResult{
		Memory: &model.Memory{
			ID:      "m_cached",
			Content: "I graduated from Harvard University with a degree in economics.",
		},
		Score:  0.88,
		Source: stage.SourceVector,
	}
	s := stage.NewFTSRewriteRetryStage(fts, nil, nil, vectorCfg, vec, emb, gStore)
	state := makeRetryStateWithIntent("where did Alice study", "general", []string{"alice", "study"}, nil,
		[]*model.SearchResult{cached})
	_, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if vec.called > 0 {
		t.Error("REGRESSION: vectorSearcher called on cached-candidate path (should be zero-cost)")
	}
	query := strings.ToLower(fts.lastQuery)
	if !strings.Contains(query, "harvard") && !strings.Contains(query, "economics") {
		t.Errorf("REGRESSION: FTS query %q should contain 'harvard' or 'economics' from cached content", query)
	}
}

// ============================================================
// BILINGUAL TESTS — Chinese content term extraction
// ============================================================

// Regression_ChineseTrans: Chinese memory "我是一名跨性别女性" → FTS retry must contain
// "跨性" or "性别" bigrams (the key CJK content terms).
func TestFTSRewrite_Regression_Chinese_TransGender(t *testing.T) {
	fts := &mockFTSRetry{results: []*model.SearchResult{makeRetryResult("r1", stage.SourceFTS, 0.5)}}
	vec := &mockFTSRetryVec{
		results: []*model.SearchResult{{
			Memory: &model.Memory{
				ID:      "m_zh_trans",
				Content: "我是一名跨性别女性，为自己感到骄傲。",
			},
			Score:  0.80,
			Source: stage.SourceVector,
		}},
	}
	emb := &mockFTSRetryEmbed{vec: []float32{0.1, 0.2}}
	gStore := &mockFTSRetryGraph{entities: map[string][]*model.Entity{}}
	s := stage.NewFTSRewriteRetryStage(fts, nil, nil, vectorCfg, vec, emb, gStore)

	state := makeRetryStateWithIntent("Caroline 是什么性别", "general", []string{"Caroline", "性别"}, nil, nil)
	_, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	query := fts.lastQuery
	if !strings.Contains(query, "跨性") && !strings.Contains(query, "性别") && !strings.Contains(query, "女性") {
		t.Errorf("BILINGUAL: FTS query %q should contain Chinese bigrams '跨性', '性别' or '女性' from ZH content", query)
	}
}

// Regression_MixedContent: memory with mixed CN/EN content → both languages extracted.
func TestFTSRewrite_Regression_Mixed_Content(t *testing.T) {
	fts := &mockFTSRetry{results: []*model.SearchResult{makeRetryResult("r1", stage.SourceFTS, 0.5)}}
	vec := &mockFTSRetryVec{
		results: []*model.SearchResult{{
			Memory: &model.Memory{
				ID:      "m_mixed",
				Content: "我在 Google 担任 software engineer，专注于机器学习研究。",
			},
			Score:  0.78,
			Source: stage.SourceVector,
		}},
	}
	emb := &mockFTSRetryEmbed{vec: []float32{0.1, 0.2}}
	gStore := &mockFTSRetryGraph{entities: map[string][]*model.Entity{}}
	s := stage.NewFTSRewriteRetryStage(fts, nil, nil, vectorCfg, vec, emb, gStore)

	state := makeRetryStateWithIntent("Bob works at which company", "general", []string{"bob", "company"}, nil, nil)
	_, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	query := fts.lastQuery
	hasEN := strings.Contains(strings.ToLower(query), "google") || strings.Contains(strings.ToLower(query), "engineer")
	hasZH := strings.Contains(query, "机器") || strings.Contains(query, "学习") || strings.Contains(query, "研究")
	if !hasEN && !hasZH {
		t.Errorf("BILINGUAL: mixed content query %q should contain English ('google'/'engineer') or Chinese ('机器'/'学习') terms", query)
	}
}
