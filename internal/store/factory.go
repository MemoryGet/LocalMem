package store

import (
	"context"
	"database/sql"
	"fmt"

	"iclude/internal/config"
	"iclude/internal/logger"
	"iclude/pkg/tokenizer"

	"go.uber.org/zap"
)

// Stores 聚合所有存储后端 / Aggregate of all storage backends
type Stores struct {
	MemoryStore   MemoryStore
	VectorStore   VectorStore         // 可为 nil / may be nil
	Embedder      Embedder            // 可为 nil / may be nil
	ContextStore  ContextStore        // 可为 nil / may be nil
	TagStore      TagStore            // 可为 nil / may be nil
	GraphStore    GraphStore          // 可为 nil / may be nil
	DocumentStore DocumentStore       // 可为 nil / may be nil
	Tokenizer     tokenizer.Tokenizer // 可为 nil / may be nil (SQLite 未启用时)
}

// InitStores 根据配置初始化存储后端 / Initialize storage backends based on config
// 直接构造，不再使用函数指针模式
func InitStores(ctx context.Context, cfg config.Config, embedder Embedder) (*Stores, error) {
	stores := &Stores{
		Embedder: embedder,
	}

	// SQLite
	if cfg.Storage.SQLite.Enabled {
		weights := [3]float64{
			cfg.Storage.SQLite.Search.BM25Weights.Content,
			cfg.Storage.SQLite.Search.BM25Weights.Abstract,
			cfg.Storage.SQLite.Search.BM25Weights.Summary,
		}

		// 创建分词器 / Create tokenizer based on config
		tok := newTokenizer(cfg.Storage.SQLite.Tokenizer)
		stores.Tokenizer = tok
		logger.Info("tokenizer initialized", zap.String("provider", tok.Name()))

		ms, err := NewSQLiteMemoryStore(cfg.Storage.SQLite.Path, weights, tok)
		if err != nil {
			return nil, fmt.Errorf("failed to create SQLite store: %w", err)
		}
		if err := ms.Init(ctx); err != nil {
			return nil, fmt.Errorf("failed to init SQLite store: %w", err)
		}
		stores.MemoryStore = ms
		logger.Info("SQLite store initialized", zap.String("path", cfg.Storage.SQLite.Path))

		// 从 MemoryStore 获取 *sql.DB，创建其他 store
		if db, ok := ms.DB().(*sql.DB); ok {
			stores.ContextStore = NewSQLiteContextStore(db)
			stores.TagStore = NewSQLiteTagStore(db)
			stores.GraphStore = NewSQLiteGraphStore(db)
			stores.DocumentStore = NewSQLiteDocumentStore(db)
			logger.Info("additional stores initialized (context, tag, graph, document)")
		}
	}

	// Qdrant
	if cfg.Storage.Qdrant.Enabled {
		vs := NewQdrantVectorStore(
			cfg.Storage.Qdrant.URL,
			cfg.Storage.Qdrant.Collection,
			cfg.Storage.Qdrant.Dimension,
		)
		if err := vs.Init(ctx); err != nil {
			logger.Warn("failed to init Qdrant store, continuing without vector search",
				zap.Error(err),
			)
		} else {
			stores.VectorStore = vs
			logger.Info("Qdrant store initialized",
				zap.String("url", cfg.Storage.Qdrant.URL),
				zap.String("collection", cfg.Storage.Qdrant.Collection),
			)
		}
	}

	if stores.MemoryStore == nil {
		return nil, fmt.Errorf("at least one storage backend (SQLite) must be enabled")
	}

	return stores, nil
}

// Close 关闭所有存储连接 / Close all storage connections
func (s *Stores) Close() {
	if s.MemoryStore != nil {
		if err := s.MemoryStore.Close(); err != nil {
			logger.Error("failed to close memory store", zap.Error(err))
		}
	}
	if s.VectorStore != nil {
		if err := s.VectorStore.Close(); err != nil {
			logger.Error("failed to close vector store", zap.Error(err))
		}
	}
}

// newTokenizer 根据配置创建分词器 / Create tokenizer based on config
func newTokenizer(cfg config.TokenizerConfig) tokenizer.Tokenizer {
	switch cfg.Provider {
	case "gse":
		tok, err := tokenizer.NewGseTokenizer(cfg.DictPath, cfg.StopwordFiles)
		if err != nil {
			logger.Warn("failed to create gse tokenizer, falling back to simple",
				zap.Error(err),
			)
			return tokenizer.NewSimpleTokenizer()
		}
		return tok
	case "jieba":
		return tokenizer.NewJiebaTokenizer(cfg.JiebaURL)
	case "simple":
		return tokenizer.NewSimpleTokenizer()
	case "noop", "":
		return tokenizer.NewNoopTokenizer()
	default:
		logger.Warn("unknown tokenizer provider, falling back to simple",
			zap.String("provider", cfg.Provider),
		)
		return tokenizer.NewSimpleTokenizer()
	}
}
