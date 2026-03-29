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
	ReflectEngine      *reflectpkg.ReflectEngine
	Extractor          *memory.Extractor      // 可为 nil / may be nil
	FileStore          document.FileStore     // nil if document disabled
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
		// 写接口速率限制 / Write endpoint rate limiting
		writeRateLimit := RateLimitMiddleware(20, 40) // 20 rps, burst 40

		// Memory CRUD
		memHandler := NewMemoryHandler(deps.MemManager, deps.AuthConfig.Enabled)
		v1.POST("/memories", writeRateLimit, memHandler.Create)
		v1.GET("/memories", memHandler.List)
		v1.GET("/memories/:id", memHandler.Get)
		v1.PUT("/memories/:id", memHandler.Update)
		v1.DELETE("/memories/:id", memHandler.Delete)
		v1.DELETE("/memories/:id/soft", memHandler.SoftDelete)
		v1.POST("/memories/:id/restore", memHandler.Restore)
		v1.POST("/memories/:id/reinforce", memHandler.Reinforce)

		// Batch operations
		batchHandler := NewBatchHandler(deps.MemManager)
		v1.POST("/memories/batch", batchHandler.BatchGet)

		// Maintenance
		v1.POST("/maintenance/cleanup", memHandler.Cleanup)

		// Conversations
		convHandler := NewConversationHandler(deps.MemManager)
		v1.POST("/conversations", writeRateLimit, convHandler.Ingest)
		v1.GET("/conversations/:context_id", convHandler.GetConversation)

		// Search（带速率限制）/ Search with rate limiting
		searchHandler := NewSearchHandler(deps.Retriever)
		apiRateLimit := RateLimitMiddleware(10, 20) // 10 rps, burst 20
		v1.POST("/retrieve", apiRateLimit, searchHandler.Retrieve)
		v1.GET("/timeline", searchHandler.Timeline)

		// Contexts
		if deps.ContextManager != nil {
			ctxHandler := NewContextHandler(deps.ContextManager)
			v1.POST("/contexts", ctxHandler.Create)
			v1.GET("/contexts/:id", ctxHandler.Get)
			v1.PUT("/contexts/:id", ctxHandler.Update)
			v1.DELETE("/contexts/:id", ctxHandler.Delete)
			v1.GET("/contexts/:id/children", ctxHandler.ListChildren)
			v1.GET("/contexts/:id/tree", ctxHandler.ListSubtree)
			v1.POST("/contexts/:id/move", ctxHandler.Move)
		}

		// Tags
		if deps.TagStore != nil {
			tagHandler := NewTagHandler(deps.TagStore)
			v1.POST("/tags", tagHandler.CreateTag)
			v1.GET("/tags", tagHandler.ListTags)
			v1.DELETE("/tags/:id", tagHandler.DeleteTag)
			v1.POST("/memories/:id/tags", tagHandler.TagMemory)
			v1.DELETE("/memories/:id/tags/:tag_id", tagHandler.UntagMemory)
			v1.GET("/memories/:id/tags", tagHandler.GetMemoryTags)
		}

		// Graph (entities + relations)
		if deps.GraphManager != nil {
			graphHandler := NewGraphHandler(deps.GraphManager)
			v1.POST("/entities", graphHandler.CreateEntity)
			v1.GET("/entities", graphHandler.ListEntities)
			v1.GET("/entities/:id", graphHandler.GetEntity)
			v1.PUT("/entities/:id", graphHandler.UpdateEntity)
			v1.DELETE("/entities/:id", graphHandler.DeleteEntity)
			v1.GET("/entities/:id/relations", graphHandler.GetEntityRelations)
			v1.GET("/entities/:id/memories", graphHandler.GetEntityMemories)
			v1.POST("/entity-relations", graphHandler.CreateRelation)
			v1.DELETE("/entity-relations/:id", graphHandler.DeleteRelation)
			v1.POST("/memory-entities", graphHandler.CreateMemoryEntity)
			v1.DELETE("/memory-entities", graphHandler.DeleteMemoryEntity)
		}

		// Documents
		if deps.DocProcessor != nil {
			docHandler := NewDocumentHandler(deps.DocProcessor, deps.FileStore, deps.DocumentConfig)
			docGroup := v1.Group("/documents")
			{
				docGroup.POST("/upload", MaxBodySizeMiddleware(deps.DocumentConfig.MaxFileSize+1<<20), writeRateLimit, docHandler.Upload)
				docGroup.GET("", docHandler.List)
				docGroup.GET("/:id", docHandler.Get)
				docGroup.GET("/:id/status", docHandler.Status)
				docGroup.DELETE("/:id", docHandler.Delete)
				docGroup.POST("/:id/reprocess", docHandler.Process)
			}
		}

		// LLM 密集型接口使用更严格的速率限制 / Stricter rate limit for LLM-intensive endpoints
		llmRateLimit := RateLimitMiddleware(2, 5) // 2 rps, burst 5

		// Extract 实体抽取 / Entity extraction
		if deps.Extractor != nil {
			extractHandler := NewExtractHandler(deps.Extractor)
			v1.POST("/memories/:id/extract", llmRateLimit, extractHandler.Extract)
		}

		// Reflect 反思推理 / Reflect reasoning
		if deps.ReflectEngine != nil {
			reflectHandler := NewReflectHandler(deps.ReflectEngine, deps.ReflectConfig)
			v1.POST("/reflect", llmRateLimit, reflectHandler.Reflect)
		}
	}

	return r
}
