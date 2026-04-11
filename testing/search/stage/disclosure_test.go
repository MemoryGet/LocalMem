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

func TestDisclosureStage_Name(t *testing.T) {
	s := stage.NewDisclosureStage(config.DisclosureConfig{}, 0)
	if s.Name() != "disclosure" {
		t.Errorf("Name() = %q, want %q", s.Name(), "disclosure")
	}
}

func TestDisclosureStage_Implements_Stage(t *testing.T) {
	var _ pipeline.Stage = stage.NewDisclosureStage(config.DisclosureConfig{}, 0)
}

func TestDisclosureStage_Execute(t *testing.T) {
	shortContent := "hello world"
	mediumContent := strings.Repeat("word ", 100) // ~100 tokens
	longContent := strings.Repeat("word ", 500)   // ~500 tokens

	tests := []struct {
		name            string
		cfg             config.DisclosureConfig
		maxTokens       int
		pipelineName    string
		candidates      []*model.SearchResult
		wantPipelines   int
		wantTotalUsedLE int  // total_used <= this value
		wantOverflow    bool // expect overflow items
	}{
		{
			name:         "disabled config passes through",
			cfg:          config.DisclosureConfig{Enabled: false},
			maxTokens:    2000,
			pipelineName: "exploration",
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "a", Content: shortContent}, Score: 0.9},
			},
			wantPipelines: 0, // no disclosure metadata
		},
		{
			name:         "empty candidates",
			cfg:          config.DisclosureConfig{Enabled: true, CoreWeight: 0.4, ContextWeight: 0.25, EntityWeight: 0.2, TimelineWeight: 0.15},
			maxTokens:    2000,
			pipelineName: "exploration",
			candidates:   nil,
			wantPipelines: 0,
		},
		{
			name:         "all fit within budget",
			cfg:          config.DisclosureConfig{Enabled: true, CoreWeight: 0.4, ContextWeight: 0.25, EntityWeight: 0.2, TimelineWeight: 0.15},
			maxTokens:    2000,
			pipelineName: "",
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "a", Content: shortContent}, Score: 0.9},
				{Memory: &model.Memory{ID: "b", Content: shortContent}, Score: 0.8},
			},
			wantPipelines:   4,
			wantTotalUsedLE: 2000,
		},
		{
			name:         "overflow when budget exhausted",
			cfg:          config.DisclosureConfig{Enabled: true, CoreWeight: 0.4, ContextWeight: 0.25, EntityWeight: 0.2, TimelineWeight: 0.15},
			maxTokens:    50,
			pipelineName: "",
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "a", Content: longContent}, Score: 0.9},
				{Memory: &model.Memory{ID: "b", Content: longContent}, Score: 0.8},
				{Memory: &model.Memory{ID: "c", Content: longContent}, Score: 0.7},
			},
			wantPipelines:   4,
			wantTotalUsedLE: 50,
			wantOverflow:    true,
		},
		{
			name:         "summary fallback when content too large",
			cfg:          config.DisclosureConfig{Enabled: true, CoreWeight: 0.5, ContextWeight: 0.2, EntityWeight: 0.2, TimelineWeight: 0.1},
			maxTokens:    80,
			pipelineName: "",
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "a", Content: mediumContent, Summary: "brief summary"}, Score: 0.9},
			},
			wantPipelines:   4,
			wantTotalUsedLE: 80,
		},
		{
			name:         "nil memory skipped",
			cfg:          config.DisclosureConfig{Enabled: true, CoreWeight: 0.4, ContextWeight: 0.25, EntityWeight: 0.2, TimelineWeight: 0.15},
			maxTokens:    2000,
			pipelineName: "",
			candidates: []*model.SearchResult{
				{Memory: nil, Score: 0.9},
				{Memory: &model.Memory{ID: "b", Content: shortContent}, Score: 0.8},
			},
			wantPipelines:   4,
			wantTotalUsedLE: 2000,
		},
		{
			name:         "precision strategy adjusts weights",
			cfg:          config.DisclosureConfig{Enabled: true, CoreWeight: 0.4, ContextWeight: 0.25, EntityWeight: 0.2, TimelineWeight: 0.15},
			maxTokens:    2000,
			pipelineName: "precision",
			candidates: []*model.SearchResult{
				{Memory: &model.Memory{ID: "a", Content: shortContent}, Score: 0.9},
			},
			wantPipelines:   4,
			wantTotalUsedLE: 2000,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := stage.NewDisclosureStage(tt.cfg, tt.maxTokens)
			state := pipeline.NewState("test query", &model.Identity{TeamID: "t", OwnerID: "o"})
			state.Candidates = tt.candidates
			state.PipelineName = tt.pipelineName

			got, err := s.Execute(context.Background(), state)
			if err != nil {
				t.Fatalf("Execute() returned error: %v", err)
			}

			if tt.wantPipelines == 0 {
				// disabled or empty: no disclosure metadata
				if _, ok := got.Metadata["disclosure"]; ok && !tt.cfg.Enabled {
					t.Error("expected no disclosure metadata when disabled")
				}
				return
			}

			raw, ok := got.Metadata["disclosure"]
			if !ok {
				t.Fatal("expected disclosure metadata to be set")
			}
			result, ok := raw.(*model.DisclosureResult)
			if !ok {
				t.Fatal("disclosure metadata is not *model.DisclosureResult")
			}

			if len(result.Pipelines) != tt.wantPipelines {
				t.Errorf("Pipelines count = %d, want %d", len(result.Pipelines), tt.wantPipelines)
			}
			if result.TotalUsed > tt.wantTotalUsedLE {
				t.Errorf("TotalUsed = %d, want <= %d", result.TotalUsed, tt.wantTotalUsedLE)
			}
			if result.TotalBudget != tt.maxTokens {
				t.Errorf("TotalBudget = %d, want %d", result.TotalBudget, tt.maxTokens)
			}
			if tt.wantOverflow && len(result.Overflow) == 0 {
				t.Error("expected overflow items but got none")
			}
			if !tt.wantOverflow && len(result.Overflow) > 0 {
				t.Errorf("expected no overflow but got %d items", len(result.Overflow))
			}
		})
	}
}

func TestDisclosureStage_Execute_TenCandidates(t *testing.T) {
	cfg := config.DisclosureConfig{
		Enabled:        true,
		CoreWeight:     0.4,
		ContextWeight:  0.25,
		EntityWeight:   0.2,
		TimelineWeight: 0.15,
	}
	maxTokens := 2000

	// 创建 10 个不同长度的候选 / Create 10 candidates with varying content lengths
	candidates := make([]*model.SearchResult, 10)
	for i := 0; i < 10; i++ {
		contentLen := (i + 1) * 50 // 50, 100, ... 500 words → ~50..500 tokens
		content := strings.Repeat("word ", contentLen)
		candidates[i] = &model.SearchResult{
			Memory: &model.Memory{
				ID:      fmt.Sprintf("mem-%d", i),
				Content: content,
				Summary: fmt.Sprintf("summary for memory %d", i),
				Excerpt: fmt.Sprintf("excerpt %d", i),
			},
			Score:  1.0 - float64(i)*0.05,
			Source: "sqlite",
		}
	}

	s := stage.NewDisclosureStage(cfg, maxTokens)
	state := pipeline.NewState("test query", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = candidates

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	raw, ok := got.Metadata["disclosure"]
	if !ok {
		t.Fatal("expected disclosure metadata to be set")
	}
	result, ok := raw.(*model.DisclosureResult)
	if !ok {
		t.Fatal("disclosure metadata is not *model.DisclosureResult")
	}

	// 验证 4 条管线 / Verify 4 pipelines
	if len(result.Pipelines) != 4 {
		t.Errorf("Pipelines count = %d, want 4", len(result.Pipelines))
	}

	// 验证 token 预算不超标 / Verify total used <= budget
	if result.TotalUsed > maxTokens {
		t.Errorf("TotalUsed = %d, exceeds budget %d", result.TotalUsed, maxTokens)
	}

	// 验证管线名称 / Verify pipeline names
	wantNames := []string{"core", "context", "entity", "timeline"}
	for i, p := range result.Pipelines {
		if p.Name != wantNames[i] {
			t.Errorf("Pipeline[%d].Name = %q, want %q", i, p.Name, wantNames[i])
		}
	}

	// 验证每条管线的 UsedTokens 不超过其 Budget / Verify per-pipeline budget
	for _, p := range result.Pipelines {
		if p.UsedTokens > p.Budget {
			t.Errorf("Pipeline %q UsedTokens=%d exceeds Budget=%d", p.Name, p.UsedTokens, p.Budget)
		}
	}

	// 验证总预算 / Verify total budget
	if result.TotalBudget != maxTokens {
		t.Errorf("TotalBudget = %d, want %d", result.TotalBudget, maxTokens)
	}

	// 验证有 trace 记录 / Verify trace was recorded
	foundTrace := false
	for _, tr := range got.Traces {
		if tr.Name == "disclosure" {
			foundTrace = true
			if tr.InputCount != 10 {
				t.Errorf("Trace InputCount = %d, want 10", tr.InputCount)
			}
		}
	}
	if !foundTrace {
		t.Error("expected disclosure trace to be recorded")
	}
}

func TestDisclosureStage_Execute_DetailLevelFallback(t *testing.T) {
	cfg := config.DisclosureConfig{
		Enabled:        true,
		CoreWeight:     1.0,
		ContextWeight:  0.0,
		EntityWeight:   0.0,
		TimelineWeight: 0.0,
	}
	// 预算只够放 summary，不够放 full content / Budget enough for summary but not full content
	maxTokens := 20

	candidates := []*model.SearchResult{
		{
			Memory: &model.Memory{
				ID:      "a",
				Content: strings.Repeat("word ", 100), // ~100 tokens, too large
				Summary: "short summary",               // ~4 tokens, fits
				Excerpt: "ex",
			},
			Score: 0.9,
		},
	}

	s := stage.NewDisclosureStage(cfg, maxTokens)
	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = candidates

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	result := got.Metadata["disclosure"].(*model.DisclosureResult)
	if len(result.Overflow) != 0 {
		t.Errorf("expected no overflow, got %d items", len(result.Overflow))
	}

	// 核心管线应有 1 个 item，detail_level=summary / Core pipeline should have 1 item at summary level
	corePipe := result.Pipelines[0]
	if len(corePipe.Items) != 1 {
		t.Fatalf("core pipeline Items count = %d, want 1", len(corePipe.Items))
	}
	item := corePipe.Items[0]
	if item.DetailLevel != "summary" {
		t.Errorf("DetailLevel = %q, want %q", item.DetailLevel, "summary")
	}
	if item.Content != "short summary" {
		t.Errorf("Content = %q, want %q", item.Content, "short summary")
	}
}

func TestDisclosureStage_Execute_DefaultMaxTokens(t *testing.T) {
	// maxTokens=0 应使用默认值 4096 / maxTokens=0 should use default 4096
	s := stage.NewDisclosureStage(config.DisclosureConfig{Enabled: true, CoreWeight: 0.4, ContextWeight: 0.25, EntityWeight: 0.2, TimelineWeight: 0.15}, 0)

	state := pipeline.NewState("test", &model.Identity{TeamID: "t", OwnerID: "o"})
	state.Candidates = []*model.SearchResult{
		{Memory: &model.Memory{ID: "a", Content: "hello"}, Score: 0.9},
	}

	got, err := s.Execute(context.Background(), state)
	if err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	result := got.Metadata["disclosure"].(*model.DisclosureResult)
	if result.TotalBudget != 4096 {
		t.Errorf("TotalBudget = %d, want 4096 (default)", result.TotalBudget)
	}
}
