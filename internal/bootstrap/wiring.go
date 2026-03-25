// Package bootstrap 应用组件共享初始化 / Shared application component bootstrapping
package bootstrap

import (
	"context"
	"strings"
	"time"

	"iclude/internal/config"
	"iclude/internal/document"
	"iclude/internal/embed"
	"iclude/internal/heartbeat"
	"iclude/internal/llm"
	"iclude/internal/logger"
	"iclude/internal/memory"
	reflectpkg "iclude/internal/reflect"
	"iclude/internal/scheduler"
	"iclude/internal/search"
	"iclude/internal/store"

	"go.uber.org/zap"
)

// Deps 所有已初始化的业务组件 / All initialized business components
type Deps struct {
	Stores         *store.Stores
	MemManager     *memory.Manager
	Retriever      *search.Retriever
	ContextManager *memory.ContextManager    // nil if ContextStore unavailable
	GraphManager   *memory.GraphManager      // nil if GraphStore unavailable
	DocProcessor   *document.Processor       // nil if DocumentStore unavailable
	ReflectEngine  *reflectpkg.ReflectEngine // nil if LLM unavailable
	Extractor      *memory.Extractor         // nil if LLM or GraphStore unavailable
	Scheduler      *scheduler.Scheduler
	SchedCancel    context.CancelFunc
	Config         config.Config
}

// Init 根据配置初始化所有业务组件 / Initialize all business components from config
// 返回 cleanup 函数关闭所有资源 / Returns cleanup func that closes all resources
func Init(ctx context.Context, cfg config.Config) (*Deps, func(), error) {
	// Embedder（Qdrant 启用时才需要）
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
			logger.Warn("failed to create embedder, vector features disabled", zap.Error(err))
		} else {
			logger.Info("embedder initialized",
				zap.String("provider", embCfg.Provider),
				zap.String("model", embCfg.Model),
			)
		}
	}

	// 存储初始化
	stores, err := store.InitStores(ctx, cfg, embedder)
	if err != nil {
		return nil, nil, err
	}

	// LLM Provider
	var llmProvider llm.Provider
	switch {
	case cfg.LLM.OpenAI.APIKey != "":
		baseURL := cfg.LLM.OpenAI.BaseURL
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		llmProvider = llm.NewOpenAIProvider(baseURL, cfg.LLM.OpenAI.APIKey, cfg.LLM.OpenAI.Model)
		logger.Info("llm provider initialized", zap.String("provider", "openai"))
	case cfg.LLM.Ollama.BaseURL != "":
		ollamaBase := strings.TrimSuffix(cfg.LLM.Ollama.BaseURL, "/") + "/v1"
		ollamaModel := cfg.LLM.Ollama.Model
		if ollamaModel == "" {
			ollamaModel = cfg.LLM.OpenAI.Model
		}
		llmProvider = llm.NewOpenAIProvider(ollamaBase, "", ollamaModel)
		logger.Info("llm provider initialized", zap.String("provider", "ollama"))
	}

	// Graph Manager
	var graphManager *memory.GraphManager
	if stores.GraphStore != nil {
		graphManager = memory.NewGraphManager(stores.GraphStore)
	}

	// Extractor
	var extractor *memory.Extractor
	if llmProvider != nil && graphManager != nil {
		extractor = memory.NewExtractor(llmProvider, graphManager, stores.MemoryStore, cfg.Extract)
	}

	// Query Preprocessor
	var preprocessor *search.Preprocessor
	if cfg.Retrieval.Preprocess.Enabled {
		preprocessor = search.NewPreprocessor(stores.Tokenizer, stores.GraphStore, llmProvider, cfg.Retrieval)
	}

	// Access tracker
	accessTracker := memory.NewAccessTracker(stores.MemoryStore, 10000)

	// Consolidator
	var consolidator *memory.Consolidator
	if stores.VectorStore != nil && llmProvider != nil && cfg.Consolidation.Enabled {
		consolidator = memory.NewConsolidator(stores.MemoryStore, stores.VectorStore, llmProvider)
	}

	// Business managers
	memManager := memory.NewManager(stores.MemoryStore, stores.VectorStore, stores.Embedder, stores.TagStore, stores.ContextStore, extractor)
	ret := search.NewRetriever(stores.MemoryStore, stores.VectorStore, stores.Embedder, stores.GraphStore, llmProvider, cfg.Retrieval, preprocessor, accessTracker)

	var ctxManager *memory.ContextManager
	if stores.ContextStore != nil {
		ctxManager = memory.NewContextManager(stores.ContextStore)
	}

	var docProcessor *document.Processor
	if stores.DocumentStore != nil {
		docProcessor = document.NewProcessor(stores.DocumentStore, stores.MemoryStore, stores.Embedder)
	}

	var reflectEngine *reflectpkg.ReflectEngine
	if llmProvider != nil {
		reflectEngine = reflectpkg.NewReflectEngine(ret, memManager, llmProvider, cfg.Reflect)
	}

	// Scheduler
	sched := scheduler.New()
	schedCtx, schedCancel := context.WithCancel(context.Background())
	if cfg.Scheduler.Enabled {
		sched.Register("access-flush", cfg.Scheduler.AccessFlushInterval, accessTracker.Flush)
		sched.Register("cleanup", cfg.Scheduler.CleanupInterval, func(ctx context.Context) error {
			if _, err := stores.MemoryStore.CleanupExpired(ctx); err != nil {
				logger.Warn("scheduler: cleanup expired failed", zap.Error(err))
			}
			if _, err := stores.MemoryStore.PurgeDeleted(ctx, 30*24*time.Hour); err != nil {
				logger.Warn("scheduler: purge deleted failed", zap.Error(err))
			}
			return nil
		})
		if consolidator != nil {
			sched.Register("consolidation", cfg.Scheduler.ConsolidationInterval, consolidator.Run)
		}
		if cfg.Heartbeat.Enabled {
			hbEngine := heartbeat.NewEngine(stores.MemoryStore, stores.GraphStore, stores.VectorStore, llmProvider)
			sched.Register("heartbeat", cfg.Heartbeat.Interval, hbEngine.Run)
		}
		go sched.Run(schedCtx)
	}

	deps := &Deps{
		Stores:         stores,
		MemManager:     memManager,
		Retriever:      ret,
		ContextManager: ctxManager,
		GraphManager:   graphManager,
		DocProcessor:   docProcessor,
		ReflectEngine:  reflectEngine,
		Extractor:      extractor,
		Scheduler:      sched,
		SchedCancel:    schedCancel,
		Config:         cfg,
	}

	cleanup := func() {
		schedCancel()
		sched.Wait(10 * time.Second)
		stores.Close()
	}

	return deps, cleanup, nil
}
