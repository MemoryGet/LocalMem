package stage_test

import (
	"context"
	"testing"
	"time"

	"iclude/internal/model"
	"iclude/internal/search/pipeline"
	"iclude/internal/search/stage"
)

// makeTFResult 构造带时间戳的候选 / Build a candidate with HappenedAt timestamp
func makeTFResult(id string, score float64, happenedAt time.Time) *model.SearchResult {
	ts := happenedAt
	return &model.SearchResult{
		Memory: &model.Memory{ID: id, Content: id, HappenedAt: &ts},
		Score:  score,
		Source: "fts",
	}
}

// makeTFState 构造带时间锚的 state / Build state with an anchored temporal plan
func makeTFState(center time.Time, rangeD time.Duration, candidates []*model.SearchResult) *pipeline.PipelineState {
	st := pipeline.NewState("temporal query", &model.Identity{TeamID: "t", OwnerID: "o"})
	st.Plan = &pipeline.QueryPlan{
		Temporal:       true,
		TemporalAnchor: true,
		TemporalCenter: &center,
		TemporalRange:  rangeD,
	}
	st.Candidates = candidates
	return st
}

func tfIDs(results []*model.SearchResult) []string {
	out := make([]string, 0, len(results))
	for _, r := range results {
		out = append(out, r.Memory.ID)
	}
	return out
}

func TestTemporalFilterStage_Name(t *testing.T) {
	s := stage.NewTemporalFilterStage(3)
	if s.Name() != "temporal_filter" {
		t.Errorf("Name() = %q, want temporal_filter", s.Name())
	}
}

// 无时间锚 → 跳过，顺序不变
func TestTemporalFilterStage_NoAnchorSkips(t *testing.T) {
	now := time.Now()
	cands := []*model.SearchResult{
		makeTFResult("a", 0.5, now),
		makeTFResult("b", 0.9, now),
	}
	s := stage.NewTemporalFilterStage(3)
	st := pipeline.NewState("q", &model.Identity{TeamID: "t", OwnerID: "o"})
	st.Plan = &pipeline.QueryPlan{Temporal: false} // no anchor
	st.Candidates = cands

	out, err := s.Execute(context.Background(), st)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := tfIDs(out.Candidates); got[0] != "a" || got[1] != "b" {
		t.Errorf("order changed when no anchor: got %v", got)
	}
}

// tier 优先于 score：近距离低分候选排在远距离高分候选之前
func TestTemporalFilterStage_TierBeatsScore(t *testing.T) {
	center := time.Now()
	rangeD := 10 * 24 * time.Hour // innerThreshold = 5 days

	near := makeTFResult("near", 0.3, center.Add(-1*24*time.Hour))
	far := makeTFResult("far", 0.9, center.Add(-8*24*time.Hour))

	s := stage.NewTemporalFilterStage(3)
	st := makeTFState(center, rangeD, []*model.SearchResult{far, near}) // far first in input

	out, err := s.Execute(context.Background(), st)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := tfIDs(out.Candidates)
	if got[0] != "near" {
		t.Errorf("tier0 (near) should rank first despite lower score: got %v", got)
	}
}

// 同 tier 内按原始 score 降序
func TestTemporalFilterStage_SameTierByScore(t *testing.T) {
	center := time.Now()
	rangeD := 10 * 24 * time.Hour // innerThreshold = 5 days

	lo := makeTFResult("lo", 0.4, center.Add(-1*24*time.Hour))
	hi := makeTFResult("hi", 0.8, center.Add(-2*24*time.Hour))

	s := stage.NewTemporalFilterStage(3)
	st := makeTFState(center, rangeD, []*model.SearchResult{lo, hi}) // lo first in input

	out, err := s.Execute(context.Background(), st)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := tfIDs(out.Candidates)
	if got[0] != "hi" {
		t.Errorf("same tier should sort by score desc: got %v", got)
	}
}

// 原始 score 不被修改
func TestTemporalFilterStage_ScoreUnchanged(t *testing.T) {
	center := time.Now()
	rangeD := 10 * 24 * time.Hour
	c := makeTFResult("x", 0.7, center.Add(-1*24*time.Hour))
	s := stage.NewTemporalFilterStage(3)
	st := makeTFState(center, rangeD, []*model.SearchResult{
		c,
		makeTFResult("y", 0.6, center.Add(-2*24*time.Hour)),
		makeTFResult("z", 0.5, center.Add(-3*24*time.Hour)),
	})

	out, err := s.Execute(context.Background(), st)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, r := range out.Candidates {
		if r.Memory.ID == "x" && r.Score != 0.7 {
			t.Errorf("score mutated: x = %f, want 0.7", r.Score)
		}
	}
}

// 边界精确：delta == innerThreshold 归 tier0；delta == rangeD 归 tier1
func TestTemporalFilterStage_Boundaries(t *testing.T) {
	center := time.Now()
	rangeD := 10 * 24 * time.Hour // innerThreshold = 5 days
	innerT := 5 * 24 * time.Hour

	onInner := makeTFResult("onInner", 0.5, center.Add(-innerT)) // delta == innerThreshold → tier0
	onOuter := makeTFResult("onOuter", 0.9, center.Add(-rangeD)) // delta == rangeD → tier1

	s := stage.NewTemporalFilterStage(3)
	st := makeTFState(center, rangeD, []*model.SearchResult{onOuter, onInner})

	out, err := s.Execute(context.Background(), st)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := tfIDs(out.Candidates)
	if got[0] != "onInner" {
		t.Errorf("delta==innerThreshold should be tier0 (first): got %v", got)
	}
	if got[1] != "onOuter" {
		t.Errorf("delta==rangeD should be tier1 (second): got %v", got)
	}
}

// 不可变：传入 slice 不被原地修改
func TestTemporalFilterStage_Immutable(t *testing.T) {
	center := time.Now()
	rangeD := 10 * 24 * time.Hour
	far := makeTFResult("far", 0.9, center.Add(-8*24*time.Hour))
	near := makeTFResult("near", 0.3, center.Add(-1*24*time.Hour))
	input := []*model.SearchResult{far, near}

	s := stage.NewTemporalFilterStage(3)
	st := makeTFState(center, rangeD, input)

	_, err := s.Execute(context.Background(), st)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if input[0].Memory.ID != "far" || input[1].Memory.ID != "near" {
		t.Errorf("input slice was mutated in place: %v", tfIDs(input))
	}
}

// WithInnerRatio(0) 禁用重排：只过滤，顺序保持过滤后原样
func TestTemporalFilterStage_DisableRerank(t *testing.T) {
	center := time.Now()
	rangeD := 10 * 24 * time.Hour
	far := makeTFResult("far", 0.9, center.Add(-8*24*time.Hour))
	near := makeTFResult("near", 0.3, center.Add(-1*24*time.Hour))

	s := stage.NewTemporalFilterStage(3, stage.WithInnerRatio(0))
	st := makeTFState(center, rangeD, []*model.SearchResult{far, near})

	out, err := s.Execute(context.Background(), st)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got := tfIDs(out.Candidates)
	if got[0] != "far" {
		t.Errorf("WithInnerRatio(0) should preserve order: got %v", got)
	}
}

// 候选含 nil 条目时不 panic，nil 被安全跳过
func TestTemporalFilterStage_NilCandidateSkipped(t *testing.T) {
	center := time.Now()
	rangeD := 10 * 24 * time.Hour
	cands := []*model.SearchResult{
		makeTFResult("a", 0.5, center.Add(-1*24*time.Hour)),
		nil,
		{Memory: nil, Score: 0.9},
		makeTFResult("b", 0.4, center.Add(-2*24*time.Hour)),
	}
	s := stage.NewTemporalFilterStage(3)
	st := makeTFState(center, rangeD, cands)

	out, err := s.Execute(context.Background(), st)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, r := range out.Candidates {
		if r == nil || r.Memory == nil {
			t.Error("nil candidate leaked through")
		}
	}
}

// 硬过滤被跳过（候选 < minAfterFilter）→ 全部重排，tier2 窗外候选排末尾
func TestTemporalFilterStage_FilterSkippedTier2Last(t *testing.T) {
	center := time.Now()
	rangeD := 10 * 24 * time.Hour

	inWindow := makeTFResult("inWindow", 0.5, center.Add(-1*24*time.Hour))      // tier0
	outWindow := makeTFResult("outWindow", 0.9, center.Add(-100*24*time.Hour)) // tier2

	s := stage.NewTemporalFilterStage(3)
	st := makeTFState(center, rangeD, []*model.SearchResult{outWindow, inWindow})

	out, err := s.Execute(context.Background(), st)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(out.Candidates) != 2 {
		t.Fatalf("expected both candidates kept (filter skipped), got %d", len(out.Candidates))
	}
	got := tfIDs(out.Candidates)
	if got[0] != "inWindow" || got[1] != "outWindow" {
		t.Errorf("tier0 should precede tier2 when filter skipped: got %v", got)
	}
}
