// Package config 应用配置加载与管理 / Application configuration loading and management
package config

import "time"

// DocumentConfig 文档处理配置 / Document processing configuration
type DocumentConfig struct {
	Enabled           bool            `mapstructure:"enabled"`
	MaxConcurrent     int             `mapstructure:"max_concurrent"`
	ProcessTimeout    time.Duration   `mapstructure:"process_timeout"`
	MaxFileSize       int64           `mapstructure:"max_file_size"`
	CleanupAfterParse bool            `mapstructure:"cleanup_after_parse"`
	KeepImages        bool            `mapstructure:"keep_images"`
	AllowedTypes      []string        `mapstructure:"allowed_types"`
	FileStore         FileStoreConfig `mapstructure:"file_store"`
	Docling           DoclingConfig   `mapstructure:"docling"`
	Tika              TikaConfig      `mapstructure:"tika"`
	Chunking          ChunkingConfig  `mapstructure:"chunking"`
}

// FileStoreConfig 文件存储配置 / File storage configuration
type FileStoreConfig struct {
	Provider string           `mapstructure:"provider"`
	Local    LocalStoreConfig `mapstructure:"local"`
}

// LocalStoreConfig 本地文件存储配置 / Local file storage configuration
type LocalStoreConfig struct {
	BaseDir string `mapstructure:"base_dir"`
}

// DoclingConfig Docling 解析服务配置 / Docling parser configuration
type DoclingConfig struct {
	URL     string        `mapstructure:"url"`
	Timeout time.Duration `mapstructure:"timeout"`
}

// TikaConfig Tika 解析服务配置 / Tika parser configuration
type TikaConfig struct {
	URL     string        `mapstructure:"url"`
	Timeout time.Duration `mapstructure:"timeout"`
}

// ChunkingConfig 分块配置 / Chunking configuration
type ChunkingConfig struct {
	MaxTokens       int  `mapstructure:"max_tokens"`
	OverlapTokens   int  `mapstructure:"overlap_tokens"`
	ContextPrefix   bool `mapstructure:"context_prefix"`
	KeepTableIntact bool `mapstructure:"keep_table_intact"`
	KeepCodeIntact  bool `mapstructure:"keep_code_intact"`
}
