// Package main MCP 服务器入口 / MCP server entry point
package main

import (
	"context"
	"flag"
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
		Excerpt:       mem.Excerpt,
		Summary:       mem.Summary,
		HappenedAt:    mem.HappenedAt,
		SourceType:    mem.SourceType,
		SourceRef:     mem.SourceRef,
		RetentionTier: mem.RetentionTier,
		MessageRole:   mem.MessageRole,
		TurnNumber:    mem.TurnNumber,
		MemoryClass:   mem.MemoryClass,
		DerivedFrom:   mem.DerivedFrom,
		Embedding:     mem.Embedding,
	}
	return a.manager.Create(ctx, req)
}

// memoryGetterAdapter 将 *memory.Manager.GetVisible 适配为 MemoryGetter 接口 / Adapts Manager.GetVisible to tools.MemoryGetter interface
type memoryGetterAdapter struct {
	manager interface {
		GetVisible(ctx context.Context, id string, identity *model.Identity) (*model.Memory, error)
	}
}

// GetVisible 委托底层 Manager 按 ID + 可见性获取记忆 / Delegate to Manager.GetVisible by ID with visibility check
func (a *memoryGetterAdapter) GetVisible(ctx context.Context, id string, identity *model.Identity) (*model.Memory, error) {
	return a.manager.GetVisible(ctx, id, identity)
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
	stdioMode := flag.Bool("stdio", false, "Run in stdio mode (JSON-RPC over stdin/stdout)")
	configPath := flag.String("config", "", "Path to config file (overrides default search)")
	flag.Parse()

	if *stdioMode {
		logger.SetStdioMode(true)
	}
	logger.InitLogger()
	defer logger.GetLogger().Sync() //nolint:errcheck

	if *configPath != "" {
		os.Setenv("ICLUDE_CONFIG_PATH", *configPath)
	}

	if err := config.LoadConfig(); err != nil {
		logger.Fatal("failed to load config", zap.Error(err))
	}
	cfg := config.GetConfig()

	// stdio 模式不检查 mcp.enabled（由 Claude Code 直接拉起）/ stdio mode skips enabled check
	if !*stdioMode && !cfg.MCP.Enabled {
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
	getterAdapter := &memoryGetterAdapter{manager: deps.MemManager}

	reg := mcp.NewRegistry()

	// 注册工具 / Register tools
	reg.RegisterTool(tools.NewRetainTool(creatorAdapter, deps.Stores.ScopePolicyStore))
	reg.RegisterTool(tools.NewRecallTool(retrieverAdapter))
	if deps.ReflectEngine != nil {
		reg.RegisterTool(tools.NewReflectTool(deps.ReflectEngine))
	}
	reg.RegisterTool(tools.NewIngestConversationTool(deps.MemManager))
	reg.RegisterTool(tools.NewTimelineTool(deps.Retriever))
	reg.RegisterTool(tools.NewScanTool(retrieverAdapter, deps.Stores.TagStore))
	reg.RegisterTool(tools.NewFetchTool(getterAdapter))
	if deps.ContextManager != nil {
		reg.RegisterTool(tools.NewCreateSessionTool(deps.ContextManager))
	}
	if deps.FinalizeService != nil {
		reg.RegisterTool(tools.NewFinalizeSessionTool(deps.FinalizeService))
	}

	// 注册资源 / Register resources
	reg.RegisterResource(resources.NewRecentResource(deps.Retriever, 20))
	reg.RegisterResource(resources.NewSessionContextResource(deps.Retriever))

	// 注册提示模板 / Register prompts
	reg.RegisterPrompt(prompts.NewMemoryContextPrompt(retrieverAdapter))

	// stdio 模式：stdin/stdout JSON-RPC 传输 / stdio mode: JSON-RPC over stdin/stdout
	if *stdioMode {
		identity := &model.Identity{
			TeamID:  cfg.MCP.DefaultTeamID,
			OwnerID: cfg.MCP.DefaultOwnerID,
		}
		logger.Info("mcp server starting in stdio mode")
		if err := mcp.RunStdio(context.Background(), reg, identity, os.Stdin, os.Stdout); err != nil {
			logger.Error("stdio transport error", zap.Error(err))
		}
		return
	}

	srv := mcp.NewServer(cfg.MCP, reg)
	addr := fmt.Sprintf(":%d", cfg.MCP.Port)
	httpSrv := &http.Server{
		Addr:              addr,
		Handler:           srv.Handler(),
		ReadTimeout:       30 * time.Second,
		ReadHeaderTimeout: 10 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
	}

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
