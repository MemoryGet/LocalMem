// Package bootstrap 应用组件共享初始化 / Shared application component bootstrapping
package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"iclude/internal/config"
	"iclude/internal/document"
	"iclude/internal/embed"
	"iclude/internal/heartbeat"
	"iclude/internal/llm"
	"iclude/internal/logger"
	"iclude/internal/memory"
	"iclude/internal/model"
	"iclude/internal/queue"
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
	DocFileStore   document.FileStore        // nil if document pipeline disabled
	ReflectEngine  *reflectpkg.ReflectEngine // nil if LLM unavailable
	Extractor      *memory.Extractor         // nil if LLM or GraphStore unavailable
	Scheduler      *scheduler.Scheduler
	SchedCancel    context.CancelFunc
	Queue          *queue.Queue // nil if queue disabled or SQLite unavailable
	Config         config.Config
}

// extractHandler 将实体抽取任务委托给 Extractor / Delegates entity extraction tasks to Extractor.
type extractHandler struct {
	extractor *memory.Extractor
}

// Handle 解包 payload 并调用 Extractor / Unmarshal payload and invoke Extractor.
func (h *extractHandler) Handle(ctx context.Context, payload json.RawMessage) error {
	var req model.ExtractRequest
	if err := json.Unmarshal(payload, &req); err != nil {
		return fmt.Errorf("unmarshal extract payload: %w", err)
	}
	_, err := h.extractor.Extract(ctx, &req)
	return err
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
			// B1#3: 维度校验 — probe 一次确认返回维度与配置一致 / Dimension validation: probe once to verify
			expectedDim := cfg.Storage.Qdrant.Dimension
			if expectedDim > 0 {
				probeVec, probeErr := embedder.Embed(ctx, "dimension probe")
				if probeErr != nil {
					logger.Fatal("embedder dimension probe failed — cannot verify vector dimension, refusing to start",
						zap.Error(probeErr),
						zap.Int("expected_dimension", expectedDim),
					)
				} else if len(probeVec) != expectedDim {
					logger.Fatal("embedder dimension mismatch — model returns different dimension than qdrant.dimension config",
						zap.Int("expected", expectedDim),
						zap.Int("actual", len(probeVec)),
						zap.String("model", embCfg.Model),
					)
				} else {
					logger.Info("embedder dimension verified",
						zap.Int("dimension", expectedDim),
					)
				}
			}
		}

		// B2#7: LRU 缓存（1000 条，约 6MB for 1536-dim float32）/ Wrap with LRU cache
		embedder = embed.NewCachedEmbedder(embedder, 1000)
		logger.Info("embedding LRU cache enabled", zap.Int("max_size", 1000))
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

	// 如果配置了备用提供者，构建降级链 / Wrap with fallback chain if fallback providers are configured
	if llmProvider != nil && len(cfg.LLM.Fallback) > 0 {
		providers := []llm.Provider{llmProvider}
		names := []string{"primary"}
		for _, fb := range cfg.LLM.Fallback {
			if fb.BaseURL == "" {
				continue
			}
			providers = append(providers, llm.NewOpenAIProvider(fb.BaseURL, fb.APIKey, fb.Model))
			name := fb.Name
			if name == "" {
				name = fb.BaseURL
			}
			names = append(names, name)
		}
		if len(providers) > 1 {
			llmProvider = llm.NewFallbackProvider(providers, names)
			logger.Info("llm fallback chain configured", zap.Int("providers", len(providers)))
		}
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
		consolidator = memory.NewConsolidator(stores.MemoryStore, stores.VectorStore, llmProvider, cfg.Consolidation)
	}

	// Business managers
	mgrCfg := memory.ManagerConfig{
		Dedup:           cfg.Dedup,
		Extract:         cfg.Extract,
		Crystallization: cfg.Crystallization,
	}
	memManager := memory.NewManager(stores.MemoryStore, stores.VectorStore, stores.Embedder, stores.TagStore, stores.ContextStore, extractor, llmProvider, mgrCfg)

	// 延迟注入 Manager 到 Consolidator（避免循环依赖）/ Deferred injection to avoid circular deps
	if consolidator != nil {
		consolidator.SetCreator(memManager)
	}

	ret := search.NewRetriever(stores.MemoryStore, stores.VectorStore, stores.Embedder, stores.GraphStore, llmProvider, cfg.Retrieval, preprocessor, accessTracker)

	var ctxManager *memory.ContextManager
	if stores.ContextStore != nil {
		ctxManager = memory.NewContextManager(stores.ContextStore)
	}

	var docProcessor *document.Processor
	var docFileStore document.FileStore
	if stores.DocumentStore != nil {
		pipeline := document.InitDocumentPipeline(ctx, cfg.Document, stores.DocumentStore, memManager, stores.Embedder)
		if pipeline != nil {
			docProcessor = pipeline.Processor
			docFileStore = pipeline.FileStore
		} else {
			// document.enabled=false 但 DocumentStore 可用时，保持基础功能
			docProcessor = document.NewProcessor(stores.DocumentStore, memManager, stores.Embedder, nil, nil, nil)
		}
	}

	var reflectEngine *reflectpkg.ReflectEngine
	if llmProvider != nil {
		reflectEngine = reflectpkg.NewReflectEngine(ret, memManager, llmProvider, cfg.Reflect)
	}

	// Async task queue — created outside scheduler block so Manager can use it
	// 异步任务队列（在 scheduler 块之外创建，Manager 可直接引用）
	var taskQueue *queue.Queue
	if cfg.Queue.Enabled {
		if stores.RawDB != nil {
			sqlDB := stores.RawDB
			if err := queue.CreateTable(sqlDB); err != nil {
				logger.Warn("failed to create async_tasks table", zap.Error(err))
			} else {
				taskQueue = queue.New(sqlDB)
				logger.Info("async task queue initialized")
			}
		}
	}
	if taskQueue != nil {
		memManager.SetQueue(taskQueue)
	}

	// Scheduler
	sched := scheduler.New()
	schedCtx, schedCancel := context.WithCancel(ctx)
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
			hbEngine := heartbeat.NewEngine(stores.MemoryStore, stores.GraphStore, stores.VectorStore, llmProvider, cfg.Heartbeat)
			sched.Register("heartbeat", cfg.Heartbeat.Interval, hbEngine.Run)
		}
		// Register queue worker inside scheduler block
		// 在 scheduler 块内注册队列 worker
		if taskQueue != nil {
			worker := queue.NewWorker(taskQueue, cfg.Queue.StaleTimeout)
			if extractor != nil {
				worker.RegisterHandler("entity_extract", &extractHandler{extractor: extractor})
			}
			sched.Register("queue-worker", cfg.Queue.PollInterval, worker.Run)
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
		DocFileStore:   docFileStore,
		ReflectEngine:  reflectEngine,
		Extractor:      extractor,
		Scheduler:      sched,
		SchedCancel:    schedCancel,
		Queue:          taskQueue,
		Config:         cfg,
	}

	cleanup := func() {
		schedCancel()
		sched.Wait(10 * time.Second)
		stores.Close()
	}

	return deps, cleanup, nil
}
