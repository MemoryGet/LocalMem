// Package qdrant 可复用 Qdrant HTTP 客户端 / Reusable Qdrant HTTP client
package qdrant

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Client Qdrant REST API 客户端 / Qdrant REST API client
type Client struct {
	baseURL    string
	httpClient *http.Client
	collection string
	dimension  int
}

// PointStruct 向量点结构 / Vector point structure for upsert
type PointStruct struct {
	ID      string         `json:"id"`
	Vector  []float32      `json:"vector"`
	Payload map[string]any `json:"payload,omitempty"`
}

// SearchRequest 向量检索请求 / Vector search request body
type SearchRequest struct {
	Vector      []float32 `json:"vector"`
	Limit       int       `json:"limit"`
	Filter      *Filter   `json:"filter,omitempty"`
	WithPayload bool      `json:"with_payload"`
}

// Filter 过滤条件 / Filter conditions for search
type Filter struct {
	Must   []FieldCondition `json:"must,omitempty"`
	Should []FieldCondition `json:"should,omitempty"` // OR 条件 / OR conditions (min_should = 1)
}

// FieldCondition 字段匹配条件 / Field match condition
type FieldCondition struct {
	Key   string     `json:"key"`
	Match MatchValue `json:"match"`
}

// MatchValue 精确匹配值 / Exact match value
type MatchValue struct {
	Value string `json:"value"`
}

// SearchResult 检索结果 / Search result from Qdrant
type SearchResult struct {
	ID      string         `json:"id"`
	Score   float64        `json:"score"`
	Payload map[string]any `json:"payload,omitempty"`
}

// PointResult 点查询结果（含向量）/ Point query result with vector
type PointResult struct {
	ID     string    `json:"id"`
	Vector []float32 `json:"vector,omitempty"`
}

// qdrantResponse Qdrant API 通用响应
type qdrantResponse struct {
	Status interface{}     `json:"status"`
	Result json.RawMessage `json:"result"`
}

// NewClient 创建 Qdrant 客户端 / Create a new Qdrant HTTP client
func NewClient(baseURL, collection string, dimension int) *Client {
	return &Client{
		baseURL:    baseURL,
		collection: collection,
		dimension:  dimension,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// EnsureCollection 确保集合存在，不存在则创建 / Ensure collection exists, create if not
func (c *Client) EnsureCollection(ctx context.Context) error {
	exists, err := c.CollectionExists(ctx)
	if err != nil {
		return fmt.Errorf("failed to check collection existence: %w", err)
	}
	if exists {
		return nil
	}

	body := map[string]any{
		"vectors": map[string]any{
			"size":     c.dimension,
			"distance": "Cosine",
		},
	}

	url := fmt.Sprintf("%s/collections/%s", c.baseURL, c.collection)
	resp, err := c.doRequest(ctx, http.MethodPut, url, body)
	if err != nil {
		return fmt.Errorf("failed to create collection %q: %w", c.collection, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.readError(resp, "create collection")
	}
	return nil
}

// CollectionExists 检查集合是否存在 / Check whether collection exists
func (c *Client) CollectionExists(ctx context.Context) (bool, error) {
	url := fmt.Sprintf("%s/collections/%s", c.baseURL, c.collection)
	resp, err := c.doRequest(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, fmt.Errorf("failed to check collection %q: %w", c.collection, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		return true, nil
	}
	if resp.StatusCode == http.StatusNotFound {
		return false, nil
	}
	return false, c.readError(resp, "check collection existence")
}

// UpsertPoints 插入或更新向量点 / Insert or update vector points
func (c *Client) UpsertPoints(ctx context.Context, points []PointStruct) error {
	body := map[string]any{
		"points": points,
	}

	url := fmt.Sprintf("%s/collections/%s/points", c.baseURL, c.collection)
	resp, err := c.doRequest(ctx, http.MethodPut, url, body)
	if err != nil {
		return fmt.Errorf("failed to upsert points: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.readError(resp, "upsert points")
	}
	return nil
}

// DeletePoints 删除向量点 / Delete vector points by IDs
func (c *Client) DeletePoints(ctx context.Context, ids []string) error {
	body := map[string]any{
		"points": ids,
	}

	url := fmt.Sprintf("%s/collections/%s/points/delete", c.baseURL, c.collection)
	resp, err := c.doRequest(ctx, http.MethodPost, url, body)
	if err != nil {
		return fmt.Errorf("failed to delete points: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.readError(resp, "delete points")
	}
	return nil
}

// Search 向量相似度检索 / Vector similarity search
func (c *Client) Search(ctx context.Context, req SearchRequest) ([]SearchResult, error) {
	url := fmt.Sprintf("%s/collections/%s/points/search", c.baseURL, c.collection)
	resp, err := c.doRequest(ctx, http.MethodPost, url, req)
	if err != nil {
		return nil, fmt.Errorf("failed to search points: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.readError(resp, "search points")
	}

	var qResp qdrantResponse
	if err := json.NewDecoder(resp.Body).Decode(&qResp); err != nil {
		return nil, fmt.Errorf("failed to decode search response: %w", err)
	}

	var results []SearchResult
	if err := json.Unmarshal(qResp.Result, &results); err != nil {
		return nil, fmt.Errorf("failed to unmarshal search results: %w", err)
	}
	return results, nil
}

// GetPoints 批量获取向量点（含向量）/ Batch retrieve points with vectors
func (c *Client) GetPoints(ctx context.Context, ids []string) ([]PointResult, error) {
	body := map[string]any{
		"ids":          ids,
		"with_vector":  true,
		"with_payload": false,
	}

	url := fmt.Sprintf("%s/collections/%s/points", c.baseURL, c.collection)
	resp, err := c.doRequest(ctx, http.MethodPost, url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to get points: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, c.readError(resp, "get points")
	}

	var qResp qdrantResponse
	if err := json.NewDecoder(resp.Body).Decode(&qResp); err != nil {
		return nil, fmt.Errorf("failed to decode get points response: %w", err)
	}

	var results []PointResult
	if err := json.Unmarshal(qResp.Result, &results); err != nil {
		return nil, fmt.Errorf("failed to unmarshal point results: %w", err)
	}
	return results, nil
}

// EnsureFieldIndex 为 payload 字段创建 keyword 索引（幂等）/ Create keyword payload index for a field (idempotent)
// 大数据量时加速过滤查询 / Accelerates filtered queries at scale
func (c *Client) EnsureFieldIndex(ctx context.Context, field string) error {
	body := map[string]any{
		"field_name":   field,
		"field_schema": "keyword",
	}
	url := fmt.Sprintf("%s/collections/%s/index", c.baseURL, c.collection)
	resp, err := c.doRequest(ctx, http.MethodPut, url, body)
	if err != nil {
		return fmt.Errorf("failed to create field index for %q: %w", field, err)
	}
	defer resp.Body.Close()

	// 200 OK 或 400（已存在）均视为成功 / 200 OK or 400 (already exists) treated as success
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusBadRequest {
		return c.readError(resp, fmt.Sprintf("create field index %q", field))
	}
	return nil
}

// doRequest 发送 HTTP 请求
func (c *Client) doRequest(ctx context.Context, method, url string, body any) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to create HTTP request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	return c.httpClient.Do(req)
}

// readError 读取错误响应体并返回格式化错误
func (c *Client) readError(resp *http.Response, operation string) error {
	const maxQdrantErrorSize = 1 << 20 // 1 MB for error messages
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, maxQdrantErrorSize))
	return fmt.Errorf("qdrant %s failed (status %d): %s", operation, resp.StatusCode, string(bodyBytes))
}
