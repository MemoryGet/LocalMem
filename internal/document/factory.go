// Package document 文档处理 / Document processing and chunking
package document

import (
	"context"
	"time"

	"iclude/internal/config"
	"iclude/internal/logger"
	"iclude/internal/store"

	"go.uber.org/zap"
)

// Pipeline 文档处理管线组件 / Document processing pipeline components
type Pipeline struct {
	Processor *Processor
	FileStore FileStore
}

// InitDocumentPipeline 初始化文档处理管线 / Initialize document processing pipeline
// 返回 nil 如果 document.enabled=false 或 DocumentStore 不可用
func InitDocumentPipeline(ctx context.Context, cfg config.DocumentConfig, docStore store.DocumentStore, memStore store.MemoryStore, embedder store.Embedder) *Pipeline {
	if !cfg.Enabled || docStore == nil {
		return nil
	}

	// FileStore
	var fileStore FileStore
	switch cfg.FileStore.Provider {
	case "local", "":
		baseDir := cfg.FileStore.Local.BaseDir
		if baseDir == "" {
			baseDir = "./data/uploads"
		}
		fileStore = NewLocalFileStore(baseDir)
		logger.Info("file store initialized", zap.String("provider", "local"), zap.String("base_dir", baseDir))
	default:
		logger.Warn("unknown file store provider, using local", zap.String("provider", cfg.FileStore.Provider))
		fileStore = NewLocalFileStore("./data/uploads")
	}

	// Parsers
	var primary Parser
	var fallback Parser

	doclingParser := NewDoclingParser(cfg.Docling.URL, cfg.Docling.Timeout)
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := doclingParser.Ping(pingCtx); err != nil {
		logger.Warn("docling not available, will skip as primary parser", zap.Error(err))
	} else {
		primary = doclingParser
		logger.Info("docling parser available", zap.String("url", cfg.Docling.URL))
	}

	tikaParser := NewTikaParser(cfg.Tika.URL, cfg.Tika.Timeout)
	pingCtx2, cancel2 := context.WithTimeout(ctx, 5*time.Second)
	defer cancel2()
	if err := tikaParser.Ping(pingCtx2); err != nil {
		logger.Warn("tika not available, will skip as fallback parser", zap.Error(err))
	} else {
		fallback = tikaParser
		logger.Info("tika parser available", zap.String("url", cfg.Tika.URL))
	}

	var parseRouter *ParseRouter
	if primary != nil || fallback != nil {
		parseRouter = NewParseRouter(primary, fallback)
	}

	// Chunker
	var chunker Chunker = NewMarkdownChunker()

	// Processor
	procCfg := ProcessorConfig{
		ProcessTimeout:    cfg.ProcessTimeout,
		CleanupAfterParse: cfg.CleanupAfterParse,
		KeepImages:        cfg.KeepImages,
		ChunkingOpts: ChunkOptions{
			MaxTokens:       cfg.Chunking.MaxTokens,
			OverlapTokens:   cfg.Chunking.OverlapTokens,
			ContextPrefix:   cfg.Chunking.ContextPrefix,
			KeepTableIntact: cfg.Chunking.KeepTableIntact,
			KeepCodeIntact:  cfg.Chunking.KeepCodeIntact,
		},
	}

	processor := NewProcessor(docStore, memStore, embedder, fileStore, parseRouter, chunker,
		WithMaxConcurrent(cfg.MaxConcurrent),
		WithProcessorConfig(procCfg),
	)

	logger.Info("document pipeline initialized",
		zap.Bool("docling", primary != nil),
		zap.Bool("tika", fallback != nil),
	)

	return &Pipeline{
		Processor: processor,
		FileStore: fileStore,
	}
}
