package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"iclude/internal/api"
	"iclude/internal/config"
	"iclude/internal/memory"
	"iclude/internal/search"
	"iclude/internal/store"
)

type dataset struct {
	SeedMemories []seedMemory `json:"seed_memories"`
	TestQueries  []testQuery  `json:"test_queries"`
}

type seedMemory struct {
	Content string `json:"content"`
	Kind    string `json:"kind"`
	SubKind string `json:"sub_kind"`
}

type testQuery []any

type benchmarkResult struct {
	Index       int     `json:"index"`
	Query       string  `json:"query"`
	Expected    string  `json:"expected"`
	Category    string  `json:"category"`
	Difficulty  string  `json:"difficulty"`
	Hit         bool    `json:"hit"`
	Rank        int     `json:"rank"`
	Score       float64 `json:"score"`
	ResultCount int     `json:"result_count"`
}

type apiEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type createMemoryResponse struct {
	ID string `json:"id"`
}

type retrieveResponse struct {
	Results []*searchResult `json:"results"`
}

type searchResult struct {
	Memory *retrievedMemory `json:"memory"`
	Score  float64          `json:"score"`
	Source string           `json:"source"`
}

type retrievedMemory struct {
	Content  string `json:"content"`
	Excerpt string `json:"excerpt"`
}

type summary struct {
	RerankMode string             `json:"rerank_mode"`
	Total      int                `json:"total"`
	Hits       int                `json:"hits"`
	HitRate    float64            `json:"hit_rate"`
	Top1Rate   float64            `json:"top1_rate"`
	Top3Rate   float64            `json:"top3_rate"`
	Top5Rate   float64            `json:"top5_rate"`
	ByCategory map[string]float64 `json:"by_category"`
}

func main() {
	rerankMode := flag.String("rerank", "off", "rerank mode: off|overlap|remote")
	datasetPath := flag.String("dataset-script", "tools/retrieval_test_500.py", "path to benchmark dataset script")
	configPath := flag.String("config", "", "optional path to config.yaml")
	rerankBaseURL := flag.String("rerank-base-url", "", "override retrieval.rerank.base_url")
	rerankAPIKey := flag.String("rerank-api-key", "", "override retrieval.rerank.api_key")
	rerankModel := flag.String("rerank-model", "", "override retrieval.rerank.model")
	flag.Parse()

	if *configPath != "" {
		_ = os.Setenv("ICLUDE_CONFIG_PATH", *configPath)
	}
	if err := config.LoadConfig(); err != nil {
		panic(fmt.Errorf("load config: %w", err))
	}
	appCfg := config.GetConfig()

	ds, err := loadDataset(*datasetPath)
	if err != nil {
		panic(fmt.Errorf("load dataset: %w", err))
	}

	router, cleanup, err := setupRouter(*rerankMode, appCfg.Retrieval, rerankOverrides{
		BaseURL: strings.TrimSpace(*rerankBaseURL),
		APIKey:  strings.TrimSpace(*rerankAPIKey),
		Model:   strings.TrimSpace(*rerankModel),
	})
	if err != nil {
		panic(fmt.Errorf("setup router: %w", err))
	}
	defer cleanup()

	fmt.Printf("LocalMem in-process retrieval benchmark\n")
	fmt.Printf("Rerank mode: %s\n", *rerankMode)
	fmt.Printf("Seed memories: %d\n", len(ds.SeedMemories))
	fmt.Printf("Test queries: %d\n\n", len(ds.TestQueries))

	created := 0
	for i, mem := range ds.SeedMemories {
		ok, err := createMemoryViaAPI(router, mem, i)
		if err != nil {
			fmt.Printf("WARN create failed (%d): %v\n", i+1, err)
			continue
		}
		if ok {
			created++
		}
	}
	fmt.Printf("Seeded: %d/%d\n", created, len(ds.SeedMemories))

	start := time.Now()
	results := make([]benchmarkResult, 0, len(ds.TestQueries))
	for i, tq := range ds.TestQueries {
		query, expected, category, difficulty, ok := parseTestQuery(tq)
		if !ok {
			continue
		}
		searchResults, err := retrieveViaAPI(router, query, *rerankMode, i)
		if err != nil {
			fmt.Printf("WARN retrieve failed (%d): %v\n", i+1, err)
		}
		hit, rank, score := checkHit(searchResults, expected)
		results = append(results, benchmarkResult{
			Index:       i + 1,
			Query:       query,
			Expected:    expected,
			Category:    category,
			Difficulty:  difficulty,
			Hit:         hit,
			Rank:        rank,
			Score:       score,
			ResultCount: len(searchResults),
		})
		if (i+1)%50 == 0 {
			hits := 0
			for _, r := range results {
				if r.Hit {
					hits++
				}
			}
			fmt.Printf("%d/%d — hit %d/%d (%.1f%%) — %.1fs\n", i+1, len(ds.TestQueries), hits, i+1, float64(hits)*100/float64(i+1), time.Since(start).Seconds())
		}
	}

	s := summarize(results, *rerankMode)
	out, _ := json.MarshalIndent(s, "", "  ")
	fmt.Printf("\n%s\n", string(out))
}

func loadDataset(scriptPath string) (*dataset, error) {
	cmd := exec.Command("python3", scriptPath, "--dump-dataset")
	cmd.Dir = repoRootFromPath(scriptPath)
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}
	var ds dataset
	if err := json.Unmarshal(output, &ds); err != nil {
		return nil, err
	}
	return &ds, nil
}

func repoRootFromPath(scriptPath string) string {
	dir := filepath.Dir(scriptPath)
	if dir == "." {
		return "."
	}
	return "."
}

type rerankOverrides struct {
	BaseURL string
	APIKey  string
	Model   string
}

func setupRouter(rerankMode string, retrievalCfg config.RetrievalConfig, overrides rerankOverrides) (http.Handler, func(), error) {
	tmpDir, err := os.MkdirTemp("", "iclude-bench-*")
	if err != nil {
		return nil, nil, err
	}
	dbPath := filepath.Join(tmpDir, "benchmark.db")

	memStore, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, nil)
	if err != nil {
		return nil, nil, err
	}
	if err := memStore.Init(context.Background()); err != nil {
		return nil, nil, err
	}

	cfg := retrievalCfg
	cfg.Preprocess.Enabled = true
	cfg.Rerank.Enabled = rerankMode != "off"
	cfg.Rerank.Provider = rerankMode
	if cfg.Rerank.TopK <= 0 {
		cfg.Rerank.TopK = 20
	}
	if cfg.Rerank.ScoreWeight <= 0 {
		cfg.Rerank.ScoreWeight = 0.7
	}
	if overrides.BaseURL != "" {
		cfg.Rerank.BaseURL = overrides.BaseURL
	}
	if overrides.APIKey != "" {
		cfg.Rerank.APIKey = overrides.APIKey
	}
	if overrides.Model != "" {
		cfg.Rerank.Model = overrides.Model
	}
	if rerankMode == "remote" && strings.TrimSpace(cfg.Rerank.BaseURL) == "" {
		return nil, nil, fmt.Errorf("rerank mode remote requires retrieval.rerank.base_url or --rerank-base-url")
	}

	memMgr := memory.NewManager(memStore, nil, nil, nil, nil, nil, nil, memory.ManagerConfig{})
	ret := search.NewRetriever(memStore, nil, nil, nil, nil, cfg, nil, nil)
	router := api.SetupRouter(&api.RouterDeps{
		MemManager:         memMgr,
		Retriever:          ret,
		AuthConfig:         config.AuthConfig{Enabled: false},
		CORSAllowedOrigins: []string{"*"},
	})

	cleanup := func() {
		_ = memStore.Close()
		_ = os.RemoveAll(tmpDir)
	}
	return router, cleanup, nil
}

func createMemoryViaAPI(router http.Handler, mem seedMemory, seq int) (bool, error) {
	payload := map[string]any{
		"content":  mem.Content,
		"kind":     fallback(mem.Kind, "note"),
		"sub_kind": mem.SubKind,
		"scope":    "user/test",
	}
	body, _ := json.Marshal(payload)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/memories", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = benchmarkRemoteAddr(seq)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated && rec.Code != http.StatusOK {
		return false, fmt.Errorf("status=%d body=%s", rec.Code, truncate(rec.Body.String(), 120))
	}

	var resp apiEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return false, err
	}
	var created createMemoryResponse
	if err := json.Unmarshal(resp.Data, &created); err != nil {
		return false, err
	}
	return created.ID != "", nil
}

func retrieveViaAPI(router http.Handler, query string, rerankMode string, seq int) ([]*searchResult, error) {
	payload := map[string]any{
		"query":          query,
		"limit":          10,
		"rerank_enabled": rerankMode != "off",
	}
	if rerankMode != "off" {
		payload["rerank_provider"] = rerankMode
	}
	body, _ := json.Marshal(payload)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/retrieve", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.RemoteAddr = benchmarkRemoteAddr(seq)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		return nil, fmt.Errorf("status=%d body=%s", rec.Code, truncate(rec.Body.String(), 120))
	}

	var resp apiEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		return nil, err
	}
	var data retrieveResponse
	if err := json.Unmarshal(resp.Data, &data); err != nil {
		return nil, err
	}
	return data.Results, nil
}

func parseTestQuery(tq testQuery) (query, expected, category, difficulty string, ok bool) {
	if len(tq) != 4 {
		return "", "", "", "", false
	}
	query, _ = tq[0].(string)
	expected, _ = tq[1].(string)
	category, _ = tq[2].(string)
	difficulty, _ = tq[3].(string)
	return query, expected, category, difficulty, query != ""
}

func checkHit(results []*searchResult, expected string) (bool, int, float64) {
	for i, r := range results {
		if r == nil || r.Memory == nil {
			continue
		}
		content := strings.ToLower(r.Memory.Content)
		excerpt := strings.ToLower(r.Memory.Excerpt)
		target := strings.ToLower(expected)
		if strings.Contains(content, target) || strings.Contains(excerpt, target) {
			return true, i + 1, r.Score
		}
	}
	return false, -1, 0
}

func summarize(results []benchmarkResult, rerankMode string) summary {
	total := len(results)
	hits := 0
	top1 := 0
	top3 := 0
	top5 := 0
	byCategoryCounts := map[string][2]int{}

	for _, r := range results {
		stats := byCategoryCounts[r.Category]
		stats[0]++
		if r.Hit {
			hits++
			stats[1]++
			if r.Rank == 1 {
				top1++
			}
			if r.Rank > 0 && r.Rank <= 3 {
				top3++
			}
			if r.Rank > 0 && r.Rank <= 5 {
				top5++
			}
		}
		byCategoryCounts[r.Category] = stats
	}

	byCategory := make(map[string]float64, len(byCategoryCounts))
	cats := make([]string, 0, len(byCategoryCounts))
	for cat := range byCategoryCounts {
		cats = append(cats, cat)
	}
	sort.Strings(cats)
	for _, cat := range cats {
		stats := byCategoryCounts[cat]
		if stats[0] == 0 {
			byCategory[cat] = 0
			continue
		}
		byCategory[cat] = float64(stats[1]) * 100 / float64(stats[0])
	}

	return summary{
		RerankMode: rerankMode,
		Total:      total,
		Hits:       hits,
		HitRate:    pct(hits, total),
		Top1Rate:   pct(top1, total),
		Top3Rate:   pct(top3, total),
		Top5Rate:   pct(top5, total),
		ByCategory: byCategory,
	}
}

func pct(n, d int) float64 {
	if d == 0 {
		return 0
	}
	return float64(n) * 100 / float64(d)
}

func fallback(v, def string) string {
	if strings.TrimSpace(v) == "" {
		return def
	}
	return v
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

func benchmarkRemoteAddr(seq int) string {
	return fmt.Sprintf("198.51.%d.%d:12345", (seq/250)%250, seq%250+1)
}
