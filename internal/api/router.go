package api

import (
	"iclude/internal/config"
	"iclude/internal/document"
	"iclude/internal/memory"
	reflectpkg "iclude/internal/reflect"
	"iclude/internal/search"
	"iclude/internal/store"

	"github.com/gin-gonic/gin"
)

// RouterDeps 路由依赖 / Router dependencies
type RouterDeps struct {
	MemManager         *memory.Manager
	ContextManager     *memory.ContextManager
	GraphManager       *memory.GraphManager
	Retriever          *search.Retriever
	DocProcessor       *document.Processor
	TagStore           store.TagStore
	MemStore           store.MemoryReader // 用于标签操作的记忆归属校验 / For memory ownership checks in tag operations
	ReflectEngine      *reflectpkg.ReflectEngine
	Extractor          *memory.Extractor          // 可为 nil / may be nil
	Summarizer         *memory.SessionSummarizer  // B7: 可为 nil / may be nil
	LineageTracer      *memory.LineageTracer      // B7: 可为 nil / may be nil
	ExperienceRecaller *search.ExperienceRecaller // B7: 可为 nil / may be nil
	FileStore          document.FileStore         // nil if document disabled
	DocumentConfig     config.DocumentConfig
	AuthConfig         config.AuthConfig
	ReflectConfig      config.ReflectConfig
	CORSAllowedOrigins []string
}

// SetupRouter 初始化路由 / Initialize router with all handlers
func SetupRouter(deps *RouterDeps) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	r.Use(gin.Recovery())
	r.Use(SecurityHeadersMiddleware())
	r.Use(CORSMiddleware(deps.CORSAllowedOrigins))
	r.Use(MaxBodySizeMiddleware(4 << 20)) // 4 MB 请求体上限 / 4 MB request body limit
	r.Use(LoggerMiddleware())

	r.GET("/health", func(c *gin.Context) {
		Success(c, gin.H{"status": "ok"})
	})

	v1 := r.Group("/v1")
	v1.Use(AuthMiddleware(deps.AuthConfig))
	v1.Use(IdentityMiddleware())
	{
		// 速率限制 / Rate limiting
		writeRateLimit := RateLimitMiddleware(20, 40) // 20 rps, burst 40
		llmRateLimit := RateLimitMiddleware(2, 5)     // 2 rps, burst 5

		// Session collaboration (B6 + B7) — 静态前缀路由需在参数路由前注册 / Static prefix routes before param routes
		sessionHandler := NewSessionHandler(deps.MemManager, deps.Summarizer, deps.LineageTracer)
		v1.GET("/memories/by-source/:sourceRef", withIdentity(sessionHandler.ListBySourceRef))
		v1.DELETE("/memories/by-source/:sourceRef", withIdentity(sessionHandler.SoftDeleteBySourceRef))
		v1.POST("/memories/by-source/:sourceRef/restore", withIdentity(sessionHandler.RestoreBySourceRef))
		v1.POST("/sessions/:contextId/summarize", llmRateLimit, withIdentity(sessionHandler.Summarize))
		v1.POST("/sessions/by-source/:sourceRef/summarize", llmRateLimit, withIdentity(sessionHandler.SummarizeBySourceRef))

		// Memory CRUD
		memHandler := NewMemoryHandler(deps.MemManager, deps.ExperienceRecaller, deps.AuthConfig.Enabled)
		v1.POST("/memories", writeRateLimit, withIdentity(memHandler.Create))
		v1.GET("/memories", withIdentity(memHandler.List))
		v1.GET("/memories/:id", withIdentity(memHandler.Get))
		v1.PUT("/memories/:id", withIdentity(memHandler.Update))
		v1.DELETE("/memories/:id", withIdentity(memHandler.Delete))
		v1.DELETE("/memories/:id/soft", withIdentity(memHandler.SoftDelete))
		v1.POST("/memories/:id/restore", withIdentity(memHandler.Restore))
		v1.POST("/memories/:id/reinforce", withIdentity(memHandler.Reinforce))
		v1.GET("/memories/:id/derived-from", withIdentity(sessionHandler.ListDerivedFrom))
		v1.GET("/memories/:id/consolidated-into", withIdentity(sessionHandler.ListConsolidatedInto))
		v1.GET("/memories/:id/lineage", withIdentity(sessionHandler.Lineage))

		// Batch operations
		batchHandler := NewBatchHandler(deps.MemManager)
		v1.POST("/memories/batch", withIdentity(batchHandler.BatchGet))

		// Maintenance
		v1.POST("/maintenance/cleanup", withIdentity(memHandler.Cleanup))

		// Conversations
		convHandler := NewConversationHandler(deps.MemManager)
		v1.POST("/conversations", writeRateLimit, withIdentity(convHandler.Ingest))
		v1.GET("/conversations/:context_id", withIdentity(convHandler.GetConversation))

		// Search（带速率限制）/ Search with rate limiting
		searchHandler := NewSearchHandler(deps.Retriever)
		apiRateLimit := RateLimitMiddleware(10, 20) // 10 rps, burst 20
		v1.POST("/retrieve", apiRateLimit, withIdentity(searchHandler.Retrieve))
		v1.GET("/timeline", withIdentity(searchHandler.Timeline))

		// Contexts
		if deps.ContextManager != nil {
			ctxHandler := NewContextHandler(deps.ContextManager)
			v1.POST("/contexts", withIdentity(ctxHandler.Create))
			v1.GET("/contexts/:id", withIdentity(ctxHandler.Get))
			v1.PUT("/contexts/:id", withIdentity(ctxHandler.Update))
			v1.DELETE("/contexts/:id", withIdentity(ctxHandler.Delete))
			v1.GET("/contexts/:id/children", withIdentity(ctxHandler.ListChildren))
			v1.GET("/contexts/:id/tree", withIdentity(ctxHandler.ListSubtree))
			v1.POST("/contexts/:id/move", withIdentity(ctxHandler.Move))
		}

		// Tags
		if deps.TagStore != nil {
			tagHandler := NewTagHandler(deps.TagStore, deps.MemStore)
			v1.POST("/tags", withIdentity(tagHandler.CreateTag))
			v1.GET("/tags", withIdentity(tagHandler.ListTags))
			v1.DELETE("/tags/:id", withIdentity(tagHandler.DeleteTag))
			v1.POST("/memories/:id/tags", withIdentity(tagHandler.TagMemory))
			v1.DELETE("/memories/:id/tags/:tag_id", withIdentity(tagHandler.UntagMemory))
			v1.GET("/memories/:id/tags", withIdentity(tagHandler.GetMemoryTags))
		}

		// Graph (entities + relations)
		if deps.GraphManager != nil {
			graphHandler := NewGraphHandler(deps.GraphManager)
			v1.POST("/entities", withIdentity(graphHandler.CreateEntity))
			v1.GET("/entities", withIdentity(graphHandler.ListEntities))
			v1.GET("/entities/:id", withIdentity(graphHandler.GetEntity))
			v1.PUT("/entities/:id", withIdentity(graphHandler.UpdateEntity))
			v1.DELETE("/entities/:id", withIdentity(graphHandler.DeleteEntity))
			v1.GET("/entities/:id/relations", withIdentity(graphHandler.GetEntityRelations))
			v1.GET("/entities/:id/memories", withIdentity(graphHandler.GetEntityMemories))
			v1.POST("/entity-relations", withIdentity(graphHandler.CreateRelation))
			v1.DELETE("/entity-relations/:id", withIdentity(graphHandler.DeleteRelation))
			v1.POST("/memory-entities", withIdentity(graphHandler.CreateMemoryEntity))
			v1.DELETE("/memory-entities", withIdentity(graphHandler.DeleteMemoryEntity))
		}

		// Documents
		if deps.DocProcessor != nil {
			docHandler := NewDocumentHandler(deps.DocProcessor, deps.FileStore, deps.DocumentConfig)
			docGroup := v1.Group("/documents")
			{
				docGroup.POST("/upload", MaxBodySizeMiddleware(deps.DocumentConfig.MaxFileSize+1<<20), writeRateLimit, withIdentity(docHandler.Upload))
				docGroup.GET("", withIdentity(docHandler.List))
				docGroup.GET("/:id", withIdentity(docHandler.Get))
				docGroup.GET("/:id/status", withIdentity(docHandler.Status))
				docGroup.DELETE("/:id", withIdentity(docHandler.Delete))
				docGroup.POST("/:id/reprocess", withIdentity(docHandler.Process))
			}
		}

		// Extract 实体抽取 / Entity extraction
		if deps.Extractor != nil {
			extractHandler := NewExtractHandler(deps.Extractor)
			v1.POST("/memories/:id/extract", llmRateLimit, withIdentity(extractHandler.Extract))
		}

		// Reflect 反思推理 / Reflect reasoning
		if deps.ReflectEngine != nil {
			reflectHandler := NewReflectHandler(deps.ReflectEngine, deps.ReflectConfig)
			v1.POST("/reflect", llmRateLimit, withIdentity(reflectHandler.Reflect))
		}
	}

	return r
}
