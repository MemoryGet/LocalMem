package stage

import (
	"context"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"

	"iclude/internal/model"
	"iclude/internal/search/pipeline"
)

// overlapRerankerEpsilon 精排分数比较容差 / Epsilon for reranker float comparison
const overlapRerankerEpsilon = 1e-12

// defaultOverlapTopK 默认精排数量 / Default top-K for overlap reranking
const defaultOverlapTopK = 20

// defaultOverlapScoreWeight 默认分数权重 / Default score weight for overlap reranking
const defaultOverlapScoreWeight = 0.7

// OverlapRerankStage 重叠度精排阶段 / Overlap reranker pipeline stage
type OverlapRerankStage struct {
	topK        int
	scoreWeight float64
}

// NewOverlapRerankStage 创建重叠度精排阶段 / Create a new overlap reranker stage
func NewOverlapRerankStage(topK int, scoreWeight float64) *OverlapRerankStage {
	if topK <= 0 {
		topK = defaultOverlapTopK
	}
	if scoreWeight <= 0 {
		scoreWeight = defaultOverlapScoreWeight
	}
	if scoreWeight > 1 {
		scoreWeight = 1
	}
	return &OverlapRerankStage{
		topK:        topK,
		scoreWeight: scoreWeight,
	}
}

// Name 返回阶段名称 / Return stage name
func (s *OverlapRerankStage) Name() string {
	return "rerank_overlap"
}

// Execute 执行重叠度精排 / Execute overlap reranking
func (s *OverlapRerankStage) Execute(ctx context.Context, state *pipeline.PipelineState) (*pipeline.PipelineState, error) {
	start := time.Now()
	inputCount := len(state.Candidates)

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

	query := strings.TrimSpace(state.Query)
	if query == "" {
		return state, nil
	}

	topK := s.topK
	if topK > len(state.Candidates) {
		topK = len(state.Candidates)
	}

	terms := overlapExpandTerms(query)
	queryNorm := overlapNormalizeText(query)
	if queryNorm == "" || len(terms) == 0 {
		return state, nil
	}

	subset := make([]*model.SearchResult, topK)
	copy(subset, state.Candidates[:topK])

	maxBaseScore := 0.0
	for _, res := range subset {
		if res != nil && res.Score > maxBaseScore {
			maxBaseScore = res.Score
		}
	}
	if maxBaseScore <= 0 {
		maxBaseScore = 1
	}

	type scoredResult struct {
		res       *model.SearchResult
		index     int
		final     float64
		baseScore float64
	}
	scored := make([]scoredResult, 0, len(subset))
	for i, res := range subset {
		if res == nil || res.Memory == nil {
			continue
		}
		baseNorm := res.Score / maxBaseScore
		overlap := overlapScore(queryNorm, terms, res.Memory)
		final := (1-s.scoreWeight)*baseNorm + s.scoreWeight*overlap
		scored = append(scored, scoredResult{
			res:       res,
			index:     i,
			final:     final,
			baseScore: res.Score,
		})
	}
	if len(scored) <= 1 {
		return state, nil
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if math.Abs(scored[i].final-scored[j].final) > overlapRerankerEpsilon {
			return scored[i].final > scored[j].final
		}
		if math.Abs(scored[i].baseScore-scored[j].baseScore) > overlapRerankerEpsilon {
			return scored[i].baseScore > scored[j].baseScore
		}
		return scored[i].index < scored[j].index
	})

	// 创建副本避免修改输入 / Create copies to avoid mutating input
	reranked := make([]*model.SearchResult, len(state.Candidates))
	copy(reranked, state.Candidates)
	for i, item := range scored {
		resCopy := *item.res
		resCopy.Score = item.final
		reranked[i] = &resCopy
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

// overlapSplitPattern 词分割正则 / Token split regex
var overlapSplitPattern = regexp.MustCompile(`[^\p{L}\p{N}\p{Han}]+`)

// overlapNormalizeText 文本归一化 / Normalize text for overlap comparison
func overlapNormalizeText(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return ""
	}
	text = overlapSplitPattern.ReplaceAllString(text, " ")
	return strings.Join(strings.Fields(text), " ")
}

// overlapExpandTerms 展开查询为词项（含 CJK bigram）/ Expand query into terms with CJK bigrams
func overlapExpandTerms(query string) []string {
	normalized := overlapNormalizeText(query)
	if normalized == "" {
		return nil
	}
	parts := strings.Fields(normalized)
	seen := make(map[string]bool, len(parts)*2)
	terms := make([]string, 0, len(parts)*2)
	for _, part := range parts {
		if !seen[part] {
			seen[part] = true
			terms = append(terms, part)
		}
		runes := []rune(part)
		if overlapIsHanOnly(runes) && len(runes) > 1 {
			for i := 0; i < len(runes)-1; i++ {
				bigram := string(runes[i : i+2])
				if !seen[bigram] {
					seen[bigram] = true
					terms = append(terms, bigram)
				}
			}
		}
	}
	return terms
}

// overlapIsHanOnly 判断是否全是汉字 / Check if all runes are Han characters
func overlapIsHanOnly(runes []rune) bool {
	if len(runes) == 0 {
		return false
	}
	for _, r := range runes {
		if !unicode.Is(unicode.Han, r) {
			return false
		}
	}
	return true
}

// overlapScore 计算查询与记忆的重叠度 / Calculate query-memory overlap score
func overlapScore(queryNorm string, terms []string, mem *model.Memory) float64 {
	doc := overlapNormalizeText(strings.Join([]string{mem.Content, mem.Excerpt, mem.Summary}, " "))
	if doc == "" {
		return 0
	}

	phraseBoost := 0.0
	if strings.Contains(doc, queryNorm) {
		phraseBoost = 1.0
	}

	matched := 0
	for _, term := range terms {
		if strings.Contains(doc, term) {
			matched++
		}
	}

	coverage := float64(matched) / float64(len(terms))
	return 0.35*phraseBoost + 0.65*coverage
}
