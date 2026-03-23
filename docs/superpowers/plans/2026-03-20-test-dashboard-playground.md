# Test Dashboard Playground Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Extend test-dashboard with JSON fixture datasets, REST API for interactive query testing, and a Vue Playground UI for batch test cases + freeform query debugging.

**Architecture:** Go backend adds `TestEnv` (ephemeral SQLite + preprocessor/retriever chain) with REST endpoints. Vue frontend adds a Playground tab with dataset selector, batch case runner, and interactive query panel. Existing Go Tests tab is untouched.

**Tech Stack:** Go (stdlib net/http, existing store/search/memory packages), Vue 3 + Pinia + Tailwind CSS, JSON fixture files

**Spec:** `docs/superpowers/specs/2026-03-20-test-dashboard-playground-design.md`

---

## Phase 1: Backend

### Task 1: Create test fixture datasets

**Files:**
- Create: `testing/fixtures/tech_knowledge.json`
- Create: `testing/fixtures/meeting_notes.json`

- [ ] **Step 1: Create tech_knowledge.json**

Create `testing/fixtures/tech_knowledge.json`:

```json
{
  "name": "技术知识库",
  "description": "Go/K8s/Docker 相关的技术记忆，含实体和关系图谱",
  "memories": [
    {"content": "Go语言的并发模型基于 goroutine 和 channel，支持轻量级线程", "scope": "tech", "kind": "fact", "team_id": "t1", "retention_tier": "permanent", "strength": 0.95, "abstract": "Go并发模型", "summary": "goroutine + channel 轻量级并发"},
    {"content": "Kubernetes 是容器编排平台，核心概念包括 Pod、Service、Deployment", "scope": "tech", "kind": "fact", "team_id": "t1", "retention_tier": "permanent", "strength": 0.9, "abstract": "K8s核心概念", "summary": "Pod/Service/Deployment 容器编排"},
    {"content": "Docker 容器技术基于 Linux namespace 和 cgroup 实现进程隔离", "scope": "tech", "kind": "fact", "team_id": "t1", "retention_tier": "long_term", "strength": 0.85, "abstract": "Docker隔离原理", "summary": "namespace + cgroup 进程隔离"},
    {"content": "微服务架构将单体应用拆分为多个独立部署的小服务，通过 API 通信", "scope": "tech", "kind": "fact", "team_id": "t1", "retention_tier": "permanent", "strength": 0.9, "abstract": "微服务架构", "summary": "独立部署的小服务通过API通信"},
    {"content": "gRPC 是 Google 开源的高性能 RPC 框架，使用 Protocol Buffers 序列化", "scope": "tech", "kind": "fact", "team_id": "t1", "retention_tier": "standard", "strength": 0.8, "abstract": "gRPC框架", "summary": "高性能RPC + Protobuf序列化"},
    {"content": "SQLite 是嵌入式关系数据库，支持 FTS5 全文搜索和 WAL 模式", "scope": "tech", "kind": "fact", "team_id": "t1", "retention_tier": "permanent", "strength": 0.92, "abstract": "SQLite特性", "summary": "嵌入式数据库 FTS5 WAL"},
    {"content": "Qdrant 是专为向量相似性搜索设计的数据库，支持过滤和负载", "scope": "tech", "kind": "fact", "team_id": "t1", "retention_tier": "long_term", "strength": 0.75, "abstract": "Qdrant向量数据库", "summary": "向量相似性搜索引擎"},
    {"content": "BM25 是经典的文本检索排名算法，考虑词频和文档长度归一化", "scope": "tech", "kind": "fact", "team_id": "t1", "retention_tier": "standard", "strength": 0.7, "abstract": "BM25算法", "summary": "词频+文档长度归一化排名"},
    {"content": "Reciprocal Rank Fusion 将多路检索结果融合，公式 score = Σ 1/(k+rank)", "scope": "tech", "kind": "fact", "team_id": "t1", "retention_tier": "standard", "strength": 0.78, "abstract": "RRF融合算法", "summary": "多路检索结果排名融合"},
    {"content": "Go 的 context 包用于控制 goroutine 生命周期，支持超时和取消", "scope": "tech", "kind": "skill", "team_id": "t1", "retention_tier": "long_term", "strength": 0.88, "abstract": "Go context用法", "summary": "goroutine 超时控制和取消传播"},
    {"content": "Kubernetes 通过 etcd 存储集群状态，使用 watch 机制实现事件驱动", "scope": "tech", "kind": "fact", "team_id": "t1", "retention_tier": "long_term", "strength": 0.82, "abstract": "K8s etcd", "summary": "etcd 存储集群状态 watch事件驱动"},
    {"content": "Docker Compose 用于定义和运行多容器应用，使用 YAML 配置", "scope": "tech", "kind": "skill", "team_id": "t1", "retention_tier": "standard", "strength": 0.72, "abstract": "Docker Compose", "summary": "多容器YAML编排工具"},
    {"content": "向量嵌入将文本映射到高维空间，语义相近的文本距离更近", "scope": "tech", "kind": "fact", "team_id": "t1", "retention_tier": "permanent", "strength": 0.85, "abstract": "向量嵌入原理", "summary": "文本→高维向量 语义距离"},
    {"content": "Go 测试使用 testing 包，支持表驱动测试和基准测试", "scope": "tech", "kind": "skill", "team_id": "t1", "retention_tier": "standard", "strength": 0.8, "abstract": "Go测试", "summary": "testing包 表驱动 benchmark"},
    {"content": "Helm 是 Kubernetes 的包管理器，通过 Chart 模板管理应用部署", "scope": "tech", "kind": "fact", "team_id": "t1", "retention_tier": "standard", "strength": 0.68, "abstract": "Helm包管理", "summary": "K8s Chart模板包管理器"},
    {"content": "最近在调研 RAG 系统架构，检索增强生成需要向量数据库配合 LLM", "scope": "tech", "kind": "note", "team_id": "t1", "retention_tier": "short_term", "strength": 0.6, "abstract": "RAG调研", "summary": "检索增强生成 向量库+LLM"}
  ],
  "entities": [
    {"name": "Go", "entity_type": "tool", "scope": "tech", "description": "Go编程语言"},
    {"name": "Kubernetes", "entity_type": "tool", "scope": "tech", "description": "容器编排平台"},
    {"name": "Docker", "entity_type": "tool", "scope": "tech", "description": "容器技术"},
    {"name": "SQLite", "entity_type": "tool", "scope": "tech", "description": "嵌入式数据库"},
    {"name": "Qdrant", "entity_type": "tool", "scope": "tech", "description": "向量数据库"},
    {"name": "gRPC", "entity_type": "tool", "scope": "tech", "description": "RPC框架"},
    {"name": "Helm", "entity_type": "tool", "scope": "tech", "description": "K8s包管理器"},
    {"name": "BM25", "entity_type": "concept", "scope": "tech", "description": "文本检索排名算法"},
    {"name": "RRF", "entity_type": "concept", "scope": "tech", "description": "多路检索融合算法"}
  ],
  "relations": [
    {"source": "Kubernetes", "target": "Docker", "relation_type": "uses"},
    {"source": "Kubernetes", "target": "Helm", "relation_type": "uses"},
    {"source": "Go", "target": "Kubernetes", "relation_type": "used_by"},
    {"source": "Go", "target": "gRPC", "relation_type": "uses"},
    {"source": "SQLite", "target": "BM25", "relation_type": "uses"},
    {"source": "Qdrant", "target": "RRF", "relation_type": "related_to"}
  ],
  "test_cases": [
    {"name": "精确查找-短查询", "query": "Go goroutine", "expected_intent": "keyword", "description": "2词短查询 → keyword"},
    {"name": "精确查找-工具名", "query": "SQLite FTS5", "expected_intent": "keyword", "description": "技术术语 → keyword"},
    {"name": "语义探索", "query": "如何设计高并发的微服务架构", "expected_intent": "semantic", "description": "探索性问题 → semantic"},
    {"name": "语义-英文", "query": "how does container isolation work in Docker", "expected_intent": "semantic", "description": "how 开头 → semantic"},
    {"name": "时间相关", "query": "最近在调研什么技术", "expected_intent": "temporal", "description": "最近 → temporal"},
    {"name": "关联查询", "query": "和Kubernetes相关的技术栈", "expected_intent": "relational", "description": "相关 → relational"},
    {"name": "通用查询", "query": "Go testing package table driven benchmark usage", "expected_intent": "general", "description": "6-15词中等长度 → general"},
    {"name": "向量数据库", "query": "什么是向量嵌入", "expected_intent": "semantic", "description": "什么是 → semantic"}
  ]
}
```

- [ ] **Step 2: Create meeting_notes.json**

Create `testing/fixtures/meeting_notes.json`:

```json
{
  "name": "会议记录",
  "description": "项目周会和技术评审的记忆数据",
  "memories": [
    {"content": "周一项目周会决定将 IClude 的检索模块重构为三通道架构", "scope": "work", "kind": "note", "team_id": "t1", "retention_tier": "standard", "strength": 0.8, "abstract": "检索重构决策", "summary": "三通道架构重构"},
    {"content": "张三负责实现 Query Preprocessor 模块，预计本周完成", "scope": "work", "kind": "note", "team_id": "t1", "retention_tier": "short_term", "strength": 0.7, "abstract": "任务分配", "summary": "张三→Preprocessor 本周"},
    {"content": "李四在技术评审中提出应该使用 BM25 加权而非简单词频匹配", "scope": "work", "kind": "fact", "team_id": "t1", "retention_tier": "long_term", "strength": 0.85, "abstract": "技术决策", "summary": "BM25加权替代词频匹配"},
    {"content": "下周需要完成 Qdrant 向量存储的集成测试", "scope": "work", "kind": "note", "team_id": "t1", "retention_tier": "short_term", "strength": 0.6, "abstract": "待办事项", "summary": "Qdrant集成测试"},
    {"content": "王五演示了新的测试仪表盘UI，团队反馈良好", "scope": "work", "kind": "note", "team_id": "t1", "retention_tier": "standard", "strength": 0.75, "abstract": "UI演示", "summary": "测试仪表盘UI演示反馈良好"},
    {"content": "技术评审会议讨论了混合检索的 RRF 融合策略和权重配置", "scope": "work", "kind": "fact", "team_id": "t1", "retention_tier": "long_term", "strength": 0.88, "abstract": "RRF讨论", "summary": "混合检索RRF权重策略"},
    {"content": "产品经理要求在月底前完成文档处理功能的第一版", "scope": "work", "kind": "note", "team_id": "t1", "retention_tier": "standard", "strength": 0.7, "abstract": "产品需求", "summary": "文档处理月底第一版"},
    {"content": "最近的代码审查发现几个并发安全问题需要修复", "scope": "work", "kind": "note", "team_id": "t1", "retention_tier": "short_term", "strength": 0.65, "abstract": "代码审查", "summary": "并发安全问题"},
    {"content": "团队决定使用 SimpleTokenizer 作为默认分词器，Jieba 作为可选方案", "scope": "work", "kind": "fact", "team_id": "t1", "retention_tier": "permanent", "strength": 0.9, "abstract": "分词器决策", "summary": "Simple默认 Jieba可选"},
    {"content": "赵六上周完成了知识图谱实体抽取的 LLM 集成", "scope": "work", "kind": "note", "team_id": "t1", "retention_tier": "standard", "strength": 0.72, "abstract": "图谱进展", "summary": "LLM实体抽取完成"}
  ],
  "entities": [
    {"name": "张三", "entity_type": "person", "scope": "work", "description": "开发工程师"},
    {"name": "李四", "entity_type": "person", "scope": "work", "description": "技术负责人"},
    {"name": "王五", "entity_type": "person", "scope": "work", "description": "前端工程师"},
    {"name": "赵六", "entity_type": "person", "scope": "work", "description": "AI工程师"},
    {"name": "IClude", "entity_type": "org", "scope": "work", "description": "记忆系统项目"}
  ],
  "relations": [
    {"source": "张三", "target": "IClude", "relation_type": "belongs_to"},
    {"source": "李四", "target": "IClude", "relation_type": "belongs_to"},
    {"source": "王五", "target": "IClude", "relation_type": "belongs_to"},
    {"source": "赵六", "target": "IClude", "relation_type": "belongs_to"}
  ],
  "test_cases": [
    {"name": "人名查找", "query": "张三的任务", "expected_intent": "keyword", "description": "短查询 → keyword"},
    {"name": "时间查询", "query": "最近的会议决策", "expected_intent": "temporal", "description": "最近 → temporal"},
    {"name": "关联查询", "query": "和IClude相关的工作进展", "expected_intent": "relational", "description": "相关 → relational"},
    {"name": "探索查询", "query": "为什么选择BM25而不是简单词频匹配", "expected_intent": "semantic", "description": "为什么 → semantic"},
    {"name": "通用查询", "query": "技术评审 RRF 融合策略 权重 配置方案讨论", "expected_intent": "general", "description": "中等长度 → general"}
  ]
}
```

- [ ] **Step 3: Verify JSON is valid**

Run: `cd "D:/workspace/AI_P/mem0" && python -m json.tool testing/fixtures/tech_knowledge.json > /dev/null && python -m json.tool testing/fixtures/meeting_notes.json > /dev/null && echo "OK"`
Expected: `OK`

- [ ] **Step 4: Commit**

```bash
git add testing/fixtures/
git commit -m "feat: add test fixture datasets for playground"
```

---

### Task 2: Implement TestEnv

**Files:**
- Create: `cmd/test-dashboard/testenv.go`

- [ ] **Step 1: Implement TestEnv struct and Load/Close/Query methods**

Create `cmd/test-dashboard/testenv.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"fmt"
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
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Memories    []FixtureMemory  `json:"memories"`
	Entities    []FixtureEntity  `json:"entities"`
	Relations   []FixtureRelation `json:"relations"`
	TestCases   []TestCaseDef    `json:"test_cases"`
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
	OriginalQuery string            `json:"original_query"`
	SemanticQuery string            `json:"semantic_query"`
	Keywords      []string          `json:"keywords"`
	Entities      []string          `json:"entities"`
	Intent        string            `json:"intent"`
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
	Name           string           `json:"name"`
	Query          string           `json:"query"`
	Description    string           `json:"description"`
	ExpectedIntent string           `json:"expected_intent"`
	ActualIntent   string           `json:"actual_intent"`
	Passed         bool             `json:"passed"`
	ResultCount    int              `json:"result_count"`
	DurationMs     int64            `json:"duration_ms"`
	Preprocess     *PreprocessResult `json:"preprocess"`
	TopResults     []MemoryResult   `json:"top_results"`
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

	cfg := config.Config{
		Storage: config.StorageConfig{
			SQLite: config.SQLiteConfig{
				Enabled: true,
				Path:    dbPath,
				Search: config.SearchConfig{
					BM25Weights: config.BM25WeightsConfig{Content: 10, Abstract: 5, Summary: 3},
				},
				Tokenizer: config.TokenizerConfig{Provider: "simple"},
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
			continue // 跳过失败的记忆，不中断加载
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
		allMems, _ := stores.MemoryStore.List(ctx, "", 0, 1000)
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

	// 各通道独立检索（用于展示分通道结果）
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
		ftsResults, err := e.stores.MemoryStore.SearchText(ctx, ftsQuery, "", limit)
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

	// Graph 通道（通过 retriever 的完整流程获取融合结果）
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
		result.Passed = true // 无期望值时默认通过
	}

	return result
}
```

- [ ] **Step 2: Verify build**

Run: `go build ./cmd/test-dashboard/`
Expected: SUCCESS (may need `go get github.com/google/uuid` if not already a dependency)

- [ ] **Step 3: Commit**

```bash
git add cmd/test-dashboard/testenv.go
git commit -m "feat: add TestEnv for ephemeral dataset loading and query execution"
```

---

### Task 3: Implement REST API handlers

**Files:**
- Create: `cmd/test-dashboard/api.go`

- [ ] **Step 1: Implement API handlers**

Create `cmd/test-dashboard/api.go`:

```go
package main

import (
	"encoding/json"
	"log"
	"net/http"
	"time"
)

// jsonResponse 写入 JSON 响应 / Write JSON response
func jsonResponse(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// jsonError 写入错误响应 / Write error response
func jsonError(w http.ResponseWriter, code int, msg string) {
	jsonResponse(w, code, map[string]any{"error": msg, "code": code})
}

// HandleListDatasets GET /api/datasets
func (e *TestEnv) HandleListDatasets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	datasets, err := e.ListDatasets()
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(w, http.StatusOK, datasets)
}

// HandleLoadDataset POST /api/datasets/load
func (e *TestEnv) HandleLoadDataset(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Name == "" {
		jsonError(w, http.StatusBadRequest, "name is required")
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if err := e.Load(req.Name); err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}

	log.Printf("dataset loaded: %s (%d memories, %d entities)", e.datasetName, e.stats.Memories, e.stats.Entities)
	jsonResponse(w, http.StatusOK, map[string]any{
		"name":  e.datasetName,
		"stats": e.stats,
	})
}

// HandleDatasetStatus GET /api/datasets/status
func (e *TestEnv) HandleDatasetStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.IsLoaded() {
		jsonResponse(w, http.StatusOK, nil)
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{
		"name":  e.datasetName,
		"stats": e.stats,
	})
}

// HandleQuery POST /api/query
func (e *TestEnv) HandleQuery(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var req struct {
		Query string `json:"query"`
		Limit int    `json:"limit"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		jsonError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.IsLoaded() {
		jsonError(w, http.StatusBadRequest, "no dataset loaded")
		return
	}

	result, err := e.Query(req.Query, req.Limit)
	if err != nil {
		jsonError(w, http.StatusInternalServerError, err.Error())
		return
	}
	jsonResponse(w, http.StatusOK, result)
}

// HandleRunCases POST /api/cases/run
func (e *TestEnv) HandleRunCases(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		jsonError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	if !e.IsLoaded() {
		jsonError(w, http.StatusBadRequest, "no dataset loaded")
		return
	}

	start := time.Now()
	var results []*CaseResult
	totalPassed, totalFailed := 0, 0

	for _, tc := range e.testCases {
		cr := e.RunCase(tc)
		results = append(results, cr)
		if cr.Passed {
			totalPassed++
		} else {
			totalFailed++
		}
	}

	jsonResponse(w, http.StatusOK, map[string]any{
		"dataset":     e.datasetName,
		"results":     results,
		"summary":     map[string]int{"total": len(results), "passed": totalPassed, "failed": totalFailed},
		"duration_ms": time.Since(start).Milliseconds(),
	})
}
```

- [ ] **Step 2: Register routes in main.go**

In `cmd/test-dashboard/main.go`, add after `hub := newHub()`:

```go
	fixtureDir := filepath.Join(findProjectRoot(), "testing", "fixtures")
	env := NewTestEnv(fixtureDir)
```

Add route registrations before `log.Printf(...)`:

```go
	http.HandleFunc("/api/datasets", env.HandleListDatasets)
	http.HandleFunc("/api/datasets/load", env.HandleLoadDataset)
	http.HandleFunc("/api/datasets/status", env.HandleDatasetStatus)
	http.HandleFunc("/api/query", env.HandleQuery)
	http.HandleFunc("/api/cases/run", env.HandleRunCases)
```

- [ ] **Step 3: Verify build**

Run: `go build ./cmd/test-dashboard/`
Expected: SUCCESS

- [ ] **Step 4: Manual smoke test**

Run: `./test-dashboard.exe &` then:
```bash
curl http://localhost:3001/api/datasets
curl -X POST http://localhost:3001/api/datasets/load -d '{"name":"tech_knowledge"}'
curl -X POST http://localhost:3001/api/query -d '{"query":"Go goroutine","limit":5}'
curl -X POST http://localhost:3001/api/cases/run
```
Expected: JSON responses with data

- [ ] **Step 5: Commit**

```bash
git add cmd/test-dashboard/api.go cmd/test-dashboard/main.go
git commit -m "feat: add REST API for dataset loading and interactive query"
```

---

### Task 4: Rebuild test-dashboard binary

- [ ] **Step 1: Build new binary**

Run: `cd "D:/workspace/AI_P/mem0" && go build -o test-dashboard.exe ./cmd/test-dashboard/`
Expected: New `test-dashboard.exe` with REST API support

- [ ] **Step 2: Commit binary**

```bash
git add test-dashboard.exe
git commit -m "chore: rebuild test-dashboard binary with REST API"
```

---

## Phase 2: Frontend

### Task 5: Update Vite proxy and add Playground store

**Files:**
- Modify: `tools/test-dashboard-ui/vite.config.ts`
- Create: `tools/test-dashboard-ui/src/stores/playgroundStore.ts`

- [ ] **Step 1: Add /api proxy to vite.config.ts**

```typescript
export default defineConfig({
  plugins: [vue(), tailwindcss()],
  server: {
    port: 5173,
    proxy: {
      '/ws': {
        target: 'ws://localhost:3001',
        ws: true,
      },
      '/api': {
        target: 'http://localhost:3001',
        changeOrigin: true,
      },
    },
  },
})
```

- [ ] **Step 2: Create playgroundStore.ts**

Create `tools/test-dashboard-ui/src/stores/playgroundStore.ts`:

```typescript
import { defineStore } from 'pinia'
import { ref, computed } from 'vue'

export interface DatasetInfo {
  name: string
  file_name: string
  description: string
  stats: { memories: number; entities: number; relations: number; cases: number }
}

export interface PreprocessResult {
  original_query: string
  semantic_query: string
  keywords: string[]
  entities: string[]
  intent: string
  weights: { fts: number; qdrant: number; graph: number }
}

export interface ChannelResult {
  available: boolean
  count: number
  results: MemoryResult[]
}

export interface MemoryResult {
  memory_id: string
  content: string
  score: number
  source: string
  scope?: string
  kind?: string
}

export interface QueryResult {
  preprocess: PreprocessResult | null
  channels: Record<string, ChannelResult>
  merged: MemoryResult[]
  duration_ms: number
}

export interface CaseResult {
  name: string
  query: string
  description: string
  expected_intent: string
  actual_intent: string
  passed: boolean
  result_count: number
  duration_ms: number
  preprocess: PreprocessResult | null
  top_results: MemoryResult[]
}

export interface BatchResult {
  dataset: string
  results: CaseResult[]
  summary: { total: number; passed: number; failed: number }
  duration_ms: number
}

export const usePlaygroundStore = defineStore('playground', () => {
  // 数据集
  const datasets = ref<DatasetInfo[]>([])
  const loadedDataset = ref<{ name: string; stats: DatasetInfo['stats'] } | null>(null)
  const loading = ref(false)

  // 查询
  const queryResult = ref<QueryResult | null>(null)
  const querying = ref(false)

  // 批量测试
  const batchResult = ref<BatchResult | null>(null)
  const batchRunning = ref(false)

  // 选中的 case
  const selectedCase = ref<CaseResult | null>(null)

  const isLoaded = computed(() => loadedDataset.value !== null)

  async function fetchDatasets() {
    const res = await fetch('/api/datasets')
    if (res.ok) {
      datasets.value = await res.json()
    }
  }

  async function loadDataset(fileName: string) {
    loading.value = true
    queryResult.value = null
    batchResult.value = null
    selectedCase.value = null
    try {
      const res = await fetch('/api/datasets/load', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ name: fileName }),
      })
      if (res.ok) {
        loadedDataset.value = await res.json()
      }
    } finally {
      loading.value = false
    }
  }

  async function executeQuery(query: string, limit = 10) {
    querying.value = true
    try {
      const res = await fetch('/api/query', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ query, limit }),
      })
      if (res.ok) {
        queryResult.value = await res.json()
      }
    } finally {
      querying.value = false
    }
  }

  async function runBatchCases() {
    batchRunning.value = true
    selectedCase.value = null
    try {
      const res = await fetch('/api/cases/run', { method: 'POST' })
      if (res.ok) {
        batchResult.value = await res.json()
      }
    } finally {
      batchRunning.value = false
    }
  }

  return {
    datasets, loadedDataset, loading, isLoaded,
    queryResult, querying,
    batchResult, batchRunning,
    selectedCase,
    fetchDatasets, loadDataset, executeQuery, runBatchCases,
  }
})
```

- [ ] **Step 3: Verify build**

Run: `cd tools/test-dashboard-ui && npx vue-tsc --noEmit`
Expected: No type errors

- [ ] **Step 4: Commit**

```bash
git add tools/test-dashboard-ui/vite.config.ts tools/test-dashboard-ui/src/stores/playgroundStore.ts
git commit -m "feat: add playground store and API proxy config"
```

---

### Task 6: Create Playground Vue components

**Files:**
- Create: `tools/test-dashboard-ui/src/components/DatasetSelector.vue`
- Create: `tools/test-dashboard-ui/src/components/BatchCases.vue`
- Create: `tools/test-dashboard-ui/src/components/QueryResult.vue`
- Create: `tools/test-dashboard-ui/src/components/PlaygroundView.vue`

- [ ] **Step 1: Create DatasetSelector.vue**

Create `tools/test-dashboard-ui/src/components/DatasetSelector.vue`:

```vue
<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { usePlaygroundStore } from '../stores/playgroundStore'
import { Database, Loader, CheckCircle } from 'lucide-vue-next'

const store = usePlaygroundStore()
const selectedFile = ref('')

onMounted(() => { store.fetchDatasets() })
</script>

<template>
  <div class="flex items-center gap-4 p-4 bg-gray-900 border-b border-gray-700">
    <Database class="w-5 h-5 text-blue-400" />
    <select v-model="selectedFile"
      class="bg-gray-800 border border-gray-600 rounded px-3 py-1.5 text-sm text-gray-200 min-w-48">
      <option value="" disabled>选择数据集...</option>
      <option v-for="ds in store.datasets" :key="ds.file_name" :value="ds.file_name">
        {{ ds.name }} ({{ ds.stats.memories }}条记忆)
      </option>
    </select>
    <button @click="store.loadDataset(selectedFile)" :disabled="!selectedFile || store.loading"
      class="px-4 py-1.5 bg-blue-600 hover:bg-blue-500 disabled:bg-gray-700 disabled:text-gray-500 rounded text-sm font-medium transition-colors">
      <Loader v-if="store.loading" class="w-4 h-4 animate-spin" />
      <span v-else>Load</span>
    </button>
    <div v-if="store.isLoaded" class="flex items-center gap-2 text-sm text-green-400">
      <CheckCircle class="w-4 h-4" />
      <span>{{ store.loadedDataset?.name }}</span>
      <span class="text-gray-500">
        {{ store.loadedDataset?.stats.memories }}条记忆 /
        {{ store.loadedDataset?.stats.entities }}个实体 /
        {{ store.loadedDataset?.stats.relations }}条关系
      </span>
    </div>
  </div>
</template>
```

- [ ] **Step 2: Create QueryResult.vue**

Create `tools/test-dashboard-ui/src/components/QueryResult.vue`:

```vue
<script setup lang="ts">
import { ref } from 'vue'
import { usePlaygroundStore } from '../stores/playgroundStore'
import { Search, Loader, Zap, Hash, GitBranch } from 'lucide-vue-next'

const store = usePlaygroundStore()
const query = ref('')

function run() {
  if (query.value.trim()) store.executeQuery(query.value.trim())
}

const intentColors: Record<string, string> = {
  keyword: 'text-green-400 bg-green-900/30',
  semantic: 'text-purple-400 bg-purple-900/30',
  temporal: 'text-yellow-400 bg-yellow-900/30',
  relational: 'text-blue-400 bg-blue-900/30',
  general: 'text-gray-400 bg-gray-700/30',
}

function formatScore(s: number) { return s > 0 ? s.toFixed(4) : '-' }
</script>

<template>
  <div class="flex flex-col h-full overflow-auto">
    <!-- 输入栏 -->
    <div class="flex gap-2 p-4 border-b border-gray-700">
      <input v-model="query" @keyup.enter="run" placeholder="输入查询..."
        class="flex-1 bg-gray-800 border border-gray-600 rounded px-3 py-2 text-sm text-gray-200 placeholder-gray-500" />
      <button @click="run" :disabled="!store.isLoaded || store.querying || !query.trim()"
        class="px-4 py-2 bg-emerald-600 hover:bg-emerald-500 disabled:bg-gray-700 disabled:text-gray-500 rounded text-sm font-medium transition-colors flex items-center gap-2">
        <Loader v-if="store.querying" class="w-4 h-4 animate-spin" />
        <Search v-else class="w-4 h-4" />
        执行
      </button>
    </div>

    <div v-if="!store.isLoaded" class="flex-1 flex items-center justify-center text-gray-500 text-sm">
      请先加载数据集
    </div>

    <div v-else-if="store.queryResult" class="flex-1 overflow-auto p-4 space-y-4">
      <!-- 预处理结果 -->
      <div v-if="store.queryResult.preprocess" class="bg-gray-900 rounded-lg p-4 border border-gray-700">
        <div class="flex items-center gap-2 mb-3">
          <Zap class="w-4 h-4 text-amber-400" />
          <span class="text-sm font-medium text-amber-400">Preprocess</span>
          <span class="text-xs text-gray-500">{{ store.queryResult.duration_ms }}ms</span>
        </div>
        <div class="grid grid-cols-2 gap-3 text-sm">
          <div>
            <span class="text-gray-500">Intent:</span>
            <span :class="['ml-2 px-2 py-0.5 rounded text-xs font-medium', intentColors[store.queryResult.preprocess.intent] || 'text-gray-400']">
              {{ store.queryResult.preprocess.intent }}
            </span>
          </div>
          <div>
            <span class="text-gray-500">Keywords:</span>
            <span class="ml-2 text-gray-300">{{ store.queryResult.preprocess.keywords?.join(', ') || '-' }}</span>
          </div>
          <div class="col-span-2">
            <span class="text-gray-500">SemanticQuery:</span>
            <span class="ml-2 text-gray-300">{{ store.queryResult.preprocess.semantic_query }}</span>
          </div>
          <div class="col-span-2">
            <span class="text-gray-500">Weights:</span>
            <span class="ml-2 text-gray-300">
              FTS={{ store.queryResult.preprocess.weights.fts.toFixed(2) }}
              Qdrant={{ store.queryResult.preprocess.weights.qdrant.toFixed(2) }}
              Graph={{ store.queryResult.preprocess.weights.graph.toFixed(2) }}
            </span>
          </div>
          <div v-if="store.queryResult.preprocess.entities?.length" class="col-span-2">
            <span class="text-gray-500">Matched Entities:</span>
            <span class="ml-2 text-gray-300">{{ store.queryResult.preprocess.entities.length }}个</span>
          </div>
        </div>
      </div>

      <!-- 通道结果 -->
      <div class="bg-gray-900 rounded-lg p-4 border border-gray-700">
        <div class="flex items-center gap-2 mb-3">
          <Hash class="w-4 h-4 text-blue-400" />
          <span class="text-sm font-medium text-blue-400">Channels</span>
        </div>
        <div class="flex gap-4 text-sm">
          <div v-for="(ch, name) in store.queryResult.channels" :key="name"
            class="px-3 py-1.5 rounded border"
            :class="ch.available ? 'border-gray-600 text-gray-300' : 'border-gray-800 text-gray-600'">
            <span class="font-medium">{{ name }}</span>
            <span class="ml-2">{{ ch.available ? ch.count + '条' : 'N/A' }}</span>
          </div>
        </div>
      </div>

      <!-- 融合结果 -->
      <div class="bg-gray-900 rounded-lg p-4 border border-gray-700">
        <div class="flex items-center gap-2 mb-3">
          <GitBranch class="w-4 h-4 text-emerald-400" />
          <span class="text-sm font-medium text-emerald-400">Merged (RRF)</span>
          <span class="text-xs text-gray-500">{{ store.queryResult.merged?.length || 0 }}条</span>
        </div>
        <div v-if="store.queryResult.merged?.length" class="space-y-2">
          <div v-for="(item, idx) in store.queryResult.merged" :key="item.memory_id"
            class="flex items-start gap-3 p-2 rounded bg-gray-800/50 text-sm">
            <span class="text-gray-500 font-mono w-6 text-right shrink-0">#{{ idx + 1 }}</span>
            <div class="flex-1 min-w-0">
              <div class="text-gray-200 break-words">{{ item.content }}</div>
              <div class="flex gap-3 mt-1 text-xs text-gray-500">
                <span>score: {{ formatScore(item.score) }}</span>
                <span>source: {{ item.source }}</span>
                <span v-if="item.scope">scope: {{ item.scope }}</span>
                <span v-if="item.kind">kind: {{ item.kind }}</span>
              </div>
            </div>
          </div>
        </div>
        <div v-else class="text-sm text-gray-500">无匹配结果</div>
      </div>
    </div>
  </div>
</template>
```

- [ ] **Step 3: Create BatchCases.vue**

Create `tools/test-dashboard-ui/src/components/BatchCases.vue`:

```vue
<script setup lang="ts">
import { usePlaygroundStore } from '../stores/playgroundStore'
import { Play, Loader, CheckCircle, XCircle, ChevronRight } from 'lucide-vue-next'

const store = usePlaygroundStore()

const intentColors: Record<string, string> = {
  keyword: 'text-green-400',
  semantic: 'text-purple-400',
  temporal: 'text-yellow-400',
  relational: 'text-blue-400',
  general: 'text-gray-400',
}
</script>

<template>
  <div class="flex flex-col h-full border-r border-gray-700 w-72 shrink-0 bg-gray-900">
    <div class="flex items-center justify-between p-3 border-b border-gray-700">
      <span class="text-sm font-medium text-gray-300">Batch Cases</span>
      <button @click="store.runBatchCases()" :disabled="!store.isLoaded || store.batchRunning"
        class="px-3 py-1 bg-blue-600 hover:bg-blue-500 disabled:bg-gray-700 disabled:text-gray-500 rounded text-xs font-medium transition-colors flex items-center gap-1">
        <Loader v-if="store.batchRunning" class="w-3 h-3 animate-spin" />
        <Play v-else class="w-3 h-3" />
        Run All
      </button>
    </div>

    <!-- 摘要 -->
    <div v-if="store.batchResult" class="flex items-center gap-3 px-3 py-2 border-b border-gray-700 text-xs">
      <span class="text-green-400">✓ {{ store.batchResult.summary.passed }}</span>
      <span class="text-red-400">✗ {{ store.batchResult.summary.failed }}</span>
      <span class="text-gray-500">{{ store.batchResult.duration_ms }}ms</span>
    </div>

    <!-- Case 列表 -->
    <div class="flex-1 overflow-auto">
      <div v-if="!store.isLoaded" class="p-4 text-sm text-gray-500 text-center">
        请先加载数据集
      </div>
      <div v-else-if="!store.batchResult" class="p-4 text-sm text-gray-500 text-center">
        点击 Run All 执行测试
      </div>
      <div v-else>
        <div v-for="cr in store.batchResult.results" :key="cr.name"
          @click="store.selectedCase = cr"
          class="flex items-center gap-2 px-3 py-2 cursor-pointer hover:bg-gray-800 border-b border-gray-800 transition-colors"
          :class="store.selectedCase?.name === cr.name ? 'bg-gray-800' : ''">
          <CheckCircle v-if="cr.passed" class="w-4 h-4 text-green-400 shrink-0" />
          <XCircle v-else class="w-4 h-4 text-red-400 shrink-0" />
          <div class="flex-1 min-w-0">
            <div class="text-sm text-gray-200 truncate">{{ cr.name }}</div>
            <div class="flex items-center gap-2 text-xs text-gray-500">
              <span :class="intentColors[cr.actual_intent]">{{ cr.actual_intent }}</span>
              <span>{{ cr.result_count }}条</span>
              <span>{{ cr.duration_ms }}ms</span>
            </div>
          </div>
          <ChevronRight class="w-3 h-3 text-gray-600 shrink-0" />
        </div>
      </div>
    </div>
  </div>
</template>
```

- [ ] **Step 4: Create PlaygroundView.vue**

Create `tools/test-dashboard-ui/src/components/PlaygroundView.vue`:

```vue
<script setup lang="ts">
import DatasetSelector from './DatasetSelector.vue'
import BatchCases from './BatchCases.vue'
import QueryResult from './QueryResult.vue'
</script>

<template>
  <div class="flex flex-col h-full">
    <DatasetSelector />
    <div class="flex flex-1 overflow-hidden">
      <BatchCases />
      <div class="flex-1 overflow-hidden">
        <QueryResult />
      </div>
    </div>
  </div>
</template>
```

- [ ] **Step 5: Commit**

```bash
git add tools/test-dashboard-ui/src/components/DatasetSelector.vue tools/test-dashboard-ui/src/components/QueryResult.vue tools/test-dashboard-ui/src/components/BatchCases.vue tools/test-dashboard-ui/src/components/PlaygroundView.vue
git commit -m "feat: add Playground Vue components"
```

---

### Task 7: Integrate Playground into App.vue

**Files:**
- Modify: `tools/test-dashboard-ui/src/App.vue`

- [ ] **Step 1: Add tab switching to App.vue**

Replace the content of `tools/test-dashboard-ui/src/App.vue`:

```vue
<script setup lang="ts">
import { ref } from 'vue'
import { useTestSocket } from './composables/useTestSocket'
import TopBar from './components/TopBar.vue'
import ProgressBar from './components/ProgressBar.vue'
import TestSidebar from './components/TestSidebar.vue'
import TestDetail from './components/TestDetail.vue'
import TestFlowGraph from './components/TestFlowGraph.vue'
import PlaygroundView from './components/PlaygroundView.vue'

const { connected, runTests, stopTests } = useTestSocket()
const activeTab = ref<'tests' | 'playground'>('tests')
</script>

<template>
  <div class="h-screen flex flex-col bg-gray-950 text-gray-300">
    <!-- Tab 栏 -->
    <div class="flex items-center border-b border-gray-800">
      <button @click="activeTab = 'tests'"
        class="px-6 py-2 text-sm font-medium transition-colors"
        :class="activeTab === 'tests' ? 'text-blue-400 border-b-2 border-blue-400' : 'text-gray-500 hover:text-gray-300'">
        Go Tests
      </button>
      <button @click="activeTab = 'playground'"
        class="px-6 py-2 text-sm font-medium transition-colors"
        :class="activeTab === 'playground' ? 'text-emerald-400 border-b-2 border-emerald-400' : 'text-gray-500 hover:text-gray-300'">
        Playground
      </button>
      <div class="flex-1" />
    </div>

    <!-- Go Tests 视图 (现有) -->
    <template v-if="activeTab === 'tests'">
      <TopBar :connected="connected" @run="runTests()" @stop="stopTests()" />
      <ProgressBar />
      <div class="flex flex-1 overflow-hidden">
        <TestSidebar />
        <main class="flex-1 overflow-auto flex items-center justify-center p-6">
          <TestFlowGraph />
        </main>
        <TestDetail />
      </div>
    </template>

    <!-- Playground 视图 (新增) -->
    <template v-if="activeTab === 'playground'">
      <PlaygroundView />
    </template>
  </div>
</template>
```

- [ ] **Step 2: Verify dev server runs**

Run: `cd tools/test-dashboard-ui && npx vite --port 5173`
Open http://localhost:5173, verify both tabs are visible and switchable.

- [ ] **Step 3: Commit**

```bash
git add tools/test-dashboard-ui/src/App.vue
git commit -m "feat: integrate Playground tab into dashboard app"
```

---

### Task 8: End-to-end verification

- [ ] **Step 1: Start backend**

Run: `./test-dashboard.exe`
Expected: `Test Dashboard server running on :3001`

- [ ] **Step 2: Start frontend**

Run: `cd tools/test-dashboard-ui && npm run dev`

- [ ] **Step 3: Verify Go Tests tab**

Open browser, select "Go Tests" tab, click Run. Verify existing flow graph works.

- [ ] **Step 4: Verify Playground tab**

1. Switch to "Playground" tab
2. Select "技术知识库" from dropdown, click Load
3. Verify status shows "16条记忆 / 9个实体 / 6条关系"
4. Click "Run All" — verify batch cases show pass/fail
5. Type "Go goroutine" in query box, click 执行
6. Verify Preprocess section shows intent=keyword, weights adjusted
7. Verify Merged section shows relevant memories

- [ ] **Step 5: Run go vet and fmt**

Run: `go vet ./... && go fmt ./...`

- [ ] **Step 6: Final commit**

```bash
git add -A
git commit -m "chore: end-to-end verified playground feature"
```
