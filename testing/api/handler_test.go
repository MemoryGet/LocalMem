package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"iclude/internal/api"
	"iclude/internal/config"
	"iclude/internal/memory"
	"iclude/internal/search"
	"iclude/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type apiResp struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

func setupRouter(t *testing.T) (http.Handler, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	s, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, nil)
	require.NoError(t, err)

	err = s.Init(context.Background())
	require.NoError(t, err)

	mgr := memory.NewManager(s, nil, nil, nil, nil, nil, nil, memory.ManagerConfig{})
	ret := search.NewRetriever(s, nil, nil, nil, nil, config.RetrievalConfig{}, nil, nil)
	router := api.SetupRouter(&api.RouterDeps{
		MemManager: mgr,
		Retriever:  ret,
	})

	return router, func() {
		s.Close()
		os.RemoveAll(dir)
	}
}

func TestHealthEndpoint(t *testing.T) {
	router, cleanup := setupRouter(t)
	defer cleanup()

	w := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/health", nil)
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp apiResp
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, 0, resp.Code)
}

func TestMemoryCRUD(t *testing.T) {
	router, cleanup := setupRouter(t)
	defer cleanup()

	// Create
	body := `{"content":"integration test memory","team_id":"t1"}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/memories", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	var createResp apiResp
	json.Unmarshal(w.Body.Bytes(), &createResp)
	assert.Equal(t, 0, createResp.Code)

	var mem map[string]any
	json.Unmarshal(createResp.Data, &mem)
	memID := mem["id"].(string)
	assert.NotEmpty(t, memID)

	// Get
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/v1/memories/"+memID, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// Update
	updateBody := `{"content":"updated content"}`
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("PUT", "/v1/memories/"+memID, bytes.NewBufferString(updateBody))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// List
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/v1/memories?team_id=t1", nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// Delete
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("DELETE", "/v1/memories/"+memID, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)

	// Get after delete → 404
	w = httptest.NewRecorder()
	req, _ = http.NewRequest("GET", "/v1/memories/"+memID, nil)
	router.ServeHTTP(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestCreateMemory_InvalidInput(t *testing.T) {
	router, cleanup := setupRouter(t)
	defer cleanup()

	tests := []struct {
		name string
		body string
		code int
	}{
		{
			name: "empty body",
			body: `{}`,
			code: http.StatusBadRequest,
		},
		{
			name: "invalid json",
			body: `{invalid`,
			code: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req, _ := http.NewRequest("POST", "/v1/memories", bytes.NewBufferString(tt.body))
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)
			assert.Equal(t, tt.code, w.Code)
		})
	}
}

func TestRetrieve(t *testing.T) {
	router, cleanup := setupRouter(t)
	defer cleanup()

	// 先创建几条记忆
	for _, content := range []string{"Go programming language", "Python scripting", "Rust systems programming"} {
		body, _ := json.Marshal(map[string]string{"content": content})
		w := httptest.NewRecorder()
		req, _ := http.NewRequest("POST", "/v1/memories", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(w, req)
		require.Equal(t, http.StatusCreated, w.Code)
	}

	// 检索
	searchBody := `{"query":"programming","limit":10}`
	w := httptest.NewRecorder()
	req, _ := http.NewRequest("POST", "/v1/retrieve", bytes.NewBufferString(searchBody))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	var resp apiResp
	json.Unmarshal(w.Body.Bytes(), &resp)
	assert.Equal(t, 0, resp.Code)
}
