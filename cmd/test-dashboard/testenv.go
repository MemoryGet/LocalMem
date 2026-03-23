package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"iclude/internal/config"
	"iclude/internal/model"
	"iclude/internal/search"
	"iclude/internal/store"
	"iclude/pkg/tokenizer"

	"github.com/google/uuid"
)

// FixtureDataset JSON 数据集文件结构 / JSON fixture dataset file structure
type FixtureDataset struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Memories    []FixtureMemory   `json:"memories"`
	Entities    []FixtureEntity   `json:"entities"`
	Relations   []FixtureRelation `json:"relations"`
	TestCases   []TestCaseDef     `json:"test_cases"`
}

// FixtureMemory 数据集中的记忆 / Memory entry in fixture
type FixtureMemory struct {
	Content       string  `json:"content"`
	Scope         string  `json:"scope"`
	Kind          string  `json:"kind"`
	TeamID        string  `json:"team_id"`
	RetentionTier string  `json:"retention_tier"`
	Strength      float64 `json:"strength"`
	Abstract      string  `json:"abstract"`
	Summary       string  `json:"summary"`
}

// FixtureEntity 数据集中的实体 / Entity entry in fixture
type FixtureEntity struct {
	Name        string `json:"name"`
	EntityType  string `json:"entity_type"`
	Scope       string `json:"scope"`
	Description string `json:"description"`
}

// FixtureRelation 数据集中的关系 / Relation entry in fixture
type FixtureRelation struct {
	Source       string `json:"source"`
	Target       string `json:"target"`
	RelationType string `json:"relation_type"`
}

// TestCaseDef 测试用例定义 / Test case definition
type TestCaseDef struct {
	Name           string `json:"name"`
	Query          string `json:"query"`
	ExpectedIntent string `json:"expected_intent"`
	Description    string `json:"description"`
}

// DatasetStats 数据集统计 / Dataset statistics
type DatasetStats struct {
	Memories  int `json:"memories"`
	Entities  int `json:"entities"`
	Relations int `json:"relations"`
	Cases     int `json:"cases"`
}

// DatasetInfo 数据集信息 / Dataset info for listing
type DatasetInfo struct {
	Name        string       `json:"name"`
	FileName    string       `json:"file_name"`
	Description string       `json:"description"`
	Stats       DatasetStats `json:"stats"`
}

// QueryResult 查询结果 / Full query result with pipeline details
type QueryResult struct {
	Preprocess *PreprocessResult         `json:"preprocess"`
	Channels   map[string]*ChannelResult `json:"channels"`
	Merged     []MemoryResult            `json:"merged"`
	DurationMs int64                     `json:"duration_ms"`
}

// PreprocessResult 预处理结果 / Preprocessing result
type PreprocessResult struct {
	OriginalQuery string             `json:"original_query"`
	SemanticQuery string             `json:"semantic_query"`
	Keywords      []string           `json:"keywords"`
	Entities      []string           `json:"entities"`
	Intent        string             `json:"intent"`
	Weights       map[string]float64 `json:"weights"`
}

// ChannelResult 单通道结果 / Single channel result
type ChannelResult struct {
	Available bool           `json:"available"`
	Count     int            `json:"count"`
	Results   []MemoryResult `json:"results"`
}

// MemoryResult 记忆结果 / Memory result item
type MemoryResult struct {
	MemoryID string  `json:"memory_id"`
	Content  string  `json:"content"`
	Score    float64 `json:"score"`
	Source   string  `json:"source"`
	Scope    string  `json:"scope,omitempty"`
	Kind     string  `json:"kind,omitempty"`
}

// CaseResult 测试用例结果 / Test case execution result
type CaseResult struct {
	Name           string            `json:"name"`
	Query          string            `json:"query"`
	Description    string            `json:"description"`
	ExpectedIntent string            `json:"expected_intent"`
	ActualIntent   string            `json:"actual_intent"`
	Passed         bool              `json:"passed"`
	ResultCount    int               `json:"result_count"`
	DurationMs     int64             `json:"duration_ms"`
	Preprocess     *PreprocessResult `json:"preprocess"`
	TopResults     []MemoryResult    `json:"top_results"`
}

// TestEnv 临时测试环境 / Ephemeral test environment
type TestEnv struct {
	mu           sync.Mutex
	dir          string
	stores       *store.Stores
	preprocessor *search.Preprocessor
	retriever    *search.Retriever
	datasetName  string
	stats        DatasetStats
	testCases    []TestCaseDef
	fixtureDir   string
}

// NewTestEnv 创建测试环境 / Create test environment
func NewTestEnv(fixtureDir string) *TestEnv {
	return &TestEnv{fixtureDir: fixtureDir}
}

// IsLoaded 检查是否已加载数据集 / Check if dataset is loaded
func (e *TestEnv) IsLoaded() bool {
	return e.stores != nil
}

// Close 销毁临时环境 / Destroy ephemeral environment
func (e *TestEnv) Close() {
	if e.stores != nil {
		e.stores.Close()
		e.stores = nil
	}
	if e.dir != "" {
		os.RemoveAll(e.dir)
		e.dir = ""
	}
	e.preprocessor = nil
	e.retriever = nil
	e.datasetName = ""
	e.stats = DatasetStats{}
	e.testCases = nil
}

// ListDatasets 列出所有数据集 / List all fixture datasets
func (e *TestEnv) ListDatasets() ([]DatasetInfo, error) {
	files, err := filepath.Glob(filepath.Join(e.fixtureDir, "*.json"))
	if err != nil {
		return nil, err
	}
	var datasets []DatasetInfo
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var ds FixtureDataset
		if err := json.Unmarshal(data, &ds); err != nil {
			continue
		}
		baseName := strings.TrimSuffix(filepath.Base(f), ".json")
		datasets = append(datasets, DatasetInfo{
			Name:        ds.Name,
			FileName:    baseName,
			Description: ds.Description,
			Stats: DatasetStats{
				Memories:  len(ds.Memories),
				Entities:  len(ds.Entities),
				Relations: len(ds.Relations),
				Cases:     len(ds.TestCases),
			},
		})
	}
	return datasets, nil
}

// Load 加载数据集 / Load fixture dataset into ephemeral SQLite
func (e *TestEnv) Load(datasetFile string) error {
	e.Close()

	// 读取并解析 fixture
	path := filepath.Join(e.fixtureDir, datasetFile+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read fixture: %w", err)
	}
	var ds FixtureDataset
	if err := json.Unmarshal(data, &ds); err != nil {
		return fmt.Errorf("parse fixture: %w", err)
	}

	// 创建临时目录和 SQLite
	e.dir, err = os.MkdirTemp("", "testenv-*")
	if err != nil {
		return fmt.Errorf("create temp dir: %w", err)
	}
	dbPath := filepath.Join(e.dir, "test.db")

	// 自动检测 Jieba 服务，可用则用 jieba，否则 fallback 到 simple
	tokProvider := "simple"
	jiebaURL := "http://localhost:8866"
	if resp, err := http.Get(jiebaURL + "/health"); err == nil {
		resp.Body.Close()
		if resp.StatusCode == 200 {
			tokProvider = "jieba"
			log.Printf("jieba service detected at %s, using jieba tokenizer", jiebaURL)
		}
	}

	cfg := config.Config{
		Storage: config.StorageConfig{
			SQLite: config.SQLiteConfig{
				Enabled: true,
				Path:    dbPath,
				Search: config.SearchConfig{
					BM25Weights: config.BM25WeightsConfig{Content: 10, Abstract: 5, Summary: 3},
				},
				Tokenizer: config.TokenizerConfig{Provider: tokProvider, JiebaURL: jiebaURL},
			},
		},
	}

	ctx := context.Background()
	stores, err := store.InitStores(ctx, cfg, nil)
	if err != nil {
		os.RemoveAll(e.dir)
		return fmt.Errorf("init stores: %w", err)
	}
	e.stores = stores

	// 写入 memories
	for _, fm := range ds.Memories {
		strength := fm.Strength
		if strength == 0 {
			strength = 1.0
		}
		mem := &model.Memory{
			ID:            uuid.New().String(),
			Content:       fm.Content,
			Scope:         fm.Scope,
			Kind:          fm.Kind,
			TeamID:        fm.TeamID,
			RetentionTier: fm.RetentionTier,
			Strength:      strength,
			Abstract:      fm.Abstract,
			Summary:       fm.Summary,
			IsLatest:      true,
			CreatedAt:     time.Now().UTC(),
			UpdatedAt:     time.Now().UTC(),
		}
		if err := stores.MemoryStore.Create(ctx, mem); err != nil {
			log.Printf("warn: skip memory %q: %v", fm.Content[:min(30, len(fm.Content))], err)
			continue
		}
	}

	// 写入 entities，维护 nameToID 映射
	nameToID := make(map[string]string)
	if stores.GraphStore != nil {
		for _, fe := range ds.Entities {
			ent := &model.Entity{
				ID:          uuid.New().String(),
				Name:        fe.Name,
				EntityType:  fe.EntityType,
				Scope:       fe.Scope,
				Description: fe.Description,
				CreatedAt:   time.Now().UTC(),
				UpdatedAt:   time.Now().UTC(),
			}
			if err := stores.GraphStore.CreateEntity(ctx, ent); err != nil {
				log.Printf("warn: skip entity %q: %v", fe.Name, err)
				continue
			}
			nameToID[fe.Name] = ent.ID
		}

		// 写入 relations
		for _, fr := range ds.Relations {
			srcID, ok1 := nameToID[fr.Source]
			tgtID, ok2 := nameToID[fr.Target]
			if !ok1 || !ok2 {
				continue
			}
			rel := &model.EntityRelation{
				ID:           uuid.New().String(),
				SourceID:     srcID,
				TargetID:     tgtID,
				RelationType: fr.RelationType,
				Weight:       1.0,
				CreatedAt:    time.Now().UTC(),
			}
			stores.GraphStore.CreateRelation(ctx, rel)
		}

		// 关联 memory-entity（基于 content 包含 entity name）
		allMems, _ := stores.MemoryStore.List(ctx, &model.Identity{TeamID: "", OwnerID: model.SystemOwnerID}, 0, 1000)
		for _, mem := range allMems {
			contentLower := strings.ToLower(mem.Content)
			for name, entityID := range nameToID {
				if strings.Contains(contentLower, strings.ToLower(name)) {
					me := &model.MemoryEntity{
						MemoryID:  mem.ID,
						EntityID:  entityID,
						Role:      "mentioned",
						CreatedAt: time.Now().UTC(),
					}
					stores.GraphStore.CreateMemoryEntity(ctx, me)
				}
			}
		}
	}

	// 构建业务层
	retrievalCfg := config.RetrievalConfig{
		GraphEnabled:     true,
		GraphDepth:       1,
		GraphWeight:      0.8,
		FTSWeight:        1.0,
		QdrantWeight:     1.0,
		GraphFTSTop:      5,
		GraphEntityLimit: 10,
		Preprocess:       config.PreprocessConfig{Enabled: true, UseLLM: false},
	}

	var tok tokenizer.Tokenizer
	if stores.Tokenizer != nil {
		tok = stores.Tokenizer
	} else {
		tok = tokenizer.NewSimpleTokenizer()
	}

	e.preprocessor = search.NewPreprocessor(tok, stores.GraphStore, nil, retrievalCfg)
	e.retriever = search.NewRetriever(stores.MemoryStore, nil, nil, stores.GraphStore, nil, retrievalCfg, e.preprocessor)
	e.datasetName = ds.Name
	e.testCases = ds.TestCases
	e.stats = DatasetStats{
		Memories:  len(ds.Memories),
		Entities:  len(ds.Entities),
		Relations: len(ds.Relations),
		Cases:     len(ds.TestCases),
	}

	return nil
}

// Query 执行查询 / Execute query through full pipeline
func (e *TestEnv) Query(query string, limit int) (*QueryResult, error) {
	if !e.IsLoaded() {
		return nil, fmt.Errorf("no dataset loaded")
	}
	if limit <= 0 {
		limit = 10
	}

	start := time.Now()
	ctx := context.Background()

	// 预处理
	var pp *PreprocessResult
	plan, err := e.preprocessor.Process(ctx, query, "")
	if err == nil && plan != nil {
		pp = &PreprocessResult{
			OriginalQuery: plan.OriginalQuery,
			SemanticQuery: plan.SemanticQuery,
			Keywords:      plan.Keywords,
			Entities:      plan.Entities,
			Intent:        string(plan.Intent),
			Weights: map[string]float64{
				"fts":    plan.Weights.FTS,
				"qdrant": plan.Weights.Qdrant,
				"graph":  plan.Weights.Graph,
			},
		}
	}

	// 各通道独立结果
	channels := map[string]*ChannelResult{
		"fts":    {Available: e.stores.MemoryStore != nil},
		"qdrant": {Available: false},
		"graph":  {Available: e.stores.GraphStore != nil},
	}

	// FTS 通道
	if channels["fts"].Available && query != "" {
		ftsQuery := query
		if plan != nil && len(plan.Keywords) > 0 {
			ftsQuery = strings.Join(plan.Keywords, " ")
		}
		ftsResults, err := e.stores.MemoryStore.SearchText(ctx, ftsQuery, &model.Identity{TeamID: "", OwnerID: model.SystemOwnerID}, limit)
		if err == nil {
			channels["fts"].Count = len(ftsResults)
			for _, r := range ftsResults {
				channels["fts"].Results = append(channels["fts"].Results, MemoryResult{
					MemoryID: r.Memory.ID, Content: r.Memory.Content,
					Score: r.Score, Source: r.Source,
					Scope: r.Memory.Scope, Kind: r.Memory.Kind,
				})
			}
		}
	}

	// 融合结果
	req := &model.RetrieveRequest{Query: query, Limit: limit}
	merged, _ := e.retriever.Retrieve(ctx, req)

	var mergedResults []MemoryResult
	for _, r := range merged {
		mergedResults = append(mergedResults, MemoryResult{
			MemoryID: r.Memory.ID, Content: r.Memory.Content,
			Score: r.Score, Source: r.Source,
			Scope: r.Memory.Scope, Kind: r.Memory.Kind,
		})
	}

	return &QueryResult{
		Preprocess: pp,
		Channels:   channels,
		Merged:     mergedResults,
		DurationMs: time.Since(start).Milliseconds(),
	}, nil
}

// RunCase 执行单个测试用例 / Run single test case
func (e *TestEnv) RunCase(tc TestCaseDef) *CaseResult {
	start := time.Now()
	qr, _ := e.Query(tc.Query, 5)

	result := &CaseResult{
		Name:           tc.Name,
		Query:          tc.Query,
		Description:    tc.Description,
		ExpectedIntent: tc.ExpectedIntent,
		DurationMs:     time.Since(start).Milliseconds(),
	}

	if qr != nil {
		if qr.Preprocess != nil {
			result.ActualIntent = qr.Preprocess.Intent
			result.Preprocess = qr.Preprocess
		}
		result.ResultCount = len(qr.Merged)
		for i, r := range qr.Merged {
			if i >= 3 {
				break
			}
			result.TopResults = append(result.TopResults, r)
		}
	}

	if tc.ExpectedIntent != "" {
		result.Passed = result.ActualIntent == tc.ExpectedIntent
	} else {
		result.Passed = true
	}

	return result
}
