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
	"iclude/internal/runtime"
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
	TagManager     *memory.TagManager            // nil if TagStore unavailable
	Extractor      *memory.Extractor             // nil if LLM or GraphStore unavailable
	Summarizer     *memory.SessionSummarizer    // nil if LLM unavailable
	LineageTracer  *memory.LineageTracer        // always non-nil when MemoryStore exists
	ExperienceRecaller *search.ExperienceRecaller // nil if Retriever unavailable
	SessionService  *runtime.SessionService     // nil if SessionStore unavailable
	FinalizeService *runtime.FinalizeService    // nil if SessionStore/FinalizeStore unavailable
	RepairService   *runtime.RepairService     // nil if FinalizeService unavailable
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

// managers 聚合所有业务管理器 / Aggregates all business managers
type managers struct {
	memManager   *memory.Manager
	graphManager *memory.GraphManager
	ctxManager   *memory.ContextManager
	extractor    *memory.Extractor
	consolidator *memory.Consolidator
	accessTracker *memory.AccessTracker
}

// Init 根据配置初始化所有业务组件 / Initialize all business components from config
// 返回 cleanup 函数关闭所有资源 / Returns cleanup func that closes all resources
func Init(ctx context.Context, cfg config.Config) (*Deps, func(), error) {
	// 1. Embedder（Qdrant 启用时才需要）
	embedder, err := initEmbedder(ctx, cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("init embedder: %w", err)
	}

	// 2. 存储初始化
	stores, err := initStores(ctx, cfg, embedder)
	if err != nil {
		return nil, nil, err
	}

	// 3. LLM Provider
	llmProvider := initLLM(cfg)

	// 4. 业务管理器
	mgrs := initBusinessManagers(stores, llmProvider, cfg)

	// 5. 检索层
	ret, experienceRecaller := initRetrievalLayer(stores, llmProvider, mgrs, cfg)

	// 6. 上层服务（Reflect、Doc、Summarizer、Lineage、Runtime）
	upper := initUpperServices(ctx, stores, mgrs, ret, llmProvider, cfg)

	// 7. 异步队列
	taskQueue := initQueue(stores, mgrs.memManager, cfg)

	// 8. 调度器
	sched, schedCancel := initScheduler(ctx, stores, mgrs, llmProvider, taskQueue, upper.repairService, cfg)

	// Tag Manager（TagStore 存在时创建）/ Create when TagStore is available
	var tagManager *memory.TagManager
	if stores.TagStore != nil {
		tagManager = memory.NewTagManager(stores.TagStore, stores.MemoryStore)
	}

	deps := &Deps{
		Stores:             stores,
		MemManager:         mgrs.memManager,
		Retriever:          ret,
		ContextManager:     mgrs.ctxManager,
		GraphManager:       mgrs.graphManager,
		DocProcessor:       upper.docProcessor,
		DocFileStore:       upper.docFileStore,
		ReflectEngine:      upper.reflectEngine,
		TagManager:         tagManager,
		Extractor:          mgrs.extractor,
		Summarizer:         upper.summarizer,
		LineageTracer:      upper.lineageTracer,
		ExperienceRecaller: experienceRecaller,
		SessionService:     upper.sessionService,
		FinalizeService:    upper.finalizeService,
		RepairService:      upper.repairService,
		Scheduler:          sched,
		SchedCancel:        schedCancel,
		Queue:              taskQueue,
		Config:             cfg,
	}

	cleanup := func() {
		schedCancel()
		sched.Wait(10 * time.Second)
		stores.Close()
	}

	return deps, cleanup, nil
}

// initEmbedder 初始化嵌入器（仅 Qdrant 启用时）/ Initialize embedder (only when Qdrant enabled)
func initEmbedder(ctx context.Context, cfg config.Config) (store.Embedder, error) {
	if !cfg.Storage.Qdrant.Enabled {
		return nil, nil
	}

	embCfg := cfg.LLM.Embedding
	var apiKeyOrURL string
	switch embCfg.Provider {
	case "openai":
		apiKeyOrURL = cfg.LLM.OpenAI.APIKey
	case "ollama":
		apiKeyOrURL = cfg.LLM.Ollama.BaseURL
	}

	embedder, err := embed.NewEmbedder(embCfg.Provider, embCfg.Model, apiKeyOrURL)
	if err != nil {
		logger.Warn("failed to create embedder, vector features disabled", zap.Error(err))
		return nil, nil
	}

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

	// B2#7: LRU 缓存（1000 条，约 6MB for 1536-dim float32）/ Wrap with LRU cache
	embedder = embed.NewCachedEmbedder(embedder, 1000, embCfg.Model)
	logger.Info("embedding LRU cache enabled", zap.Int("max_size", 1000))

	return embedder, nil
}

// initStores 初始化存储层 / Initialize storage layer
func initStores(ctx context.Context, cfg config.Config, embedder store.Embedder) (*store.Stores, error) {
	return store.InitStores(ctx, cfg, embedder)
}

// initLLM 初始化 LLM 提供者（含降级链）/ Initialize LLM provider with fallback chain
func initLLM(cfg config.Config) llm.Provider {
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

	return llmProvider
}

// initBusinessManagers 初始化业务管理器 / Initialize business managers
func initBusinessManagers(stores *store.Stores, llmProvider llm.Provider, cfg config.Config) managers {
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

	// Access tracker
	accessTracker := memory.NewAccessTracker(stores.MemoryStore, 10000)

	// Consolidator
	var consolidator *memory.Consolidator
	if stores.VectorStore != nil && llmProvider != nil && cfg.Consolidation.Enabled {
		consolidator = memory.NewConsolidator(stores.MemoryStore, stores.VectorStore, llmProvider, cfg.Consolidation)
	}

	// Memory Manager
	mgrCfg := memory.ManagerConfig{
		Dedup:           cfg.Dedup,
		Extract:         cfg.Extract,
		Crystallization: cfg.Crystallization,
		Ingest:          cfg.Ingest,
	}
	memManager := memory.NewManager(memory.ManagerDeps{
		MemStore:     stores.MemoryStore,
		VecStore:     stores.VectorStore,
		Embedder:     stores.Embedder,
		TagStore:     stores.TagStore,
		ContextStore: stores.ContextStore,
		Extractor:    extractor,
		LLMProvider:  llmProvider,
		Config:       mgrCfg,
	})

	// 向量实体解析器 / Vector entity resolver
	if cfg.Extract.Resolver.Enabled && stores.GraphStore != nil && stores.CandidateStore != nil {
		resolver := memory.NewEntityResolver(
			stores.Tokenizer,
			stores.GraphStore,
			stores.CandidateStore,
			nil, nil, // centroidMgr, vecStore — wired in Task E3
			cfg.Extract.Resolver,
		)
		memManager.SetResolver(resolver)
		logger.Info("entity resolver enabled (vector-driven)")
	}

	// 延迟注入 Manager 到 Consolidator（避免循环依赖）/ Deferred injection to avoid circular deps
	if consolidator != nil {
		consolidator.SetCreator(memManager)
	}

	// Context Manager
	var ctxManager *memory.ContextManager
	if stores.ContextStore != nil {
		ctxManager = memory.NewContextManager(stores.ContextStore)
	}

	return managers{
		memManager:    memManager,
		graphManager:  graphManager,
		ctxManager:    ctxManager,
		extractor:     extractor,
		consolidator:  consolidator,
		accessTracker: accessTracker,
	}
}

// initRetrievalLayer 初始化检索层 / Initialize retrieval layer
func initRetrievalLayer(stores *store.Stores, llmProvider llm.Provider, mgrs managers, cfg config.Config) (*search.Retriever, *search.ExperienceRecaller) {
	// Query Preprocessor
	var preprocessor *search.Preprocessor
	if cfg.Retrieval.Preprocess.Enabled {
		preprocessor = search.NewPreprocessor(stores.Tokenizer, stores.GraphStore, llmProvider, cfg.Retrieval)
	}

	ret := search.NewRetriever(stores.MemoryStore, stores.VectorStore, stores.Embedder, stores.GraphStore, llmProvider, cfg.Retrieval, preprocessor, mgrs.accessTracker)
	experienceRecaller := search.NewExperienceRecaller(ret)

	return ret, experienceRecaller
}

// upperServices 聚合上层服务 / Aggregates upper-level services
type upperServices struct {
	docProcessor    *document.Processor
	docFileStore    document.FileStore
	reflectEngine   *reflectpkg.ReflectEngine
	summarizer      *memory.SessionSummarizer
	lineageTracer   *memory.LineageTracer
	sessionService  *runtime.SessionService
	finalizeService *runtime.FinalizeService
	repairService   *runtime.RepairService
}

// initUpperServices 初始化上层服务（Reflect、Doc、Summarizer 等）/ Initialize upper services
func initUpperServices(ctx context.Context, stores *store.Stores, mgrs managers, ret *search.Retriever, llmProvider llm.Provider, cfg config.Config) upperServices {
	// Document Processor
	var docProcessor *document.Processor
	var docFileStore document.FileStore
	if stores.DocumentStore != nil {
		pipeline := document.InitDocumentPipeline(ctx, cfg.Document, stores.DocumentStore, mgrs.memManager, stores.Embedder)
		if pipeline != nil {
			docProcessor = pipeline.Processor
			docFileStore = pipeline.FileStore
		} else {
			// document.enabled=false 但 DocumentStore 可用时，保持基础功能
			docProcessor = document.NewProcessor(stores.DocumentStore, mgrs.memManager, stores.Embedder, nil, nil, nil)
		}
	}

	// Reflect Engine
	var reflectEngine *reflectpkg.ReflectEngine
	if llmProvider != nil {
		reflectEngine = reflectpkg.NewReflectEngine(ret, mgrs.memManager, stores.ContextStore, llmProvider, cfg.Reflect)
	}

	// B7: Session Summarizer / Lineage Tracer
	var summarizer *memory.SessionSummarizer
	if llmProvider != nil && stores.MemoryStore != nil {
		summarizer = memory.NewSessionSummarizer(stores.MemoryStore, stores.ContextStore, llmProvider, mgrs.memManager, memory.DefaultSummarizerConfig())
	}

	lineageTracer := memory.NewLineageTracer(stores.MemoryStore)

	// Runtime services / 运行时服务
	var sessionService *runtime.SessionService
	var finalizeService *runtime.FinalizeService
	if stores.SessionStore != nil {
		sessionService = runtime.NewSessionService(stores.SessionStore)
	}
	if stores.SessionStore != nil && stores.SessionFinalizeStore != nil && stores.IdempotencyStore != nil {
		var sum runtime.Summarizer
		if summarizer != nil {
			sum = summarizer
		}
		finalizeService = runtime.NewFinalizeService(stores.SessionStore, stores.SessionFinalizeStore, stores.IdempotencyStore, sum)
		logger.Info("runtime services initialized (session, finalize)")
	}

	var repairService *runtime.RepairService
	if finalizeService != nil {
		repairService = runtime.NewRepairService(stores.SessionStore, finalizeService, runtime.DefaultRepairConfig())
	}

	return upperServices{
		docProcessor:    docProcessor,
		docFileStore:    docFileStore,
		reflectEngine:   reflectEngine,
		summarizer:      summarizer,
		lineageTracer:   lineageTracer,
		sessionService:  sessionService,
		finalizeService: finalizeService,
		repairService:   repairService,
	}
}

// initQueue 初始化异步任务队列 / Initialize async task queue
func initQueue(stores *store.Stores, memManager *memory.Manager, cfg config.Config) *queue.Queue {
	if !cfg.Queue.Enabled {
		return nil
	}
	if stores.RawDB == nil {
		return nil
	}

	sqlDB := stores.RawDB
	if err := queue.CreateTable(sqlDB); err != nil {
		logger.Warn("failed to create async_tasks table", zap.Error(err))
		return nil
	}

	taskQueue := queue.New(sqlDB)
	logger.Info("async task queue initialized")

	memManager.SetQueue(taskQueue)
	return taskQueue
}

// initScheduler 初始化调度器 + 心跳注册 / Initialize scheduler + heartbeat registration
func initScheduler(ctx context.Context, stores *store.Stores, mgrs managers, llmProvider llm.Provider, taskQueue *queue.Queue, repairService *runtime.RepairService, cfg config.Config) (*scheduler.Scheduler, context.CancelFunc) {
	sched := scheduler.New()
	schedCtx, schedCancel := context.WithCancel(ctx)

	if !cfg.Scheduler.Enabled {
		return sched, schedCancel
	}

	sched.Register("access-flush", cfg.Scheduler.AccessFlushInterval, mgrs.accessTracker.Flush)
	sched.Register("cleanup", cfg.Scheduler.CleanupInterval, func(ctx context.Context) error {
		if _, err := stores.MemoryStore.CleanupExpired(ctx); err != nil {
			logger.Warn("scheduler: cleanup expired failed", zap.Error(err))
		}
		if _, err := stores.MemoryStore.PurgeDeleted(ctx, 30*24*time.Hour); err != nil {
			logger.Warn("scheduler: purge deleted failed", zap.Error(err))
		}
		return nil
	})
	if mgrs.consolidator != nil {
		sched.Register("consolidation", cfg.Scheduler.ConsolidationInterval, mgrs.consolidator.Run)
	}
	if repairService != nil {
		sched.Register("session-repair", 10*time.Minute, repairService.Run)
	}
	if cfg.Heartbeat.Enabled {
		hbEngine := heartbeat.NewEngine(stores.MemoryStore, stores.GraphStore, stores.VectorStore, stores.CandidateStore, llmProvider, cfg.Heartbeat)
		sched.Register("heartbeat", cfg.Heartbeat.Interval, hbEngine.Run)
	}
	// Register queue worker inside scheduler block
	// 在 scheduler 块内注册队列 worker
	if taskQueue != nil {
		worker := queue.NewWorker(taskQueue, cfg.Queue.StaleTimeout)
		if mgrs.extractor != nil {
			worker.RegisterHandler("entity_extract", &extractHandler{extractor: mgrs.extractor})
		}
		sched.Register("queue-worker", cfg.Queue.PollInterval, worker.Run)
	}
	go sched.Run(schedCtx)

	return sched, schedCancel
}
