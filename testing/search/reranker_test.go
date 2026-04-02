package search_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"

	"iclude/internal/config"
	"iclude/internal/model"
	"iclude/internal/search"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func makeRerankResult(id string, score float64, content string) *model.SearchResult {
	return &model.SearchResult{
		Memory: &model.Memory{
			ID:      id,
			Content: content,
		},
		Score:  score,
		Source: "hybrid",
	}
}

func TestOverlapReranker_ReordersTopKByQueryOverlap(t *testing.T) {
	reranker := search.NewReranker(config.RerankConfig{
		Enabled:     true,
		Provider:    "overlap",
		TopK:        3,
		ScoreWeight: 0.9,
	})
	require.NotNil(t, reranker)

	results := []*model.SearchResult{
		makeRerankResult("a", 0.90, "部署在阿里云 ECS 上海区域"),
		makeRerankResult("b", 0.80, "项目数据库从 PostgreSQL 迁移到了 SQLite"),
		makeRerankResult("c", 0.70, "团队每周二开技术周会"),
	}

	out := reranker.Rerank(context.Background(), "数据库迁移", results)
	require.Len(t, out, 3)
	assert.Equal(t, "b", out[0].Memory.ID)
	assert.Equal(t, "a", out[1].Memory.ID)
	assert.Equal(t, "c", out[2].Memory.ID)
}

func TestOverlapReranker_OnlyReordersConfiguredTopK(t *testing.T) {
	reranker := search.NewReranker(config.RerankConfig{
		Enabled:     true,
		Provider:    "overlap",
		TopK:        2,
		ScoreWeight: 0.9,
	})
	require.NotNil(t, reranker)

	results := []*model.SearchResult{
		makeRerankResult("a", 0.90, "部署在阿里云 ECS 上海区域"),
		makeRerankResult("b", 0.80, "项目数据库从 PostgreSQL 迁移到了 SQLite"),
		makeRerankResult("c", 0.70, "数据库迁移决定采用版本号递增方案"),
	}

	out := reranker.Rerank(context.Background(), "数据库迁移", results)
	require.Len(t, out, 3)
	assert.Equal(t, "b", out[0].Memory.ID)
	assert.Equal(t, "a", out[1].Memory.ID)
	assert.Equal(t, "c", out[2].Memory.ID)
}

func TestNewReranker_UnsupportedProviderReturnsNil(t *testing.T) {
	reranker := search.NewReranker(config.RerankConfig{
		Enabled:  true,
		Provider: "unknown",
	})
	assert.Nil(t, reranker)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestRemoteReranker_ReordersResults(t *testing.T) {
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			assert.Equal(t, "/rerank", r.URL.Path)
			assert.Equal(t, "Bearer secret", r.Header.Get("Authorization"))

			var req map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
			assert.Equal(t, "rerank-v1", req["model"])
			assert.Equal(t, "数据库迁移", req["query"])

			body, err := json.Marshal(map[string]any{
				"results": []map[string]any{
					{"index": 1, "relevance_score": 0.95},
					{"index": 0, "relevance_score": 0.40},
				},
			})
			require.NoError(t, err)

			return &http.Response{
				StatusCode: http.StatusOK,
				Header:     make(http.Header),
				Body:       io.NopCloser(strings.NewReader(string(body))),
			}, nil
		}),
	}

	reranker := search.NewRemoteRerankerWithClient(config.RerankConfig{
		Enabled:     true,
		Provider:    "remote",
		BaseURL:     "http://rerank.local",
		APIKey:      "secret",
		Model:       "rerank-v1",
		TopK:        2,
		ScoreWeight: 1.0,
	}, client)
	require.NotNil(t, reranker)

	results := []*model.SearchResult{
		makeRerankResult("a", 0.90, "部署在阿里云 ECS 上海区域"),
		makeRerankResult("b", 0.80, "项目数据库从 PostgreSQL 迁移到了 SQLite"),
		makeRerankResult("c", 0.70, "团队每周二开技术周会"),
	}

	out := reranker.Rerank(context.Background(), "数据库迁移", results)
	require.Len(t, out, 3)
	assert.Equal(t, "b", out[0].Memory.ID)
	assert.Equal(t, "a", out[1].Memory.ID)
	assert.Equal(t, "c", out[2].Memory.ID)
}

func TestRemoteReranker_FallbackOnError(t *testing.T) {
	client := &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			return nil, errors.New("rate limited")
		}),
	}

	reranker := search.NewRemoteRerankerWithClient(config.RerankConfig{
		Enabled:     true,
		Provider:    "remote",
		BaseURL:     "http://rerank.local",
		TopK:        2,
		ScoreWeight: 1.0,
	}, client)
	require.NotNil(t, reranker)

	results := []*model.SearchResult{
		makeRerankResult("a", 0.90, "部署在阿里云 ECS 上海区域"),
		makeRerankResult("b", 0.80, "项目数据库从 PostgreSQL 迁移到了 SQLite"),
	}

	out := reranker.Rerank(context.Background(), "数据库迁移", results)
	require.Len(t, out, 2)
	assert.Equal(t, "a", out[0].Memory.ID)
	assert.Equal(t, "b", out[1].Memory.ID)
}
