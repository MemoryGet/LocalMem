package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"iclude/internal/api"
	"iclude/internal/config"
	"iclude/internal/document"
	"iclude/internal/embed"
	"iclude/internal/llm"
	"iclude/internal/logger"
	"iclude/internal/memory"
	reflectpkg "iclude/internal/reflect"
	"iclude/internal/search"
	"iclude/internal/store"

	"go.uber.org/zap"
)

func main() {
	// 初始化日志
	logger.InitLogger()
	defer logger.GetLogger().Sync()

	// 加载配置
	if err := config.LoadConfig(); err != nil {
		logger.Fatal("failed to load config", zap.Error(err))
	}

	cfg := config.GetConfig()
	logger.Info("config loaded",
		zap.Bool("sqlite_enabled", cfg.Storage.SQLite.Enabled),
		zap.Bool("qdrant_enabled", cfg.Storage.Qdrant.Enabled),
		zap.Int("server_port", cfg.Server.Port),
	)

	// 初始化 Embedder（Qdrant 启用时才需要）
	var embedder store.Embedder
	if cfg.Storage.Qdrant.Enabled {
		embCfg := cfg.LLM.Embedding
		var apiKeyOrURL string
		switch embCfg.Provider {
		case "openai":
			apiKeyOrURL = cfg.LLM.OpenAI.APIKey
		case "ollama":
			apiKeyOrURL = cfg.LLM.Ollama.BaseURL
		}
		var err error
		embedder, err = embed.NewEmbedder(embCfg.Provider, embCfg.Model, apiKeyOrURL)
		if err != nil {
			logger.Warn("failed to create embedder, vector features will be limited",
				zap.Error(err),
			)
		} else {
			logger.Info("embedder initialized",
				zap.String("provider", embCfg.Provider),
				zap.String("model", embCfg.Model),
			)
		}
	}

	// 初始化存储
	ctx := context.Background()
	stores, err := store.InitStores(ctx, cfg, embedder)
	if err != nil {
		logger.Fatal("failed to initialize stores", zap.Error(err))
	}
	defer stores.Close()

	// 初始化 LLM Provider / Initialize LLM Provider (must be before Extractor and ReflectEngine)
	var llmProvider llm.Provider
	switch {
	case cfg.LLM.OpenAI.APIKey != "":
		baseURL := cfg.LLM.OpenAI.BaseURL
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		llmProvider = llm.NewOpenAIProvider(baseURL, cfg.LLM.OpenAI.APIKey, cfg.LLM.OpenAI.Model)
		logger.Info("llm provider initialized",
			zap.String("provider", "openai"),
			zap.String("model", cfg.LLM.OpenAI.Model),
		)
	case cfg.LLM.Ollama.BaseURL != "":
		ollamaBase := strings.TrimSuffix(cfg.LLM.Ollama.BaseURL, "/") + "/v1"
		ollamaModel := cfg.LLM.Ollama.Model
		if ollamaModel == "" {
			ollamaModel = cfg.LLM.OpenAI.Model
		}
		llmProvider = llm.NewOpenAIProvider(ollamaBase, "", ollamaModel)
		logger.Info("llm provider initialized",
			zap.String("provider", "ollama"),
			zap.String("model", ollamaModel),
		)
	}

	// 知识图谱管理器（需要 GraphStore）/ Graph manager (must be before Extractor)
	var graphManager *memory.GraphManager
	if stores.GraphStore != nil {
		graphManager = memory.NewGraphManager(stores.GraphStore)
		logger.Info("graph manager initialized")
	}

	// 初始化 Extractor / Initialize Extractor (must be before Manager)
	var extractor *memory.Extractor
	if llmProvider != nil && graphManager != nil {
		extractor = memory.NewExtractor(llmProvider, graphManager, stores.MemoryStore, cfg.Extract)
		logger.Info("extractor initialized")
	}

	// 初始化查询预处理器 / Initialize query preprocessor
	var preprocessor *search.Preprocessor
	if cfg.Retrieval.Preprocess.Enabled {
		preprocessor = search.NewPreprocessor(stores.Tokenizer, stores.GraphStore, llmProvider, cfg.Retrieval)
		logger.Info("query preprocessor initialized",
			zap.Bool("use_llm", cfg.Retrieval.Preprocess.UseLLM),
		)
	}

	// 创建业务层 / Create business layer managers
	memManager := memory.NewManager(stores.MemoryStore, stores.VectorStore, stores.Embedder, stores.TagStore, stores.ContextStore, extractor)
	ret := search.NewRetriever(stores.MemoryStore, stores.VectorStore, stores.Embedder, stores.GraphStore, llmProvider, cfg.Retrieval, preprocessor)

	// 上下文管理器（需要 ContextStore）
	var ctxManager *memory.ContextManager
	if stores.ContextStore != nil {
		ctxManager = memory.NewContextManager(stores.ContextStore)
		logger.Info("context manager initialized")
	}

	// 文档处理器（需要 DocumentStore）
	var docProcessor *document.Processor
	if stores.DocumentStore != nil {
		docProcessor = document.NewProcessor(stores.DocumentStore, stores.MemoryStore, stores.Embedder)
		logger.Info("document processor initialized")
	}

	// 初始化 ReflectEngine / Initialize ReflectEngine
	var reflectEngine *reflectpkg.ReflectEngine
	if llmProvider != nil {
		reflectEngine = reflectpkg.NewReflectEngine(ret, memManager, llmProvider, cfg.Reflect)
		logger.Info("reflect engine initialized")
	}

	// 设置路由
	router := api.SetupRouter(&api.RouterDeps{
		MemManager:     memManager,
		Retriever:      ret,
		ContextManager: ctxManager,
		GraphManager:   graphManager,
		DocProcessor:   docProcessor,
		TagStore:       stores.TagStore,
		ReflectEngine:  reflectEngine,
		Extractor:      extractor,
		AuthConfig:     cfg.Auth,
	})

	// 启动服务器
	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	srv := &http.Server{
		Addr:    addr,
		Handler: router,
	}

	// 优雅关闭
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
