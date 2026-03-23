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

// HandleListDatasets GET /api/datasets — 列出所有数据集 / List all fixture datasets
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

// HandleLoadDataset POST /api/datasets/load — 加载指定数据集 / Load specified dataset
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

// HandleDatasetStatus GET /api/datasets/status — 当前数据集状态 / Current dataset status
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

// HandleQuery POST /api/query — 执行查询 / Execute query
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

// HandleRunCases POST /api/cases/run — 批量执行测试用例 / Run all test cases
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
