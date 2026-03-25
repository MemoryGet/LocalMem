// Package mcp MCP HTTP+SSE 服务器 / MCP HTTP+SSE server with session lifecycle management
package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/google/uuid"
	"iclude/internal/config"
	"iclude/internal/logger"
	"iclude/internal/model"

	"go.uber.org/zap"
)

// Server MCP HTTP+SSE 服务器 / MCP HTTP+SSE server
type Server struct {
	cfg      config.MCPConfig
	registry *Registry
	sessions sync.Map  // map[string]*Session
	mux      *http.ServeMux
}

// NewServer 创建 MCP 服务器并注册路由 / Create MCP server and register routes
func NewServer(cfg config.MCPConfig, registry *Registry) *Server {
	s := &Server{cfg: cfg, registry: registry, mux: http.NewServeMux()}
	s.mux.HandleFunc("/sse", s.handleSSE)
	s.mux.HandleFunc("/messages", s.handleMessages)
	return s
}

// Handler 返回 HTTP handler / Return HTTP handler
func (s *Server) Handler() http.Handler { return s.mux }

// handleSSE GET /sse — 建立 SSE 流，创建会话，推送 endpoint 事件 / Establish SSE stream, create session, push endpoint event
func (s *Server) handleSSE(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
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
	w.Header().Set("Access-Control-Allow-Origin", "*")
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

	var body json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "invalid JSON body", http.StatusBadRequest)
		return
	}

	// 异步分发，响应通过 SSE 推送 / Dispatch asynchronously; response delivered via SSE
	go sess.Dispatch(context.Background(), body)
	w.WriteHeader(http.StatusAccepted)
}
