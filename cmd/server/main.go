package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"iclude/internal/api"
	"iclude/internal/bootstrap"
	"iclude/internal/config"
	"iclude/internal/logger"

	"go.uber.org/zap"
)

func main() {
	logger.InitLogger()
	defer logger.GetLogger().Sync()

	if err := config.LoadConfig(); err != nil {
		logger.Fatal("failed to load config", zap.Error(err))
	}
	cfg := config.GetConfig()

	deps, cleanup, err := bootstrap.Init(context.Background(), cfg)
	if err != nil {
		logger.Fatal("failed to initialize", zap.Error(err))
	}
	defer cleanup()

	router := api.SetupRouter(&api.RouterDeps{
		MemManager:     deps.MemManager,
		Retriever:      deps.Retriever,
		ContextManager: deps.ContextManager,
		GraphManager:   deps.GraphManager,
		DocProcessor:   deps.DocProcessor,
		TagStore:       deps.Stores.TagStore,
		ReflectEngine:  deps.ReflectEngine,
		Extractor:      deps.Extractor,
		AuthConfig:     cfg.Auth,
		ReflectConfig:  cfg.Reflect,
	})

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	srv := &http.Server{Addr: addr, Handler: router}

	go func() {
		logger.Info("server starting", zap.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Fatal("server listen failed", zap.Error(err))
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	logger.Info("shutting down server...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("server forced to shutdown", zap.Error(err))
	}
	logger.Info("server stopped")
}
