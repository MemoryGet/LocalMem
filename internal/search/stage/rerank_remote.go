package stage

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/internal/search/pipeline"

	"go.uber.org/zap"
)

// remoteRerankEpsilon 远程精排分数比较容差 / Epsilon for remote reranker float comparison
const remoteRerankEpsilon = 1e-12

// defaultRemoteTimeout 远程精排默认超时 / Default timeout for remote reranking
const defaultRemoteTimeout = 5 * time.Second

// defaultRemoteTopK 远程精排默认 top-K / Default top-K for remote reranking
const defaultRemoteTopK = 20

// defaultRemoteScoreWeight 远程精排默认分数权重 / Default score weight for remote reranking
const defaultRemoteScoreWeight = 0.7

// RemoteRerankConfig 远程精排配置 / Remote reranker configuration
type RemoteRerankConfig struct {
	BaseURL     string
	APIKey      string
	Model       string
	TopK        int
	ScoreWeight float64
	Timeout     time.Duration
}

// RemoteRerankStage 远程 API 精排阶段，内置熔断器 / Remote API reranker stage with circuit breaker
type RemoteRerankStage struct {
	cfg        RemoteRerankConfig
	httpClient *http.Client
	breaker    *stageCircuitBreaker
}

// NewRemoteRerankStage 创建远程精排阶段 / Create a new remote reranker stage
func NewRemoteRerankStage(cfg RemoteRerankConfig) *RemoteRerankStage {
	return NewRemoteRerankStageWithClient(cfg, nil)
}

// NewRemoteRerankStageWithClient 创建可注入 HTTP client 的远程精排阶段 / Create remote reranker stage with injectable HTTP client
func NewRemoteRerankStageWithClient(cfg RemoteRerankConfig, client *http.Client) *RemoteRerankStage {
	cfg.BaseURL = strings.TrimRight(strings.TrimSpace(cfg.BaseURL), "/")
	if cfg.TopK <= 0 {
		cfg.TopK = defaultRemoteTopK
	}
	if cfg.ScoreWeight <= 0 {
		cfg.ScoreWeight = defaultRemoteScoreWeight
	}
	if cfg.ScoreWeight > 1 {
		cfg.ScoreWeight = 1
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = defaultRemoteTimeout
	}
	if client == nil {
		client = &http.Client{Timeout: cfg.Timeout}
	}
	return &RemoteRerankStage{
		cfg:        cfg,
		httpClient: client,
		breaker:    newStageCircuitBreaker(3, 30*time.Second),
	}
}

// Name 返回阶段名称 / Return stage name
func (s *RemoteRerankStage) Name() string {
	return "rerank_remote"
}

// Execute 执行远程精排 / Execute remote reranking
func (s *RemoteRerankStage) Execute(ctx context.Context, state *pipeline.PipelineState) (*pipeline.PipelineState, error) {
	start := time.Now()
	inputCount := len(state.Candidates)

	if s.cfg.BaseURL == "" {
		state.AddTrace(pipeline.StageTrace{
			Name:    s.Name(),
			Skipped: true,
			Note:    "base_url is empty",
		})
		return state, nil
	}

	if len(state.Candidates) <= 1 || state.Query == "" {
		state.AddTrace(pipeline.StageTrace{
			Name:        s.Name(),
			Duration:    time.Since(start),
			InputCount:  inputCount,
			OutputCount: inputCount,
			Note:        "skipped: insufficient candidates or empty query",
		})
		return state, nil
	}

	if !s.breaker.allow() {
		logger.Debug("rerank_remote: circuit breaker open, skipping")
		state.AddTrace(pipeline.StageTrace{
			Name:        s.Name(),
			Duration:    time.Since(start),
			InputCount:  inputCount,
			OutputCount: inputCount,
			Note:        "circuit breaker open",
		})
		return state, nil
	}

	query := strings.TrimSpace(state.Query)
	if query == "" {
		return state, nil
	}

	topK := s.cfg.TopK
	if topK > len(state.Candidates) {
		topK = len(state.Candidates)
	}

	// 构建文档列表 / Build document list
	docs := make([]string, 0, topK)
	subset := make([]*model.SearchResult, topK)
	copy(subset, state.Candidates[:topK])
	for _, res := range subset {
		if res == nil || res.Memory == nil {
			docs = append(docs, "")
			continue
		}
		docs = append(docs, strings.TrimSpace(strings.Join([]string{res.Memory.Content, res.Memory.Excerpt, res.Memory.Summary}, "\n")))
	}

	ranked, err := s.request(ctx, query, docs, subset)
	if err != nil {
		s.breaker.recordFailure()
		logger.Warn("rerank_remote: request failed, using original order", zap.Error(err))
		state.AddTrace(pipeline.StageTrace{
			Name:        s.Name(),
			Duration:    time.Since(start),
			InputCount:  inputCount,
			OutputCount: inputCount,
			Note:        "remote error: " + err.Error(),
		})
		return state, nil
	}

	s.breaker.recordSuccess()

	if len(ranked) == 0 {
		state.AddTrace(pipeline.StageTrace{
			Name:        s.Name(),
			Duration:    time.Since(start),
			InputCount:  inputCount,
			OutputCount: inputCount,
			Note:        "remote returned empty results",
		})
		return state, nil
	}

	// 创建副本避免修改输入 / Create copies to avoid mutating input
	reranked := make([]*model.SearchResult, len(state.Candidates))
	copy(reranked, state.Candidates)
	for i, res := range ranked {
		reranked[i] = res
	}

	state.Candidates = reranked

	state.AddTrace(pipeline.StageTrace{
		Name:        s.Name(),
		Duration:    time.Since(start),
		InputCount:  inputCount,
		OutputCount: len(reranked),
	})

	return state, nil
}

// remoteRerankReq 远程精排请求体 / Remote rerank request body
type remoteRerankReq struct {
	Model           string   `json:"model,omitempty"`
	Query           string   `json:"query"`
	Documents       []string `json:"documents"`
	TopN            int      `json:"top_n,omitempty"`
	ReturnDocuments bool     `json:"return_documents,omitempty"`
}

// remoteRerankResp 远程精排响应体 / Remote rerank response body
type remoteRerankResp struct {
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

func (s *RemoteRerankStage) request(ctx context.Context, query string, docs []string, subset []*model.SearchResult) ([]*model.SearchResult, error) {
	reqBody := remoteRerankReq{
		Model:           strings.TrimSpace(s.cfg.Model),
		Query:           query,
		Documents:       docs,
		TopN:            len(docs),
		ReturnDocuments: false,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("rerank marshal request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.BaseURL+"/rerank", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("rerank create request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if apiKey := strings.TrimSpace(s.cfg.APIKey); apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+apiKey)
	}

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("rerank request failed: %w", err)
	}
	defer resp.Body.Close()

	// 限制响应体大小 / Limit response body size
	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("rerank read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("rerank API returned status %d: %s", resp.StatusCode, string(respBytes))
	}

	var parsed remoteRerankResp
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

	sort.SliceStable(ranked, func(i, j int) bool {
		if math.Abs(ranked[i].score-ranked[j].score) > remoteRerankEpsilon {
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
		// 创建副本避免修改输入 / Create copy to avoid mutating input
		resCopy := *item.res
		resCopy.Score = (1-s.cfg.ScoreWeight)*baseNorm + s.cfg.ScoreWeight*item.score
		out = append(out, &resCopy)
	}
	for idx, res := range subset {
		if !seen[idx] {
			out = append(out, res)
		}
	}
	return out, nil
}

// stageCircuitBreaker 简易熔断器 / Simple circuit breaker for stages
type stageCircuitBreaker struct {
	mu               sync.Mutex
	state            int // 0=closed, 1=open, 2=half-open
	consecutiveFails int
	failThreshold    int
	cooldown         time.Duration
	openedAt         time.Time
}

func newStageCircuitBreaker(failThreshold int, cooldown time.Duration) *stageCircuitBreaker {
	if failThreshold <= 0 {
		failThreshold = 3
	}
	if cooldown <= 0 {
		cooldown = 30 * time.Second
	}
	return &stageCircuitBreaker{
		failThreshold: failThreshold,
		cooldown:      cooldown,
	}
}

func (cb *stageCircuitBreaker) allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	switch cb.state {
	case 0: // closed
		return true
	case 1: // open
		if time.Since(cb.openedAt) >= cb.cooldown {
			cb.state = 2 // half-open
			return true
		}
		return false
	case 2: // half-open
		return true
	default:
		return true
	}
}

func (cb *stageCircuitBreaker) recordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.consecutiveFails = 0
	cb.state = 0
}

func (cb *stageCircuitBreaker) recordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.consecutiveFails++
	if cb.state == 2 { // half-open
		cb.state = 1
		cb.openedAt = time.Now()
		return
	}
	if cb.consecutiveFails >= cb.failThreshold {
		cb.state = 1
		cb.openedAt = time.Now()
	}
}
