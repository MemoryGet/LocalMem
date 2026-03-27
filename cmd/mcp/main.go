// Package main MCP 服务器入口 / MCP server entry point
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"iclude/internal/bootstrap"
	"iclude/internal/config"
	"iclude/internal/logger"
	"iclude/internal/mcp"
	"iclude/internal/mcp/prompts"
	"iclude/internal/mcp/resources"
	"iclude/internal/mcp/tools"
	"iclude/internal/model"

	"go.uber.org/zap"
)

// memoryCreatorAdapter 将 *memory.Manager.Create(*model.CreateMemoryRequest) 适配为 MemoryCreator 接口
// Adapts *memory.Manager.Create(*model.CreateMemoryRequest) to tools.MemoryCreator interface
type memoryCreatorAdapter struct {
	manager interface {
		Create(ctx context.Context, req *model.CreateMemoryRequest) (*model.Memory, error)
	}
}

// Create 将 *model.Memory 转换为 CreateMemoryRequest 调用底层 Manager / Convert *model.Memory to CreateMemoryRequest and delegate
func (a *memoryCreatorAdapter) Create(ctx context.Context, mem *model.Memory) (*model.Memory, error) {
	req := &model.CreateMemoryRequest{
		Content:       mem.Content,
		Metadata:      mem.Metadata,
		TeamID:        mem.TeamID,
		OwnerID:       mem.OwnerID,
		Visibility:    mem.Visibility,
		ContextID:     mem.ContextID,
		Kind:          mem.Kind,
		SubKind:       mem.SubKind,
		Scope:         mem.Scope,
		Abstract:      mem.Abstract,
		Summary:       mem.Summary,
		HappenedAt:    mem.HappenedAt,
		SourceType:    mem.SourceType,
		SourceRef:     mem.SourceRef,
		RetentionTier: mem.RetentionTier,
		MessageRole:   mem.MessageRole,
		TurnNumber:    mem.TurnNumber,
	}
	return a.manager.Create(ctx, req)
}

// memoryRetrieverAdapter 将 *search.Retriever 适配为 tools/prompts 的 MemoryRetriever 接口 / Adapter for search.Retriever
type memoryRetrieverAdapter struct {
	retriever interface {
		Retrieve(ctx context.Context, req *model.RetrieveRequest) ([]*model.SearchResult, error)
	}
}

// Retrieve 直接委托底层检索器，保留评分元数据 / Delegate directly, preserving score metadata
func (a *memoryRetrieverAdapter) Retrieve(ctx context.Context, req *model.RetrieveRequest) ([]*model.SearchResult, error) {
	return a.retriever.Retrieve(ctx, req)
}

func main() {
	logger.InitLogger()
	defer logger.GetLogger().Sync() //nolint:errcheck

	if err := config.LoadConfig(); err != nil {
		logger.Fatal("failed to load config", zap.Error(err))
	}
	cfg := config.GetConfig()

	if !cfg.MCP.Enabled {
		logger.Info("mcp server disabled, set mcp.enabled=true to enable")
		os.Exit(0)
	}

	deps, cleanup, err := bootstrap.Init(context.Background(), cfg)
	if err != nil {
		logger.Fatal("failed to initialize", zap.Error(err))
	}
	defer cleanup()

	// 构建适配器，桥接 Manager/Retriever 与 MCP 工具接口 / Build adapters bridging Manager/Retriever to MCP tool interfaces
	creatorAdapter := &memoryCreatorAdapter{manager: deps.MemManager}
	retrieverAdapter := &memoryRetrieverAdapter{retriever: deps.Retriever}

	reg := mcp.NewRegistry()

	// 注册工具 / Register tools
	reg.RegisterTool(tools.NewRetainTool(creatorAdapter))
	reg.RegisterTool(tools.NewRecallTool(retrieverAdapter))
	if deps.ReflectEngine != nil {
		reg.RegisterTool(tools.NewReflectTool(deps.ReflectEngine))
	}
	reg.RegisterTool(tools.NewIngestConversationTool(deps.MemManager))
	reg.RegisterTool(tools.NewTimelineTool(deps.Retriever))

	// 注册资源 / Register resources
	reg.RegisterResource(resources.NewRecentResource(deps.Retriever, 20))
	reg.RegisterResource(resources.NewSessionContextResource(deps.Retriever))

	// 注册提示模板 / Register prompts
	reg.RegisterPrompt(prompts.NewMemoryContextPrompt(retrieverAdapter))

	srv := mcp.NewServer(cfg.MCP, reg)
	addr := fmt.Sprintf(":%d", cfg.MCP.Port)
	httpSrv := &http.Server{Addr: addr, Handler: srv.Handler()}

	srvErr := make(chan error, 1)
	go func() {
		logger.Info("mcp server starting", zap.String("addr", addr))
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			srvErr <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-quit:
		logger.Info("mcp server received shutdown signal", zap.String("signal", sig.String()))
	case err := <-srvErr:
		logger.Error("mcp server listen failed", zap.Error(err))
	}

	logger.Info("shutting down mcp server...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		logger.Error("mcp server forced to shutdown", zap.Error(err))
	}
	logger.Info("mcp server stopped")
}
