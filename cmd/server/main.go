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
		MemManager:         deps.MemManager,
		Retriever:          deps.Retriever,
		ContextManager:     deps.ContextManager,
		GraphManager:       deps.GraphManager,
		DocProcessor:       deps.DocProcessor,
		TagStore:           deps.Stores.TagStore,
		MemStore:           deps.Stores.MemoryStore,
		ReflectEngine:      deps.ReflectEngine,
		Extractor:          deps.Extractor,
		Summarizer:         deps.Summarizer,
		LineageTracer:      deps.LineageTracer,
		ExperienceRecaller: deps.ExperienceRecaller,
		FileStore:          deps.DocFileStore,
		DocumentConfig:     cfg.Document,
		AuthConfig:         cfg.Auth,
		ReflectConfig:      cfg.Reflect,
		CORSAllowedOrigins: cfg.Auth.CORSAllowedOrigins,
	})

	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	srv := &http.Server{
		Addr:         addr,
		Handler:      router,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 60 * time.Second,
	}

	srvErr := make(chan error, 1)
	go func() {
		logger.Info("server starting", zap.String("addr", addr))
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			srvErr <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	select {
	case sig := <-quit:
		logger.Info("received signal", zap.String("signal", sig.String()))
	case err := <-srvErr:
		logger.Error("HTTP server failed", zap.Error(err))
	}

	logger.Info("shutting down server...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("server forced to shutdown", zap.Error(err))
	}
	logger.Info("server stopped")
}
