package api_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLLMConversation_OpenAI 模拟 OpenAI 标准对话摄取 / Simulate OpenAI standard conversation ingest
func TestLLMConversation_OpenAI(t *testing.T) {
	router, cleanup := setupFullRouter(t)
	defer cleanup()

	tests := []struct {
		name     string
		request  map[string]any
		wantCode int
		validate func(t *testing.T, resp apiResp)
	}{
		{
			name: "basic_multi_turn",
			request: map[string]any{
				"provider":    "openai",
				"external_id": "chatcmpl-abc123",
				"scope":       "agent/chatbot",
				"messages": []map[string]any{
					{"role": "system", "content": "You are a helpful assistant.", "turn_number": 1},
					{"role": "user", "content": "What is the capital of France?", "turn_number": 2},
					{"role": "assistant", "content": "The capital of France is Paris.", "turn_number": 3},
					{"role": "user", "content": "What about Germany?", "turn_number": 4},
					{"role": "assistant", "content": "The capital of Germany is Berlin.", "turn_number": 5},
				},
				"metadata": map[string]any{
					"model":        "gpt-4o",
					"temperature":  0.7,
					"total_tokens": 156,
				},
			},
			wantCode: http.StatusCreated,
			validate: func(t *testing.T, resp apiResp) {
				var data map[string]any
				require.NoError(t, json.Unmarshal(resp.Data, &data))
				assert.NotEmpty(t, data["context_id"])
				assert.Equal(t, float64(5), data["count"])
			},
		},
		{
			name: "with_tool_calls",
			request: map[string]any{
				"provider":    "openai-tools",
				"external_id": "chatcmpl-tool-001",
				"scope":       "agent/assistant-tools",
				"messages": []map[string]any{
					{
						"role":    "user",
						"content": "What's the weather in Beijing?",
					},
					{
						"role":    "assistant",
						"content": "Let me check the weather for you.",
						"metadata": map[string]any{
							"tool_calls": []map[string]any{
								{
									"id":       "call_abc123",
									"type":     "function",
									"function": map[string]any{"name": "get_weather", "arguments": `{"city":"Beijing"}`},
								},
							},
							"finish_reason": "tool_calls",
						},
					},
					{
						"role":    "tool",
						"content": `{"temperature": 22, "condition": "sunny", "humidity": 45}`,
						"metadata": map[string]any{
							"tool_call_id": "call_abc123",
							"name":         "get_weather",
						},
					},
					{
						"role":    "assistant",
						"content": "The weather in Beijing is sunny with a temperature of 22°C and humidity at 45%.",
						"metadata": map[string]any{
							"finish_reason": "stop",
						},
					},
				},
				"metadata": map[string]any{
					"model":             "gpt-4o",
					"prompt_tokens":     85,
					"completion_tokens": 42,
					"total_tokens":      127,
				},
			},
			wantCode: http.StatusCreated,
			validate: func(t *testing.T, resp apiResp) {
				var data map[string]any
				require.NoError(t, json.Unmarshal(resp.Data, &data))
				assert.Equal(t, float64(4), data["count"])

				// 验证 memories 列表中 tool 角色和 metadata 保留
				memories := data["memories"].([]any)
				toolMsg := memories[2].(map[string]any)
				assert.Equal(t, "tool", toolMsg["message_role"])

				// metadata 应包含 tool_call_id
				meta := toolMsg["metadata"].(map[string]any)
				assert.Equal(t, "call_abc123", meta["tool_call_id"])
				assert.Equal(t, "get_weather", meta["name"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, resp := doRequest(t, router, "POST", "/v1/conversations", tt.request)
			assert.Equal(t, tt.wantCode, code)
			assert.Equal(t, 0, resp.Code)
			if tt.validate != nil {
				tt.validate(t, resp)
			}
		})
	}
}

// TestLLMConversation_Claude 模拟 Claude 对话摄取（含扩展思考） / Simulate Claude conversation with extended thinking
func TestLLMConversation_Claude(t *testing.T) {
	router, cleanup := setupFullRouter(t)
	defer cleanup()

	t.Run("extended_thinking_via_metadata", func(t *testing.T) {
		// Claude 的 extended thinking 存为 assistant 消息，thinking 内容放 metadata
		reqBody := map[string]any{
			"provider":    "claude",
			"external_id": "msg_thinking_001",
			"scope":       "agent/claude",
			"messages": []map[string]any{
				{
					"role":    "user",
					"content": "Explain the trade-offs between SQLite and PostgreSQL for a memory system.",
				},
				{
					"role":    "assistant",
					"content": "Here are the key trade-offs:\n\n1. **SQLite** is embedded, zero-config...\n2. **PostgreSQL** offers concurrent writes...",
					"metadata": map[string]any{
						"thinking": "The user is asking about database trade-offs. Let me consider: " +
							"SQLite pros - embedded, ACID, single file, no server. " +
							"SQLite cons - single writer, limited concurrent access. " +
							"PostgreSQL pros - full ACID with MVCC, concurrent writes, extensions. " +
							"PostgreSQL cons - requires server, more complex setup. " +
							"For a memory system that's local-first, SQLite makes more sense initially.",
						"thinking_tokens":    342,
						"model":              "claude-sonnet-4-20250514",
						"stop_reason":        "end_turn",
						"input_tokens":       28,
						"output_tokens":      156,
						"cache_read_tokens":  0,
						"cache_write_tokens": 0,
					},
				},
			},
			"metadata": map[string]any{
				"model":       "claude-sonnet-4-20250514",
				"api_version": "2024-01-01",
			},
		}

		code, resp := doRequest(t, router, "POST", "/v1/conversations", reqBody)
		require.Equal(t, http.StatusCreated, code)

		var data map[string]any
		require.NoError(t, json.Unmarshal(resp.Data, &data))
		contextID := fmt.Sprintf("%v", data["context_id"])

		// 验证对话获取后 metadata 保留
		code, resp = doRequest(t, router, "GET", "/v1/conversations/"+contextID, nil)
		require.Equal(t, http.StatusOK, code)

		var convData map[string]any
		json.Unmarshal(resp.Data, &convData)
		memories := convData["memories"].([]any)
		assert.Equal(t, 2, len(memories))

		assistantMsg := memories[1].(map[string]any)
		assert.Equal(t, "assistant", assistantMsg["message_role"])

		meta := assistantMsg["metadata"].(map[string]any)
		assert.Contains(t, meta["thinking"], "database trade-offs")
		t.Logf("Thinking content preserved: %d chars", len(meta["thinking"].(string)))
	})

	t.Run("thinking_as_separate_memory", func(t *testing.T) {
		// 另一种方案：thinking 单独存为一条记忆（kind=note, sub_kind=thinking）
		reqBody := map[string]any{
			"provider":    "claude-thinking",
			"external_id": "msg_thinking_002",
			"scope":       "agent/claude-thinking",
			"messages": []map[string]any{
				{
					"role":    "user",
					"content": "How should we implement memory decay?",
				},
				{
					"role":    "assistant",
					"content": "[thinking] I need to consider several decay models: exponential, linear, stepped. Exponential is most biologically plausible. The formula would be strength * exp(-decay_rate * hours_elapsed). We need configurable decay rates per retention tier.",
					"metadata": map[string]any{
						"is_thinking":  true,
						"content_type": "thinking",
					},
				},
				{
					"role":    "assistant",
					"content": "For memory decay, I recommend an exponential decay model: `strength * exp(-decay_rate * hours)`. Each retention tier gets a different decay rate...",
					"metadata": map[string]any{
						"is_thinking":  false,
						"content_type": "response",
					},
				},
			},
		}

		code, resp := doRequest(t, router, "POST", "/v1/conversations", reqBody)
		require.Equal(t, http.StatusCreated, code)

		var data map[string]any
		json.Unmarshal(resp.Data, &data)
		contextID := fmt.Sprintf("%v", data["context_id"])

		// 验证三条消息都存储成功
		code, resp = doRequest(t, router, "GET", "/v1/conversations/"+contextID, nil)
		require.Equal(t, http.StatusOK, code)

		json.Unmarshal(resp.Data, &data)
		memories := data["memories"].([]any)
		assert.Equal(t, 3, len(memories))

		// 验证 thinking 消息的 metadata
		thinkingMsg := memories[1].(map[string]any)
		meta := thinkingMsg["metadata"].(map[string]any)
		assert.Equal(t, true, meta["is_thinking"])
		assert.Equal(t, "thinking", meta["content_type"])
	})
}

// TestLLMConversation_Retrieval 测试对话数据的检索能力 / Test retrieval of conversation data
func TestLLMConversation_Retrieval(t *testing.T) {
	router, cleanup := setupFullRouter(t)
	defer cleanup()

	// 先摄取一组对话
	reqBody := map[string]any{
		"provider":    "openai",
		"external_id": "thread-search-001",
		"scope":       "agent/assistant",
		"messages": []map[string]any{
			{"role": "user", "content": "Explain how RRF fusion works in IClude"},
			{"role": "assistant", "content": "Reciprocal Rank Fusion (RRF) combines results from multiple search backends. For each result, the RRF score is 1/(k+rank), where k=60. Results from SQLite FTS5 and Qdrant vector search are merged using this formula, then sorted by combined score."},
			{"role": "user", "content": "What is the default k value?"},
			{"role": "assistant", "content": "The default k value is 60, which is a standard choice that balances between giving weight to top-ranked results while still considering lower-ranked ones."},
		},
	}
	code, _ := doRequest(t, router, "POST", "/v1/conversations", reqBody)
	require.Equal(t, http.StatusCreated, code)

	t.Run("search_by_text", func(t *testing.T) {
		code, resp := doRequest(t, router, "POST", "/v1/retrieve", map[string]any{
			"query": "RRF fusion",
			"limit": 10,
		})
		assert.Equal(t, http.StatusOK, code)

		var retrieveResp struct{ Results []map[string]any }
		json.Unmarshal(resp.Data, &retrieveResp)
		results := retrieveResp.Results
		t.Logf("Search 'RRF fusion': %d results", len(results))
		assert.GreaterOrEqual(t, len(results), 1, "should find conversation messages about RRF")

		// 验证返回的结果包含对话字段
		for _, r := range results {
			mem := r["memory"].(map[string]any)
			t.Logf("  score=%.4f role=%v turn=%v content=%q",
				r["score"], mem["message_role"], mem["turn_number"], mem["content"])
		}
	})

	t.Run("filter_by_message_role", func(t *testing.T) {
		code, resp := doRequest(t, router, "POST", "/v1/retrieve", map[string]any{
			"query": "RRF",
			"limit": 10,
			"filters": map[string]any{
				"message_role": "assistant",
			},
		})
		assert.Equal(t, http.StatusOK, code)

		var retrieveResp struct{ Results []map[string]any }
		json.Unmarshal(resp.Data, &retrieveResp)
		results := retrieveResp.Results
		t.Logf("Search 'RRF' with role=assistant: %d results", len(results))

		for _, r := range results {
			mem := r["memory"].(map[string]any)
			assert.Equal(t, "assistant", mem["message_role"], "all results should be assistant messages")
		}
	})

	t.Run("filter_by_source_type", func(t *testing.T) {
		code, resp := doRequest(t, router, "POST", "/v1/retrieve", map[string]any{
			"query": "RRF",
			"limit": 10,
			"filters": map[string]any{
				"source_type": "conversation",
			},
		})
		assert.Equal(t, http.StatusOK, code)

		var retrieveResp struct{ Results []map[string]any }
		json.Unmarshal(resp.Data, &retrieveResp)
		results := retrieveResp.Results
		t.Logf("Search 'RRF' with source_type=conversation: %d results", len(results))
		assert.GreaterOrEqual(t, len(results), 1)
	})
}

// TestLLMConversation_MultiProvider 测试多提供商对话的隔离与共存 / Test multi-provider conversation isolation
func TestLLMConversation_MultiProvider(t *testing.T) {
	router, cleanup := setupFullRouter(t)
	defer cleanup()

	providers := []struct {
		provider   string
		externalID string
		scope      string
		messages   []map[string]any
	}{
		{
			provider:   "openai",
			externalID: "chatcmpl-openai-001",
			scope:      "agent/gpt4",
			messages: []map[string]any{
				{"role": "user", "content": "OpenAI: What is 2+2?"},
				{"role": "assistant", "content": "OpenAI: 2+2 equals 4."},
			},
		},
		{
			provider:   "claude",
			externalID: "msg-claude-001",
			scope:      "agent/claude",
			messages: []map[string]any{
				{"role": "user", "content": "Claude: What is 3+3?"},
				{"role": "assistant", "content": "Claude: 3+3 equals 6."},
			},
		},
		{
			provider:   "ollama",
			externalID: "ollama-local-001",
			scope:      "agent/local",
			messages: []map[string]any{
				{"role": "user", "content": "Ollama: What is 4+4?"},
				{"role": "assistant", "content": "Ollama: 4+4 equals 8."},
			},
		},
	}

	contextIDs := make([]string, len(providers))

	for i, p := range providers {
		reqBody := map[string]any{
			"provider":    p.provider,
			"external_id": p.externalID,
			"scope":       p.scope,
			"messages":    p.messages,
		}

		code, resp := doRequest(t, router, "POST", "/v1/conversations", reqBody)
		require.Equal(t, http.StatusCreated, code)

		var data map[string]any
		json.Unmarshal(resp.Data, &data)
		contextIDs[i] = fmt.Sprintf("%v", data["context_id"])
		t.Logf("Ingested %s conversation: context_id=%s", p.provider, contextIDs[i])
	}

	// 验证每个对话隔离
	for i, ctxID := range contextIDs {
		code, resp := doRequest(t, router, "GET", "/v1/conversations/"+ctxID, nil)
		require.Equal(t, http.StatusOK, code)

		var data map[string]any
		json.Unmarshal(resp.Data, &data)
		memories := data["memories"].([]any)
		assert.Equal(t, 2, len(memories), "provider %s should have 2 messages", providers[i].provider)
	}

	// 按 scope 检索，验证只返回对应提供商的结果
	t.Run("scope_isolation", func(t *testing.T) {
		code, resp := doRequest(t, router, "POST", "/v1/retrieve", map[string]any{
			"query": "equals",
			"limit": 10,
			"filters": map[string]any{
				"scope": "agent/claude",
			},
		})
		assert.Equal(t, http.StatusOK, code)

		var retrieveResp struct{ Results []map[string]any }
		json.Unmarshal(resp.Data, &retrieveResp)
		results := retrieveResp.Results
		t.Logf("Search with scope=agent/claude: %d results", len(results))
		for _, r := range results {
			mem := r["memory"].(map[string]any)
			assert.Equal(t, "agent/claude", mem["scope"])
		}
	})
}

// TestLLMConversation_LargeConversation 测试大规模对话摄取 / Test large conversation ingest performance
func TestLLMConversation_LargeConversation(t *testing.T) {
	router, cleanup := setupFullRouter(t)
	defer cleanup()

	// 构建 100 轮对话
	messages := make([]map[string]any, 0, 200)
	for i := 1; i <= 100; i++ {
		messages = append(messages, map[string]any{
			"role":    "user",
			"content": fmt.Sprintf("This is user message number %d about memory systems.", i),
		})
		messages = append(messages, map[string]any{
			"role":    "assistant",
			"content": fmt.Sprintf("This is assistant response number %d explaining memory concepts.", i),
		})
	}

	reqBody := map[string]any{
		"provider":    "generic",
		"external_id": "long-conv-001",
		"scope":       "test/performance",
		"messages":    messages,
	}

	code, resp := doRequest(t, router, "POST", "/v1/conversations", reqBody)
	require.Equal(t, http.StatusCreated, code)

	var data map[string]any
	json.Unmarshal(resp.Data, &data)
	assert.Equal(t, float64(200), data["count"])
	contextID := fmt.Sprintf("%v", data["context_id"])
	t.Logf("Ingested 200 messages, context_id=%s", contextID)

	// 分页获取
	code, resp = doRequest(t, router, "GET", fmt.Sprintf("/v1/conversations/%s?offset=0&limit=10", contextID), nil)
	require.Equal(t, http.StatusOK, code)

	json.Unmarshal(resp.Data, &data)
	memories := data["memories"].([]any)
	assert.Equal(t, 10, len(memories))

	// 验证排序正确
	firstMsg := memories[0].(map[string]any)
	assert.Equal(t, float64(1), firstMsg["turn_number"])
	assert.Equal(t, "user", firstMsg["message_role"])

	lastMsg := memories[9].(map[string]any)
	assert.Equal(t, float64(10), lastMsg["turn_number"])
	assert.Equal(t, "assistant", lastMsg["message_role"])
}

// TestLLMConversation_ComplexToolChain 测试复杂工具链场景 / Test complex multi-tool conversation
func TestLLMConversation_ComplexToolChain(t *testing.T) {
	router, cleanup := setupFullRouter(t)
	defer cleanup()

	// 模拟 agent 框架的复杂工具链：用户提问 → assistant 调用多个工具 → 工具返回 → 最终回答
	reqBody := map[string]any{
		"provider":    "langchain",
		"external_id": "agent-run-001",
		"scope":       "agent/research",
		"messages": []map[string]any{
			{
				"role":    "system",
				"content": "You are a research assistant with access to search and calculator tools.",
			},
			{
				"role":    "user",
				"content": "What is the GDP of China in 2024 and how does it compare to the US?",
			},
			{
				"role":    "assistant",
				"content": "I'll search for the latest GDP data for both countries.",
				"metadata": map[string]any{
					"tool_calls": []map[string]any{
						{"id": "call_1", "function": map[string]any{"name": "web_search", "arguments": `{"query":"China GDP 2024"}`}},
						{"id": "call_2", "function": map[string]any{"name": "web_search", "arguments": `{"query":"US GDP 2024"}`}},
					},
					"agent_step": 1,
				},
			},
			{
				"role":    "tool",
				"content": `{"result": "China's GDP in 2024 was approximately $18.5 trillion"}`,
				"metadata": map[string]any{
					"tool_call_id": "call_1",
					"name":         "web_search",
					"agent_step":   1,
				},
			},
			{
				"role":    "tool",
				"content": `{"result": "US GDP in 2024 was approximately $28.8 trillion"}`,
				"metadata": map[string]any{
					"tool_call_id": "call_2",
					"name":         "web_search",
					"agent_step":   1,
				},
			},
			{
				"role":    "assistant",
				"content": "Now let me calculate the ratio.",
				"metadata": map[string]any{
					"tool_calls": []map[string]any{
						{"id": "call_3", "function": map[string]any{"name": "calculator", "arguments": `{"expression":"18.5/28.8*100"}`}},
					},
					"agent_step": 2,
				},
			},
			{
				"role":    "tool",
				"content": `{"result": 64.24}`,
				"metadata": map[string]any{
					"tool_call_id": "call_3",
					"name":         "calculator",
					"agent_step":   2,
				},
			},
			{
				"role":    "assistant",
				"content": "China's GDP in 2024 was approximately $18.5 trillion, while the US GDP was about $28.8 trillion. China's GDP represents about 64.2% of the US GDP.",
				"metadata": map[string]any{
					"finish_reason":     "stop",
					"agent_step":        3,
					"total_agent_steps": 3,
					"tools_used":        []string{"web_search", "calculator"},
					"total_tool_calls":  3,
				},
			},
		},
		"metadata": map[string]any{
			"framework":      "langchain",
			"agent_type":     "react",
			"max_iterations": 10,
		},
	}

	code, resp := doRequest(t, router, "POST", "/v1/conversations", reqBody)
	require.Equal(t, http.StatusCreated, code)

	var data map[string]any
	json.Unmarshal(resp.Data, &data)
	assert.Equal(t, float64(8), data["count"], "should store all 8 messages including tool calls/results")

	contextID := fmt.Sprintf("%v", data["context_id"])

	// 获取完整对话，验证工具链顺序正确
	code, resp = doRequest(t, router, "GET", "/v1/conversations/"+contextID, nil)
	require.Equal(t, http.StatusOK, code)

	json.Unmarshal(resp.Data, &data)
	memories := data["memories"].([]any)

	// 统计各角色消息数量
	roleCounts := map[string]int{}
	for _, m := range memories {
		mem := m.(map[string]any)
		role := mem["message_role"].(string)
		roleCounts[role]++
	}

	t.Logf("Role distribution: %v", roleCounts)
	assert.Equal(t, 1, roleCounts["system"])
	assert.Equal(t, 1, roleCounts["user"])
	assert.Equal(t, 3, roleCounts["assistant"])
	assert.Equal(t, 3, roleCounts["tool"])

	// 验证工具消息的 metadata 完整
	for _, m := range memories {
		mem := m.(map[string]any)
		if mem["message_role"] == "tool" {
			meta := mem["metadata"].(map[string]any)
			assert.NotEmpty(t, meta["tool_call_id"], "tool message should have tool_call_id")
			assert.NotEmpty(t, meta["name"], "tool message should have tool name")
		}
	}
}

// TestLLMConversation_RetentionTiers 测试对话消息的不同保留策略 / Test conversation messages with different retention tiers
func TestLLMConversation_RetentionTiers(t *testing.T) {
	router, cleanup := setupFullRouter(t)
	defer cleanup()

	// 创建一个 context 用于对话
	code, resp := doRequest(t, router, "POST", "/v1/contexts", map[string]any{
		"name":  "retention-test-session",
		"kind":  "session",
		"scope": "test",
	})
	require.Equal(t, http.StatusCreated, code)
	contextID := extractID(t, resp)

	// system prompt → permanent（需要长期保留）
	code, resp = doRequest(t, router, "POST", "/v1/memories", map[string]any{
		"content":        "You are an expert Go developer.",
		"context_id":     contextID,
		"message_role":   "system",
		"turn_number":    1,
		"source_type":    "conversation",
		"retention_tier": "permanent",
		"scope":          "test",
	})
	require.Equal(t, http.StatusCreated, code)
	systemID := extractID(t, resp)

	// user 消息 → standard
	code, resp = doRequest(t, router, "POST", "/v1/memories", map[string]any{
		"content":        "How do I implement interfaces in Go?",
		"context_id":     contextID,
		"message_role":   "user",
		"turn_number":    2,
		"source_type":    "conversation",
		"retention_tier": "standard",
		"scope":          "test",
	})
	require.Equal(t, http.StatusCreated, code)
	userID := extractID(t, resp)

	// assistant 回答 → long_term（有价值的知识）
	code, resp = doRequest(t, router, "POST", "/v1/memories", map[string]any{
		"content":        "In Go, interfaces are satisfied implicitly. Any type that implements all methods of an interface automatically satisfies it.",
		"context_id":     contextID,
		"message_role":   "assistant",
		"turn_number":    3,
		"source_type":    "conversation",
		"retention_tier": "long_term",
		"scope":          "test",
		"abstract":       "Go implicit interface satisfaction",
		"summary":        "Go interfaces are satisfied implicitly - no explicit declaration needed",
		"kind":           "fact",
	})
	require.Equal(t, http.StatusCreated, code)
	assistantID := extractID(t, resp)

	// 验证各消息的 retention tier 设置正确
	for _, tc := range []struct {
		id   string
		tier string
		role string
	}{
		{systemID, "permanent", "system"},
		{userID, "standard", "user"},
		{assistantID, "long_term", "assistant"},
	} {
		code, resp = doRequest(t, router, "GET", "/v1/memories/"+tc.id, nil)
		require.Equal(t, http.StatusOK, code)

		var mem map[string]any
		json.Unmarshal(resp.Data, &mem)
		assert.Equal(t, tc.tier, mem["retention_tier"], "memory %s should have tier %s", tc.id, tc.tier)
		assert.Equal(t, tc.role, mem["message_role"])
		t.Logf("  %s (role=%s): tier=%s strength=%.2f decay_rate=%.4f",
			tc.id, tc.role, mem["retention_tier"], mem["strength"], mem["decay_rate"])
	}
}

// TestLLMConversation_WithTagsAndEntities 测试对话消息与标签/实体的集成 / Test conversation with tags and entity extraction
func TestLLMConversation_WithTagsAndEntities(t *testing.T) {
	router, cleanup := setupFullRouter(t)
	defer cleanup()

	// 摄取一段关于技术选型的对话
	reqBody := map[string]any{
		"provider":    "claude",
		"external_id": "msg-entities-001",
		"scope":       "team/engineering",
		"messages": []map[string]any{
			{"role": "user", "content": "We should consider switching from Redis to Valkey for caching."},
			{"role": "assistant", "content": "That's a good suggestion. Valkey is the community fork of Redis after the license change. It maintains API compatibility while being fully open source under BSD license."},
		},
	}

	code, resp := doRequest(t, router, "POST", "/v1/conversations", reqBody)
	require.Equal(t, http.StatusCreated, code)

	var data map[string]any
	json.Unmarshal(resp.Data, &data)
	memories := data["memories"].([]any)
	assistantMemID := memories[1].(map[string]any)["id"].(string)

	// 为 assistant 消息创建实体关联（模拟未来 Phase 2 的自动抽取）
	// 创建实体: Redis, Valkey
	code, resp = doRequest(t, router, "POST", "/v1/entities", map[string]any{
		"name":        "Redis",
		"entity_type": "technology",
		"scope":       "team/engineering",
	})
	require.Equal(t, http.StatusCreated, code)
	redisID := extractID(t, resp)

	code, resp = doRequest(t, router, "POST", "/v1/entities", map[string]any{
		"name":        "Valkey",
		"entity_type": "technology",
		"scope":       "team/engineering",
	})
	require.Equal(t, http.StatusCreated, code)
	valkeyID := extractID(t, resp)

	// 创建关系: Valkey --fork_of--> Redis
	code, _ = doRequest(t, router, "POST", "/v1/entity-relations", map[string]any{
		"source_id":     valkeyID,
		"target_id":     redisID,
		"relation_type": "fork_of",
		"weight":        1.0,
	})
	require.Equal(t, http.StatusCreated, code)

	// 将对话记忆与实体关联
	code, _ = doRequest(t, router, "POST", "/v1/memory-entities", map[string]any{
		"memory_id": assistantMemID,
		"entity_id": redisID,
		"role":      "subject",
	})
	assert.Equal(t, http.StatusCreated, code)

	code, _ = doRequest(t, router, "POST", "/v1/memory-entities", map[string]any{
		"memory_id": assistantMemID,
		"entity_id": valkeyID,
		"role":      "subject",
	})
	assert.Equal(t, http.StatusCreated, code)

	// 通过实体查找相关记忆
	code, resp = doRequest(t, router, "GET", "/v1/entities/"+valkeyID+"/memories", nil)
	assert.Equal(t, http.StatusOK, code)

	var entityMemories []map[string]any
	json.Unmarshal(resp.Data, &entityMemories)
	assert.GreaterOrEqual(t, len(entityMemories), 1, "Valkey entity should be linked to at least 1 memory")
	t.Logf("Valkey entity linked to %d memories", len(entityMemories))

	// 通过实体查找关系
	code, resp = doRequest(t, router, "GET", "/v1/entities/"+valkeyID+"/relations", nil)
	assert.Equal(t, http.StatusOK, code)

	var relations []map[string]any
	json.Unmarshal(resp.Data, &relations)
	assert.GreaterOrEqual(t, len(relations), 1)
	t.Logf("Valkey relations: %d (should include fork_of Redis)", len(relations))
}

// TestLLMConversation_AutoTurnNumber 测试自动轮次号分配 / Test automatic turn number assignment
func TestLLMConversation_AutoTurnNumber(t *testing.T) {
	router, cleanup := setupFullRouter(t)
	defer cleanup()

	// 不指定 turn_number，应自动从 1 开始分配
	reqBody := map[string]any{
		"provider": "generic",
		"scope":    "test/auto-turn",
		"messages": []map[string]any{
			{"role": "user", "content": "First message"},
			{"role": "assistant", "content": "First response"},
			{"role": "user", "content": "Second message"},
			{"role": "assistant", "content": "Second response"},
		},
	}

	code, resp := doRequest(t, router, "POST", "/v1/conversations", reqBody)
	require.Equal(t, http.StatusCreated, code)

	var data map[string]any
	json.Unmarshal(resp.Data, &data)
	contextID := fmt.Sprintf("%v", data["context_id"])

	// 获取并验证自动分配的 turn_number
	code, resp = doRequest(t, router, "GET", "/v1/conversations/"+contextID, nil)
	require.Equal(t, http.StatusOK, code)

	json.Unmarshal(resp.Data, &data)
	memories := data["memories"].([]any)

	for i, m := range memories {
		mem := m.(map[string]any)
		expectedTurn := float64(i + 1)
		assert.Equal(t, expectedTurn, mem["turn_number"],
			"message %d should have turn_number %d", i, i+1)
	}
}

// TestLLMConversation_MetadataPreservation 测试 metadata 字段的完整保留 / Test metadata field preservation
func TestLLMConversation_MetadataPreservation(t *testing.T) {
	router, cleanup := setupFullRouter(t)
	defer cleanup()

	complexMetadata := map[string]any{
		"model":       "gpt-4o-2024-08-06",
		"temperature": 0.7,
		"top_p":       0.95,
		"max_tokens":  4096,
		"usage": map[string]any{
			"prompt_tokens":     128,
			"completion_tokens": 256,
			"total_tokens":      384,
		},
		"latency_ms":    1234,
		"cached":        false,
		"finish_reason": "stop",
		"logprobs":      nil,
		"tags":          []any{"production", "customer-facing"},
	}

	reqBody := map[string]any{
		"provider": "openai",
		"scope":    "test/metadata",
		"messages": []map[string]any{
			{
				"role":     "assistant",
				"content":  "Test response with complex metadata",
				"metadata": complexMetadata,
			},
		},
	}

	code, resp := doRequest(t, router, "POST", "/v1/conversations", reqBody)
	require.Equal(t, http.StatusCreated, code)

	var data map[string]any
	json.Unmarshal(resp.Data, &data)
	contextID := fmt.Sprintf("%v", data["context_id"])

	// 获取并验证 metadata 完整保留
	code, resp = doRequest(t, router, "GET", "/v1/conversations/"+contextID, nil)
	require.Equal(t, http.StatusOK, code)

	json.Unmarshal(resp.Data, &data)
	memories := data["memories"].([]any)
	mem := memories[0].(map[string]any)
	meta := mem["metadata"].(map[string]any)

	assert.Equal(t, "gpt-4o-2024-08-06", meta["model"])
	assert.Equal(t, 0.7, meta["temperature"])
	assert.Equal(t, float64(4096), meta["max_tokens"])
	assert.Equal(t, "stop", meta["finish_reason"])
	assert.Equal(t, false, meta["cached"])

	// 嵌套对象保留
	usage := meta["usage"].(map[string]any)
	assert.Equal(t, float64(128), usage["prompt_tokens"])
	assert.Equal(t, float64(256), usage["completion_tokens"])

	// 数组保留
	tags := meta["tags"].([]any)
	assert.Equal(t, 2, len(tags))
	assert.Equal(t, "production", tags[0])

	t.Logf("Metadata fully preserved: model=%v, usage=%v, tags=%v", meta["model"], usage, tags)
}
