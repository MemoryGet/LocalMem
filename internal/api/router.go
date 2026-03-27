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
	Extractor          *memory.Extractor // 可为 nil / may be nil
	AuthConfig         config.AuthConfig
	ReflectConfig      config.ReflectConfig
	CORSAllowedOrigins []string
}

// SetupRouter 初始化路由 / Initialize router with all handlers
func SetupRouter(deps *RouterDeps) *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()

	r.Use(gin.Recovery())
	r.Use(CORSMiddleware(deps.CORSAllowedOrigins))
	r.Use(LoggerMiddleware())

	r.GET("/health", func(c *gin.Context) {
		Success(c, gin.H{"status": "ok"})
	})

	v1 := r.Group("/v1")
	v1.Use(AuthMiddleware(deps.AuthConfig))
	v1.Use(IdentityMiddleware())
	{
		// Memory CRUD
		memHandler := NewMemoryHandler(deps.MemManager)
		v1.POST("/memories", memHandler.Create)
		v1.GET("/memories", memHandler.List)
		v1.GET("/memories/:id", memHandler.Get)
		v1.PUT("/memories/:id", memHandler.Update)
		v1.DELETE("/memories/:id", memHandler.Delete)
		v1.DELETE("/memories/:id/soft", memHandler.SoftDelete)
		v1.POST("/memories/:id/restore", memHandler.Restore)
		v1.POST("/memories/:id/reinforce", memHandler.Reinforce)

		// Maintenance
		v1.POST("/maintenance/cleanup", memHandler.Cleanup)

		// Conversations
		convHandler := NewConversationHandler(deps.MemManager)
		v1.POST("/conversations", convHandler.Ingest)
		v1.GET("/conversations/:context_id", convHandler.GetConversation)

		// Search
		searchHandler := NewSearchHandler(deps.Retriever)
		v1.POST("/retrieve", searchHandler.Retrieve)
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
			docHandler := NewDocumentHandler(deps.DocProcessor)
			v1.POST("/documents", docHandler.Upload)
			v1.GET("/documents", docHandler.List)
			v1.GET("/documents/:id", docHandler.Get)
			v1.DELETE("/documents/:id", docHandler.Delete)
			v1.POST("/documents/:id/reprocess", docHandler.Process)
		}

		// Extract 实体抽取 / Entity extraction
		if deps.Extractor != nil {
			extractHandler := NewExtractHandler(deps.Extractor)
			v1.POST("/memories/:id/extract", extractHandler.Extract)
		}

		// Reflect 反思推理 / Reflect reasoning
		if deps.ReflectEngine != nil {
			reflectHandler := NewReflectHandler(deps.ReflectEngine, deps.ReflectConfig)
			v1.POST("/reflect", reflectHandler.Reflect)
		}
	}

	return r
}
