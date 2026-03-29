// Package mcp MCP HTTP+SSE 服务器 / MCP HTTP+SSE server with session lifecycle management
package mcp

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"golang.org/x/time/rate"
	"iclude/internal/config"
	"iclude/internal/logger"
	"iclude/internal/model"

	"go.uber.org/zap"
)

// Server MCP HTTP+SSE 服务器 / MCP HTTP+SSE server
type Server struct {
	cfg      config.MCPConfig
	registry *Registry
	sessions sync.Map // map[string]*Session
	mux      *http.ServeMux
	limiter  *rate.Limiter  // 全局速率限制 / Global rate limiter
	sseConns atomic.Int32   // 当前 SSE 连接数 / Current SSE connection count
}

// NewServer 创建 MCP 服务器并注册路由 / Create MCP server and register routes
func NewServer(cfg config.MCPConfig, registry *Registry) *Server {
	s := &Server{
		cfg:      cfg,
		registry: registry,
		mux:      http.NewServeMux(),
		limiter:  rate.NewLimiter(rate.Limit(10), 20), // 10 rps, burst 20
	}
	if s.cfg.APIToken == "" {
		logger.Warn("MCP server running without authentication — set mcp.api_token in config for production use")
	}
	s.mux.HandleFunc("/sse", s.rateLimitWrap(s.handleSSE))
	s.mux.HandleFunc("/messages", s.rateLimitWrap(s.handleMessages))
	return s
}

// rateLimitWrap 为 MCP handler 添加速率限制 / Add rate limiting to MCP handler
func (s *Server) rateLimitWrap(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.limiter.Allow() {
			http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
			return
		}
		next(w, r)
	}
}

// Handler 返回 HTTP handler / Return HTTP handler
func (s *Server) Handler() http.Handler { return s.mux }

// checkAuth 验证 Bearer token（token 为空时跳过验证）/ Verify Bearer token; skip if token is empty
func (s *Server) checkAuth(r *http.Request) bool {
	if s.cfg.APIToken == "" {
		return true // token 未配置时不鉴权（本地开发模式）
	}
	auth := r.Header.Get("Authorization")
	if !strings.HasPrefix(auth, "Bearer ") {
		return false
	}
	provided := strings.TrimPrefix(auth, "Bearer ")
	return subtle.ConstantTimeCompare([]byte(provided), []byte(s.cfg.APIToken)) == 1
}

// handleSSE GET /sse — 建立 SSE 流，创建会话，推送 endpoint 事件 / Establish SSE stream, create session, push endpoint event
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.checkAuth(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	if s.sseConns.Load() >= 100 {
		http.Error(w, "too many SSE connections", http.StatusTooManyRequests)
		return
	}
	s.sseConns.Add(1)
	defer s.sseConns.Add(-1)

	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	sessionID := uuid.New().String()
	identity := &model.Identity{
		TeamID:  s.cfg.DefaultTeamID,
		OwnerID: s.cfg.DefaultOwnerID,
	}
	sess := NewSession(sessionID, s.registry, identity)
	s.sessions.Store(sessionID, sess)
	defer func() {
		s.sessions.Delete(sessionID)
		sess.Close()
	}()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", s.cfg.CORSAllowedOrigin)
	w.WriteHeader(http.StatusOK)

	// 发送 endpoint 事件，告知客户端消息端点 URL / Send endpoint event with message URL for the client
	endpointURL := fmt.Sprintf("/messages?session=%s", sessionID)
	fmt.Fprintf(w, "event: endpoint\ndata: %s\n\n", endpointURL)
	flusher.Flush()

	logger.Info("mcp: sse session opened", zap.String("session_id", sessionID))

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			logger.Info("mcp: sse session closed", zap.String("session_id", sessionID))
			return
		case data, ok := <-sess.Out():
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: message\ndata: %s\n\n", data)
			flusher.Flush()
		}
	}
}

// handleMessages POST /messages?session=<id> — 接收 JSON-RPC 请求体，分发到会话 / Receive JSON-RPC body and dispatch to session
func (s *Server) handleMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.checkAuth(r) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("X-Frame-Options", "DENY")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; frame-ancestors 'none'")
	sessionID := r.URL.Query().Get("session")
	if sessionID == "" {
		http.Error(w, "missing session parameter", http.StatusBadRequest)
		return
	}
	val, ok := s.sessions.Load(sessionID)
	if !ok {
		http.Error(w, "session not found", http.StatusNotFound)
		return
	}
	sess := val.(*Session)

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1 MB limit
	var body json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	// 异步分发，响应通过 SSE 推送 / Dispatch asynchronously; response delivered via SSE
	dispatchCtx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	go func() {
		sess.Dispatch(dispatchCtx, body)
	}()
	w.WriteHeader(http.StatusAccepted)
}
