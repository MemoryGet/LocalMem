package api_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"iclude/internal/api"
	"iclude/internal/config"
	"iclude/internal/document"
	"iclude/internal/memory"
	"iclude/internal/search"
	"iclude/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupFullRouter 初始化全部 store 和 manager / Initialize all stores and managers for full integration test
func setupFullRouter(t *testing.T) (http.Handler, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	s, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, nil)
	require.NoError(t, err)
	err = s.Init(context.Background())
	require.NoError(t, err)

	db := s.DB().(*sql.DB)
	ctxStore := store.NewSQLiteContextStore(db)
	tagStore := store.NewSQLiteTagStore(db)
	graphStore := store.NewSQLiteGraphStore(db)
	docStore := store.NewSQLiteDocumentStore(db)

	mgr := memory.NewManager(s, nil, nil, tagStore, ctxStore, nil, nil, memory.ManagerConfig{})
	ret := search.NewRetriever(s, nil, nil, nil, nil, config.RetrievalConfig{}, nil, nil)
	ctxMgr := memory.NewContextManager(ctxStore)
	graphMgr := memory.NewGraphManager(graphStore)
	docProc := document.NewProcessor(docStore, s, nil, nil, nil, nil)

	router := api.SetupRouter(&api.RouterDeps{
		MemManager:     mgr,
		ContextManager: ctxMgr,
		GraphManager:   graphMgr,
		Retriever:      ret,
		DocProcessor:   docProc,
		TagStore:       tagStore,
	})

	return router, func() { s.Close() }
}

// doRequest 发送 HTTP 请求并解析响应 / Send HTTP request and parse response
func doRequest(t *testing.T, router http.Handler, method, path string, body any) (int, apiResp) {
	t.Helper()
	var reqBody *bytes.Buffer
	if body != nil {
		b, err := json.Marshal(body)
		require.NoError(t, err)
		reqBody = bytes.NewBuffer(b)
	} else {
		reqBody = bytes.NewBuffer(nil)
	}

	w := httptest.NewRecorder()
	req, err := http.NewRequest(method, path, reqBody)
	require.NoError(t, err)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	router.ServeHTTP(w, req)

	var resp apiResp
	if w.Body.Len() > 0 {
		err = json.Unmarshal(w.Body.Bytes(), &resp)
		require.NoError(t, err, "failed to unmarshal response: %s", w.Body.String())
	}
	return w.Code, resp
}

// extractID 从响应中提取 id / Extract id field from response data
func extractID(t *testing.T, resp apiResp) string {
	t.Helper()
	var data map[string]any
	err := json.Unmarshal(resp.Data, &data)
	require.NoError(t, err)
	id, ok := data["id"].(string)
	require.True(t, ok, "id field not found or not string in response: %v", data)
	return id
}

// extractField 从响应中提取指定字段 / Extract a specific field from response data
func extractField(t *testing.T, resp apiResp, field string) any {
	t.Helper()
	var data map[string]any
	err := json.Unmarshal(resp.Data, &data)
	require.NoError(t, err)
	return data[field]
}

// extractFloat 从响应中提取 float64 字段 / Extract float64 field from response data
func extractFloat(t *testing.T, resp apiResp, field string) float64 {
	t.Helper()
	v := extractField(t, resp, field)
	f, ok := v.(float64)
	require.True(t, ok, "field %s not float64: %v (%T)", field, v, v)
	return f
}

func TestFullIntegration(t *testing.T) {
	router, cleanup := setupFullRouter(t)
	defer cleanup()

	// ========== 1. Health check ==========
	t.Log("===== Step 1: Health check =====")
	{
		code, resp := doRequest(t, router, "GET", "/health", nil)
		assert.Equal(t, http.StatusOK, code)
		assert.Equal(t, 0, resp.Code)
		t.Logf("Health check OK: code=%d, message=%s", resp.Code, resp.Message)
	}

	// ========== 2. 创建记忆（多种 retention_tier） ==========
	t.Log("===== Step 2: Create memories with various retention tiers =====")

	type memEntry struct {
		name    string
		id      string
		content string
		tier    string
	}

	// 5 条记忆的参数
	expiresAt := time.Now().UTC().Add(-1 * time.Hour) // 已过期，用于后续 cleanup 测试
	createRequests := []map[string]any{
		{
			"content":        "Go 1.25 新增了迭代器语法",
			"retention_tier": "permanent",
			"kind":           "fact",
			"scope":          "tech",
			"team_id":        "team-test",
		},
		{
			"content":        "IClude 使用 SQLite + Qdrant 混合存储",
			"retention_tier": "long_term",
			"kind":           "fact",
			"scope":          "tech",
			"team_id":        "team-test",
		},
		{
			"content":        "今天的会议讨论了项目进度",
			"retention_tier": "standard",
			"kind":           "note",
			"scope":          "work",
			"team_id":        "team-test",
		},
		{
			"content":        "临时调试日志：连接池超时",
			"retention_tier": "short_term",
			"kind":           "note",
			"scope":          "debug",
			"team_id":        "team-test",
		},
		{
			"content":        "用户刚问了一个关于部署的问题",
			"retention_tier": "ephemeral",
			"kind":           "note",
			"scope":          "chat",
			"team_id":        "team-test",
			"expires_at":     expiresAt,
		},
	}

	tiers := []string{"permanent", "long_term", "standard", "short_term", "ephemeral"}
	memories := make([]memEntry, 5)

	for i, reqBody := range createRequests {
		code, resp := doRequest(t, router, "POST", "/v1/memories", reqBody)
		require.Equal(t, http.StatusCreated, code, "failed to create memory %d", i)
		assert.Equal(t, 0, resp.Code)

		id := extractID(t, resp)
		memories[i] = memEntry{
			name:    tiers[i],
			id:      id,
			content: reqBody["content"].(string),
			tier:    tiers[i],
		}
		t.Logf("  Created [%s] id=%s content=%q", tiers[i], id, reqBody["content"])
	}

	// ========== 3. 列表 + 获取 ==========
	t.Log("===== Step 3: List + Get =====")
	{
		code, resp := doRequest(t, router, "GET", "/v1/memories?team_id=team-test&limit=10", nil)
		assert.Equal(t, http.StatusOK, code)

		var list []map[string]any
		json.Unmarshal(resp.Data, &list)
		t.Logf("  List returned %d memories", len(list))
		assert.GreaterOrEqual(t, len(list), 5)

		// Get 单条
		code, resp = doRequest(t, router, "GET", "/v1/memories/"+memories[0].id, nil)
		assert.Equal(t, http.StatusOK, code)
		content := extractField(t, resp, "content")
		assert.Equal(t, memories[0].content, content)
		t.Logf("  Get memory[0]: content=%q", content)
	}

	// ========== 4. 更新记忆 ==========
	t.Log("===== Step 4: Update memory =====")
	{
		updateBody := map[string]any{
			"content": "Go 1.25 新增了迭代器语法和增强的泛型支持",
			"kind":    "skill",
		}
		code, resp := doRequest(t, router, "PUT", "/v1/memories/"+memories[0].id, updateBody)
		assert.Equal(t, http.StatusOK, code)
		assert.Equal(t, 0, resp.Code)

		updatedContent := extractField(t, resp, "content")
		updatedKind := extractField(t, resp, "kind")
		t.Logf("  Updated: content=%q kind=%v", updatedContent, updatedKind)
		assert.Equal(t, "Go 1.25 新增了迭代器语法和增强的泛型支持", updatedContent)
	}

	// ========== 5. BM25 全文检索 ==========
	t.Log("===== Step 5: BM25 full-text search =====")
	{
		// 搜索 "Go" — FTS5 unicode61 分词器对英文词效果好
		code, resp := doRequest(t, router, "POST", "/v1/retrieve", map[string]any{
			"query": "Go",
			"limit": 10,
		})
		assert.Equal(t, http.StatusOK, code)

		var retrieveResp map[string]any
		json.Unmarshal(resp.Data, &retrieveResp)
		results, _ := retrieveResp["results"].([]any)
		t.Logf("  Search 'Go': %d results", len(results))
		for i, r := range results {
			rm := r.(map[string]any)
			mem := rm["memory"].(map[string]any)
			t.Logf("    [%d] score=%.4f content=%q", i, rm["score"], mem["content"])
		}
		assert.GreaterOrEqual(t, len(results), 1, "should find at least 1 result for 'Go'")

		// 搜索 "SQLite"
		code, resp = doRequest(t, router, "POST", "/v1/retrieve", map[string]any{
			"query": "SQLite",
			"limit": 10,
		})
		assert.Equal(t, http.StatusOK, code)

		json.Unmarshal(resp.Data, &retrieveResp)
		results, _ = retrieveResp["results"].([]any)
		t.Logf("  Search 'SQLite': %d results", len(results))
		for i, r := range results {
			rm := r.(map[string]any)
			mem := rm["memory"].(map[string]any)
			t.Logf("    [%d] score=%.4f content=%q", i, rm["score"], mem["content"])
		}
		assert.GreaterOrEqual(t, len(results), 1, "should find at least 1 result for 'SQLite'")
	}

	// ========== 6. 软删除 + 恢复 ==========
	t.Log("===== Step 6: Soft delete + Restore =====")
	targetID := memories[2].id // "今天的会议讨论了项目进度"
	{
		// 软删除
		code, _ := doRequest(t, router, "DELETE", "/v1/memories/"+targetID+"/soft", nil)
		assert.Equal(t, http.StatusOK, code)
		t.Logf("  Soft-deleted id=%s", targetID)

		// 获取应返回 404
		code, _ = doRequest(t, router, "GET", "/v1/memories/"+targetID, nil)
		assert.Equal(t, http.StatusNotFound, code)
		t.Logf("  Get after soft-delete: 404 (expected)")

		// 恢复
		code, _ = doRequest(t, router, "POST", "/v1/memories/"+targetID+"/restore", nil)
		assert.Equal(t, http.StatusOK, code)
		t.Logf("  Restored id=%s", targetID)

		// 恢复后应能获取
		code, resp := doRequest(t, router, "GET", "/v1/memories/"+targetID, nil)
		assert.Equal(t, http.StatusOK, code)
		restoredContent := extractField(t, resp, "content")
		t.Logf("  Get after restore: content=%q", restoredContent)
		assert.Equal(t, memories[2].content, restoredContent)
	}

	// ========== 7. 强化记忆 ==========
	t.Log("===== Step 7: Reinforce memory =====")
	reinforceID := memories[1].id // "IClude 使用 SQLite + Qdrant 混合存储"
	{
		// 先将 strength 降低到 0.5，以便观察 reinforce 效果
		// strength = 1.0 时 delta=0.1*(1-1)=0，不会变化
		updateBody := map[string]any{"strength": 0.5}
		code, _ := doRequest(t, router, "PUT", "/v1/memories/"+reinforceID, updateBody)
		require.Equal(t, http.StatusOK, code)

		// 获取初始 strength
		_, resp := doRequest(t, router, "GET", "/v1/memories/"+reinforceID, nil)
		initialStrength := extractFloat(t, resp, "strength")
		initialCount := extractFloat(t, resp, "reinforced_count")
		t.Logf("  Before reinforce: strength=%.4f reinforced_count=%.0f", initialStrength, initialCount)

		// 强化 3 次
		for i := 0; i < 3; i++ {
			code, _ := doRequest(t, router, "POST", "/v1/memories/"+reinforceID+"/reinforce", nil)
			assert.Equal(t, http.StatusOK, code)
		}

		// 验证 strength 递增
		_, resp = doRequest(t, router, "GET", "/v1/memories/"+reinforceID, nil)
		finalStrength := extractFloat(t, resp, "strength")
		finalCount := extractFloat(t, resp, "reinforced_count")
		t.Logf("  After 3x reinforce: strength=%.4f reinforced_count=%.0f", finalStrength, finalCount)
		assert.Greater(t, finalStrength, initialStrength, "strength should increase after reinforce")
		assert.Equal(t, initialCount+3, finalCount, "reinforced_count should increase by 3")
	}

	// ========== 8. 对话摄取 ==========
	t.Log("===== Step 8: Conversation ingest =====")
	var conversationContextID string
	{
		reqBody := map[string]any{
			"provider":    "generic",
			"external_id": "conv-001",
			"scope":       "chat",
			"messages": []map[string]any{
				{"role": "user", "content": "IClude 支持哪些存储后端？", "turn_number": 1},
				{"role": "assistant", "content": "IClude 支持 SQLite 用于结构化存储和全文搜索，以及 Qdrant 用于向量语义搜索。两者可以独立使用或组合使用。", "turn_number": 2},
				{"role": "user", "content": "混合模式下检索结果如何合并？", "turn_number": 3},
			},
			"metadata": map[string]any{"session": "demo"},
		}

		code, resp := doRequest(t, router, "POST", "/v1/conversations", reqBody)
		require.Equal(t, http.StatusCreated, code)

		var data map[string]any
		json.Unmarshal(resp.Data, &data)
		conversationContextID = fmt.Sprintf("%v", data["context_id"])
		count := data["count"]
		t.Logf("  Ingested conversation: context_id=%s count=%v", conversationContextID, count)
		assert.NotEmpty(t, conversationContextID)
	}

	// ========== 9. 获取对话 ==========
	t.Log("===== Step 9: Get conversation =====")
	{
		code, resp := doRequest(t, router, "GET", "/v1/conversations/"+conversationContextID, nil)
		assert.Equal(t, http.StatusOK, code)

		var data map[string]any
		json.Unmarshal(resp.Data, &data)
		convMems, _ := data["memories"].([]any)
		t.Logf("  Conversation has %d messages", len(convMems))
		assert.Equal(t, 3, len(convMems), "should have 3 conversation messages")

		// 验证按 turn_number 排序
		for i, m := range convMems {
			mem := m.(map[string]any)
			t.Logf("    [turn %v] role=%v content=%q", mem["turn_number"], mem["message_role"], mem["content"])
			if i > 0 {
				prevTurn := convMems[i-1].(map[string]any)["turn_number"].(float64)
				currTurn := mem["turn_number"].(float64)
				assert.LessOrEqual(t, prevTurn, currTurn, "turn_number should be ordered")
			}
		}
	}

	// ========== 10. Context 树 ==========
	t.Log("===== Step 10: Context tree =====")
	var parentCtxID, childCtxID string
	{
		// 创建父节点
		code, resp := doRequest(t, router, "POST", "/v1/contexts", map[string]any{
			"name":        "项目根节点",
			"kind":        "project",
			"scope":       "tech",
			"description": "IClude 项目主上下文",
		})
		require.Equal(t, http.StatusCreated, code)
		parentCtxID = extractID(t, resp)
		t.Logf("  Created parent context: id=%s", parentCtxID)

		// 创建子节点
		code, resp = doRequest(t, router, "POST", "/v1/contexts", map[string]any{
			"name":        "存储模块",
			"parent_id":   parentCtxID,
			"kind":        "topic",
			"scope":       "tech",
			"description": "存储层相关话题",
		})
		require.Equal(t, http.StatusCreated, code)
		childCtxID = extractID(t, resp)
		t.Logf("  Created child context: id=%s parent=%s", childCtxID, parentCtxID)

		// 获取子节点列表
		code, resp = doRequest(t, router, "GET", "/v1/contexts/"+parentCtxID+"/children", nil)
		assert.Equal(t, http.StatusOK, code)
		var children []map[string]any
		json.Unmarshal(resp.Data, &children)
		t.Logf("  Children of parent: %d", len(children))
		assert.GreaterOrEqual(t, len(children), 1)

		// 获取子树
		code, resp = doRequest(t, router, "GET", "/v1/contexts/"+parentCtxID+"/tree", nil)
		assert.Equal(t, http.StatusOK, code)
		var tree []map[string]any
		json.Unmarshal(resp.Data, &tree)
		t.Logf("  Subtree of parent: %d nodes", len(tree))
		assert.GreaterOrEqual(t, len(tree), 1)
	}

	// ========== 11. 标签系统 ==========
	t.Log("===== Step 11: Tag system =====")
	var tagID string
	{
		// 创建标签
		code, resp := doRequest(t, router, "POST", "/v1/tags", map[string]any{
			"name":  "golang",
			"scope": "tech",
		})
		require.Equal(t, http.StatusCreated, code)
		tagID = extractID(t, resp)
		t.Logf("  Created tag: id=%s name=golang", tagID)

		// 给记忆打标签
		code, _ = doRequest(t, router, "POST", "/v1/memories/"+memories[0].id+"/tags", map[string]any{
			"tag_id": tagID,
		})
		assert.Equal(t, http.StatusOK, code)
		t.Logf("  Tagged memory %s with tag %s", memories[0].id, tagID)

		// 获取记忆标签
		code, resp = doRequest(t, router, "GET", "/v1/memories/"+memories[0].id+"/tags", nil)
		assert.Equal(t, http.StatusOK, code)
		var tags []map[string]any
		json.Unmarshal(resp.Data, &tags)
		t.Logf("  Memory tags: %d tags", len(tags))
		assert.GreaterOrEqual(t, len(tags), 1)
		assert.Equal(t, "golang", tags[0]["name"])

		// 列出所有标签
		code, resp = doRequest(t, router, "GET", "/v1/tags?scope=tech", nil)
		assert.Equal(t, http.StatusOK, code)
		json.Unmarshal(resp.Data, &tags)
		t.Logf("  All tags in scope=tech: %d", len(tags))
	}

	// ========== 12. 知识图谱 ==========
	t.Log("===== Step 12: Knowledge graph =====")
	var entityGoID, entitySQLiteID, relationID string
	{
		// 创建实体: Go
		code, resp := doRequest(t, router, "POST", "/v1/entities", map[string]any{
			"name":        "Go",
			"entity_type": "tool",
			"scope":       "tech",
			"description": "Go 编程语言",
		})
		require.Equal(t, http.StatusCreated, code)
		entityGoID = extractID(t, resp)
		t.Logf("  Created entity: id=%s name=Go", entityGoID)

		// 创建实体: SQLite
		code, resp = doRequest(t, router, "POST", "/v1/entities", map[string]any{
			"name":        "SQLite",
			"entity_type": "tool",
			"scope":       "tech",
			"description": "嵌入式关系数据库",
		})
		require.Equal(t, http.StatusCreated, code)
		entitySQLiteID = extractID(t, resp)
		t.Logf("  Created entity: id=%s name=SQLite", entitySQLiteID)

		// 创建关系: Go -> uses -> SQLite
		code, resp = doRequest(t, router, "POST", "/v1/entity-relations", map[string]any{
			"source_id":     entityGoID,
			"target_id":     entitySQLiteID,
			"relation_type": "uses",
			"weight":        0.9,
		})
		require.Equal(t, http.StatusCreated, code)
		relationID = extractID(t, resp)
		t.Logf("  Created relation: id=%s Go --uses--> SQLite", relationID)

		// 创建记忆-实体关联
		code, _ = doRequest(t, router, "POST", "/v1/memory-entities", map[string]any{
			"memory_id": memories[0].id,
			"entity_id": entityGoID,
			"role":      "subject",
		})
		assert.Equal(t, http.StatusCreated, code)
		t.Logf("  Linked memory %s to entity %s (subject)", memories[0].id, entityGoID)

		// 获取实体关系
		code, resp = doRequest(t, router, "GET", "/v1/entities/"+entityGoID+"/relations", nil)
		assert.Equal(t, http.StatusOK, code)
		var relations []map[string]any
		json.Unmarshal(resp.Data, &relations)
		t.Logf("  Entity Go relations: %d", len(relations))
		assert.GreaterOrEqual(t, len(relations), 1)

		// 列出所有实体
		code, resp = doRequest(t, router, "GET", "/v1/entities?scope=tech", nil)
		assert.Equal(t, http.StatusOK, code)
		var entities []map[string]any
		json.Unmarshal(resp.Data, &entities)
		t.Logf("  Entities in scope=tech: %d", len(entities))
		assert.GreaterOrEqual(t, len(entities), 2)
	}

	// ========== 13. 过期清理 ==========
	t.Log("===== Step 13: Cleanup expired memories =====")
	{
		// ephemeral 记忆设置了过期时间（1 小时前），应被清理
		code, resp := doRequest(t, router, "POST", "/v1/maintenance/cleanup", nil)
		assert.Equal(t, http.StatusOK, code)

		var data map[string]any
		json.Unmarshal(resp.Data, &data)
		cleaned := data["cleaned"]
		t.Logf("  Cleanup result: cleaned=%v", cleaned)

		// 验证 ephemeral 记忆已被清理
		code, _ = doRequest(t, router, "GET", "/v1/memories/"+memories[4].id, nil)
		t.Logf("  Ephemeral memory after cleanup: status=%d (expect 404)", code)
		assert.Equal(t, http.StatusNotFound, code, "expired ephemeral memory should be cleaned up")
	}

	// ========== 14. 时间线 ==========
	t.Log("===== Step 14: Timeline =====")
	{
		code, resp := doRequest(t, router, "GET", "/v1/timeline?limit=20", nil)
		assert.Equal(t, http.StatusOK, code)

		var timeline []map[string]any
		json.Unmarshal(resp.Data, &timeline)
		t.Logf("  Timeline entries: %d", len(timeline))
		assert.GreaterOrEqual(t, len(timeline), 1, "timeline should have entries")

		for i, entry := range timeline {
			t.Logf("    [%d] id=%v content=%q", i, entry["id"], entry["content"])
		}
	}

	t.Log("===== All integration tests passed =====")
}
