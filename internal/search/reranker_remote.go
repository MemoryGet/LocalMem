package search

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"time"

	"iclude/internal/config"
	"iclude/internal/logger"
	"iclude/internal/model"

	"go.uber.org/zap"
)

// RemoteReranker 远程 HTTP 精排器 / Remote HTTP reranker
type RemoteReranker struct {
	cfg        config.RerankConfig
	baseURL    string
	httpClient *http.Client
}

type remoteRerankRequest struct {
	Model           string   `json:"model,omitempty"`
	Query           string   `json:"query"`
	Documents       []string `json:"documents"`
	TopN            int      `json:"top_n,omitempty"`
	ReturnDocuments bool     `json:"return_documents,omitempty"`
}

type remoteRerankResponse struct {
	Results []struct {
		Index          int     `json:"index"`
		RelevanceScore float64 `json:"relevance_score"`
		Score          float64 `json:"score"`
	} `json:"results"`
	Data []struct {
		Index int     `json:"index"`
		Score float64 `json:"score"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

// NewRemoteReranker 创建远程 reranker / Create remote reranker
func NewRemoteReranker(cfg config.RerankConfig) Reranker {
	return NewRemoteRerankerWithClient(cfg, nil)
}

// NewRemoteRerankerWithClient 创建可注入 HTTP client 的远程 reranker / Create remote reranker with injectable HTTP client
func NewRemoteRerankerWithClient(cfg config.RerankConfig, client *http.Client) Reranker {
	baseURL := strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if baseURL == "" {
		logger.Warn("rerank: remote provider requires base_url, disabling")
		return nil
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	if client == nil {
		client = &http.Client{Timeout: timeout}
	}

	return &RemoteReranker{
		cfg:        cfg,
		baseURL:    baseURL,
		httpClient: client,
	}
}

// Rerank 调用远程 API 进行精排，失败则回退原始结果 / Call remote API and fall back to original results on failure
func (r *RemoteReranker) Rerank(ctx context.Context, query string, results []*model.SearchResult) []*model.SearchResult {
	if err := ctx.Err(); err != nil || len(results) <= 1 {
		return results
	}

	query = strings.TrimSpace(query)
	if query == "" {
		return results
	}

	topK := r.cfg.TopK
	if topK <= 1 || topK > len(results) {
		topK = len(results)
	}

	docs := make([]string, 0, topK)
	subset := append([]*model.SearchResult(nil), results[:topK]...)
	for _, res := range subset {
		if res == nil || res.Memory == nil {
			docs = append(docs, "")
			continue
		}
		docs = append(docs, strings.TrimSpace(strings.Join([]string{res.Memory.Content, res.Memory.Abstract, res.Memory.Summary}, "\n")))
	}

	ranked, err := r.request(ctx, query, docs, subset)
	if err != nil {
		logger.Warn("rerank: remote request failed, using original order", zap.Error(err))
		return results
	}
	if len(ranked) == 0 {
		return results
	}

	reranked := append([]*model.SearchResult(nil), results...)
	for i, res := range ranked {
		reranked[i] = res
	}
	return reranked
}

func (r *RemoteReranker) request(ctx context.Context, query string, docs []string, subset []*model.SearchResult) ([]*model.SearchResult, error) {
	reqBody := remoteRerankRequest{
		Model:           strings.TrimSpace(r.cfg.Model),
		Query:           query,
		Documents:       docs,
		TopN:            len(docs),
		ReturnDocuments: false,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("rerank marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, r.baseURL+"/rerank", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("rerank create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if apiKey := strings.TrimSpace(r.cfg.APIKey); apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := r.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("rerank request failed: %w", err)
	}
	defer resp.Body.Close()

	// 限制响应体大小，防止故障服务触发 OOM / Limit response body to prevent OOM from faulty service
	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MB
	if err != nil {
		return nil, fmt.Errorf("rerank read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rerank API returned status %d: %s", resp.StatusCode, string(respBytes))
	}

	var parsed remoteRerankResponse
	if err := json.Unmarshal(respBytes, &parsed); err != nil {
		return nil, fmt.Errorf("rerank unmarshal response: %w", err)
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("rerank API error: %s", parsed.Error.Message)
	}

	type rankedResult struct {
		index int
		score float64
		res   *model.SearchResult
	}
	ranked := make([]rankedResult, 0, len(subset))
	appendRanked := func(index int, score float64) {
		if index < 0 || index >= len(subset) {
			return
		}
		ranked = append(ranked, rankedResult{index: index, score: score, res: subset[index]})
	}

	for _, item := range parsed.Results {
		score := item.RelevanceScore
		if score == 0 {
			score = item.Score
		}
		appendRanked(item.Index, score)
	}
	for _, item := range parsed.Data {
		appendRanked(item.Index, item.Score)
	}
	if len(ranked) == 0 {
		return nil, nil
	}

	maxBaseScore := 0.0
	for _, res := range subset {
		if res != nil && res.Score > maxBaseScore {
			maxBaseScore = res.Score
		}
	}
	if maxBaseScore <= 0 {
		maxBaseScore = 1
	}

	weight := r.cfg.ScoreWeight
	if weight <= 0 {
		weight = 0.7
	}
	if weight > 1 {
		weight = 1
	}

	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].score != ranked[j].score {
			return ranked[i].score > ranked[j].score
		}
		return ranked[i].index < ranked[j].index
	})

	seen := make(map[int]bool, len(ranked))
	out := make([]*model.SearchResult, 0, len(subset))
	for _, item := range ranked {
		if seen[item.index] || item.res == nil {
			continue
		}
		seen[item.index] = true
		baseNorm := item.res.Score / maxBaseScore
		item.res.Score = (1-weight)*baseNorm + weight*item.score
		out = append(out, item.res)
	}
	for idx, res := range subset {
		if !seen[idx] {
			out = append(out, res)
		}
	}
	return out, nil
}
