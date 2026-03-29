// Package config 应用配置加载与管理 / Application configuration loading and management
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"iclude/internal/logger"

	"github.com/joho/godotenv"
	"github.com/spf13/viper"
)

// Config 应用总配置 / Top-level application configuration
type Config struct {
	Storage         StorageConfig         `mapstructure:"storage"`
	Server          ServerConfig          `mapstructure:"server"`
	Auth            AuthConfig            `mapstructure:"auth"`
	Partition       PartitionConfig       `mapstructure:"partitions"`
	LLM             LLMConfig             `mapstructure:"llm"`
	Reflect         ReflectConfig         `mapstructure:"reflect"`
	Extract         ExtractConfig         `mapstructure:"extract"`
	Retrieval       RetrievalConfig       `mapstructure:"retrieval"`
	Crystallization CrystallizationConfig `mapstructure:"crystallization"`
	Dedup           DedupConfig           `mapstructure:"dedup"`
	Scheduler       SchedulerConfig       `mapstructure:"scheduler"`
	Consolidation   ConsolidationConfig   `mapstructure:"consolidation"`
	Heartbeat       HeartbeatConfig       `mapstructure:"heartbeat"`
	MCP             MCPConfig             `mapstructure:"mcp"`
	Queue           QueueConfig           `mapstructure:"queue"`
	Hooks           HooksConfig           `mapstructure:"hooks"`
	Document        DocumentConfig        `mapstructure:"document"`
}

// StorageConfig 存储配置 / Storage configuration
type StorageConfig struct {
	SQLite SQLiteConfig `mapstructure:"sqlite"`
	Qdrant QdrantConfig `mapstructure:"qdrant"`
}

// QdrantConfig Qdrant 向量数据库配置 / Qdrant vector database configuration
type QdrantConfig struct {
	Enabled    bool   `mapstructure:"enabled"`
	URL        string `mapstructure:"url"`
	Collection string `mapstructure:"collection"`
	Dimension  int    `mapstructure:"dimension"`
}

// SQLiteConfig SQLite 数据库配置 / SQLite database configuration
type SQLiteConfig struct {
	Enabled   bool            `mapstructure:"enabled"`
	Path      string          `mapstructure:"path"`
	Search    SearchConfig    `mapstructure:"search"`
	Tokenizer TokenizerConfig `mapstructure:"tokenizer"`
}

// TokenizerConfig 分词器配置 / Tokenizer configuration
type TokenizerConfig struct {
	Provider      string   `mapstructure:"provider"`       // noop | simple | jieba | gse
	JiebaURL      string   `mapstructure:"jieba_url"`      // jieba HTTP 服务地址 / Jieba HTTP service URL
	DictPath      string   `mapstructure:"dict_path"`      // gse 自定义词典路径，空=内置词典 / gse custom dict path, empty=built-in
	StopwordFiles []string `mapstructure:"stopword_files"` // 停用词文件路径 / Stopword file paths
}

// SearchConfig 搜索配置 / Search configuration
type SearchConfig struct {
	BM25Weights BM25WeightsConfig `mapstructure:"bm25_weights"`
}

// BM25WeightsConfig BM25 列权重配置 / BM25 column weight configuration
type BM25WeightsConfig struct {
	Content  float64 `mapstructure:"content"`
	Abstract float64 `mapstructure:"abstract"`
	Summary  float64 `mapstructure:"summary"`
}

// ServerConfig 服务器配置 / Server configuration
type ServerConfig struct {
	Port        int  `mapstructure:"port"`
	AuthEnabled bool `mapstructure:"auth_enabled"`
}

// AuthConfig 认证配置 / Authentication configuration
type AuthConfig struct {
	Enabled            bool         `mapstructure:"enabled"`
	APIKeys            []APIKeyItem `mapstructure:"api_keys"`
	CORSAllowedOrigins []string     `mapstructure:"cors_allowed_origins"`
}

// APIKeyItem API Key 配置项 / API Key configuration item
type APIKeyItem struct {
	Key    string `mapstructure:"key"`
	TeamID string `mapstructure:"team_id"`
	Name   string `mapstructure:"name"`
}

// PartitionConfig 分区配置 / Partition configuration
type PartitionConfig struct {
	Enabled     bool   `mapstructure:"enabled"`
	CatalogPath string `mapstructure:"catalog_path"`
}

// LLMConfig LLM 及 Embedding 配置 / LLM and embedding configuration
type LLMConfig struct {
	DefaultProvider string              `mapstructure:"default_provider"`
	OpenAI          OpenAIConfig        `mapstructure:"openai"`
	Claude          ClaudeConfig        `mapstructure:"claude"`
	Ollama          OllamaConfig        `mapstructure:"ollama"`
	Embedding       EmbeddingConfig     `mapstructure:"embedding"`
	Fallback        []FallbackLLMConfig `mapstructure:"fallback"`
}

// FallbackLLMConfig 备用 LLM 提供者配置项 / Fallback LLM provider configuration entry
type FallbackLLMConfig struct {
	// Name 提供者可读名称，用于日志 / Human-readable provider name for logging
	Name string `mapstructure:"name"`
	// BaseURL OpenAI 兼容 API 基础地址 / OpenAI-compatible API base URL
	BaseURL string `mapstructure:"base_url"`
	// APIKey 鉴权密钥，可为空（如本地 Ollama）/ Auth key, may be empty (e.g. local Ollama)
	APIKey string `mapstructure:"api_key"`
	// Model 模型标识符 / Model identifier
	Model string `mapstructure:"model"`
}

// EmbeddingConfig 向量嵌入配置 / Embedding configuration
type EmbeddingConfig struct {
	Provider string `mapstructure:"provider"`
	Model    string `mapstructure:"model"`
}

// OpenAIConfig OpenAI 配置 / OpenAI configuration
type OpenAIConfig struct {
	APIKey  string `mapstructure:"api_key"`
	BaseURL string `mapstructure:"base_url"`
	Model   string `mapstructure:"model"`
}

// ClaudeConfig Claude 配置 / Claude configuration
type ClaudeConfig struct {
	APIKey string `mapstructure:"api_key"`
	Model  string `mapstructure:"model"`
}

// OllamaConfig Ollama 配置 / Ollama configuration
type OllamaConfig struct {
	BaseURL string `mapstructure:"base_url"`
	Model   string `mapstructure:"model"`
}

// ReflectConfig 反思引擎配置 / Reflect engine configuration
type ReflectConfig struct {
	MaxRounds    int           `mapstructure:"max_rounds"`
	TokenBudget  int           `mapstructure:"token_budget"`
	RoundTimeout time.Duration `mapstructure:"round_timeout"`
	AutoSave     bool          `mapstructure:"auto_save"`
}

// ExtractConfig 实体抽取配置 / Entity extraction config
type ExtractConfig struct {
	MaxEntities         int           `mapstructure:"max_entities"`
	MaxRelations        int           `mapstructure:"max_relations"`
	NormalizeEnabled    bool          `mapstructure:"normalize_enabled"`
	NormalizeCandidates int           `mapstructure:"normalize_candidates"`
	Timeout             time.Duration `mapstructure:"timeout"`
}

// RetrievalConfig 检索配置 / Retrieval config
type RetrievalConfig struct {
	GraphEnabled     bool             `mapstructure:"graph_enabled"`
	GraphDepth       int              `mapstructure:"graph_depth"`
	GraphWeight      float64          `mapstructure:"graph_weight"`
	FTSWeight        float64          `mapstructure:"fts_weight"`
	QdrantWeight     float64          `mapstructure:"qdrant_weight"`
	GraphFTSTop      int              `mapstructure:"graph_fts_top"`
	GraphEntityLimit int              `mapstructure:"graph_entity_limit"`
	AccessAlpha      float64          `mapstructure:"access_alpha"` // 访问频率阻尼系数 / Access frequency damping coefficient
	MMR              MMRConfig        `mapstructure:"mmr"`
	Preprocess       PreprocessConfig `mapstructure:"preprocess"`
}

// MMRConfig MMR 多样性重排配置 / MMR diversity re-ranking configuration
type MMRConfig struct {
	Enabled bool    `mapstructure:"enabled"`
	Lambda  float64 `mapstructure:"lambda"` // 相关性 vs 多样性权衡，推荐 0.7 / Relevance vs diversity tradeoff
}

// CrystallizationConfig 自动晶化配置 / Auto-crystallization configuration
type CrystallizationConfig struct {
	Enabled           bool          `mapstructure:"enabled"`
	MinReinforceCount int           `mapstructure:"min_reinforce_count"` // 最小强化次数 / Min reinforce count threshold
	MinStrength       float64       `mapstructure:"min_strength"`        // 最小强度 / Min strength threshold
	MinAge            time.Duration `mapstructure:"min_age"`             // 最小存活时间 / Min memory age
}

// DedupConfig 去重配置 / Deduplication configuration
type DedupConfig struct {
	HashEnabled    bool    `mapstructure:"hash_enabled"`    // P0 哈希去重 / Hash dedup
	VectorEnabled  bool    `mapstructure:"vector_enabled"`  // P1 余弦去重 / Cosine dedup
	SkipThreshold  float64 `mapstructure:"skip_threshold"`  // 直接跳过阈值 / Skip threshold (>=0.95)
	MergeThreshold float64 `mapstructure:"merge_threshold"` // 合并判断阈值 / Merge threshold (>=0.85)
}

// HeartbeatConfig HEARTBEAT 自主巡检配置 / HEARTBEAT autonomous inspection configuration
type HeartbeatConfig struct {
	Enabled              bool          `mapstructure:"enabled"`
	Interval             time.Duration `mapstructure:"interval"`
	ContradictionEnabled bool          `mapstructure:"contradiction_enabled"`         // 矛盾检测开关 / Contradiction detection toggle
	ContradictionMaxComp int           `mapstructure:"contradiction_max_comparisons"` // 每轮最大比较数 / Max comparisons per run
	DecayAuditMinAgeDays int           `mapstructure:"decay_audit_min_age_days"`      // 衰减审计最小天数 / Min age for decay audit
	DecayAuditThreshold  float64       `mapstructure:"decay_audit_threshold"`         // 衰减审计强度阈值 / Strength threshold for decay audit
}

// MCPConfig MCP 服务器配置 / MCP server configuration
type MCPConfig struct {
	Enabled           bool   `mapstructure:"enabled"`
	Port              int    `mapstructure:"port"`
	DefaultTeamID     string `mapstructure:"default_team_id"`
	DefaultOwnerID    string `mapstructure:"default_owner_id"`
	CORSAllowedOrigin string `mapstructure:"cors_allowed_origin"`
	APIToken          string `mapstructure:"api_token"`
}

// QueueConfig 异步任务队列配置 / Async task queue configuration
type QueueConfig struct {
	Enabled      bool          `mapstructure:"enabled"`
	PollInterval time.Duration `mapstructure:"poll_interval"`
	MaxRetries   int           `mapstructure:"max_retries"`
	StaleTimeout time.Duration `mapstructure:"stale_timeout"`
}

// SchedulerConfig 后台调度器配置 / Background scheduler configuration
type SchedulerConfig struct {
	Enabled               bool          `mapstructure:"enabled"`
	CleanupInterval       time.Duration `mapstructure:"cleanup_interval"`
	AccessFlushInterval   time.Duration `mapstructure:"access_flush_interval"`
	ConsolidationInterval time.Duration `mapstructure:"consolidation_interval"`
}

// ConsolidationConfig 记忆归纳配置 / Memory consolidation configuration
type ConsolidationConfig struct {
	Enabled             bool    `mapstructure:"enabled"`
	MinAgeDays          int     `mapstructure:"min_age_days"`
	SimilarityThreshold float64 `mapstructure:"similarity_threshold"`
	MinClusterSize      int     `mapstructure:"min_cluster_size"`
	MaxMemoriesPerRun   int     `mapstructure:"max_memories_per_run"`
}

// PreprocessConfig 查询预处理配置 / Query preprocessing configuration
type PreprocessConfig struct {
	Enabled       bool          `mapstructure:"enabled"`
	UseLLM        bool          `mapstructure:"use_llm"`
	LLMTimeout    time.Duration `mapstructure:"llm_timeout"`
	StopwordFiles []string      `mapstructure:"stopword_files"` // 外部停用词文件路径 / External stopword file paths
}

// AppConfig 全局配置单例 / Global config singleton
var AppConfig Config

// LoadConfig 加载应用配置 / Load application configuration
// 优先级: .env → 默认值 → 环境变量 → config.yaml
// Returns: error if config file exists but cannot be parsed
func LoadConfig() error {
	// 加载 .env 文件
	_ = godotenv.Load()

	// 设置默认值
	viper.SetDefault("storage.sqlite.enabled", true)
	viper.SetDefault("storage.sqlite.path", "./data/iclude.db")
	viper.SetDefault("server.port", 8080)
	viper.SetDefault("server.auth_enabled", true)
	viper.SetDefault("partitions.enabled", false)
	viper.SetDefault("partitions.catalog_path", "./data/partitions.db")
	viper.SetDefault("storage.sqlite.search.bm25_weights.content", 10.0)
	viper.SetDefault("storage.sqlite.search.bm25_weights.abstract", 5.0)
	viper.SetDefault("storage.sqlite.search.bm25_weights.summary", 3.0)
	viper.SetDefault("storage.sqlite.tokenizer.provider", "simple")
	viper.SetDefault("storage.sqlite.tokenizer.jieba_url", "http://localhost:8866")
	viper.SetDefault("storage.sqlite.tokenizer.dict_path", "")
	viper.SetDefault("storage.sqlite.tokenizer.stopword_files", []string{"config/stopwords_en.txt", "config/stopwords_zh.txt"})
	viper.SetDefault("storage.qdrant.enabled", false)
	viper.SetDefault("storage.qdrant.url", "http://localhost:6333")
	viper.SetDefault("storage.qdrant.collection", "memories")
	viper.SetDefault("storage.qdrant.dimension", 384)
	viper.SetDefault("llm.default_provider", "openai")
	viper.SetDefault("llm.openai.model", "gpt-4")
	viper.SetDefault("llm.embedding.provider", "openai")
	viper.SetDefault("llm.embedding.model", "text-embedding-3-small")
	// Reflect 默认值 / Reflect defaults
	viper.SetDefault("llm.openai.base_url", "")
	viper.SetDefault("reflect.max_rounds", 3)
	viper.SetDefault("reflect.token_budget", 4096)
	viper.SetDefault("reflect.round_timeout", "30s")
	viper.SetDefault("reflect.auto_save", false)
	// Extract 默认值 / Extract defaults
	viper.SetDefault("extract.max_entities", 20)
	viper.SetDefault("extract.max_relations", 30)
	viper.SetDefault("extract.normalize_enabled", true)
	viper.SetDefault("extract.normalize_candidates", 20)
	viper.SetDefault("extract.timeout", "30s")
	// Retrieval 默认值 / Retrieval defaults
	viper.SetDefault("retrieval.graph_enabled", true)
	viper.SetDefault("retrieval.graph_depth", 1)
	viper.SetDefault("retrieval.graph_weight", 0.8)
	viper.SetDefault("retrieval.fts_weight", 1.0)
	viper.SetDefault("retrieval.qdrant_weight", 1.0)
	viper.SetDefault("retrieval.graph_fts_top", 5)
	viper.SetDefault("retrieval.graph_entity_limit", 10)
	viper.SetDefault("retrieval.access_alpha", 0.15)
	viper.SetDefault("retrieval.mmr.enabled", false)
	viper.SetDefault("retrieval.mmr.lambda", 0.7)
	// Crystallization 默认值 / Crystallization defaults
	viper.SetDefault("crystallization.enabled", true)
	viper.SetDefault("crystallization.min_reinforce_count", 5)
	viper.SetDefault("crystallization.min_strength", 0.7)
	viper.SetDefault("crystallization.min_age", "720h")
	// Dedup 默认值 / Dedup defaults
	viper.SetDefault("dedup.hash_enabled", true)
	viper.SetDefault("dedup.vector_enabled", false)
	viper.SetDefault("dedup.skip_threshold", 0.95)
	viper.SetDefault("dedup.merge_threshold", 0.85)
	// Scheduler 默认值 / Scheduler defaults
	viper.SetDefault("scheduler.enabled", false)
	viper.SetDefault("scheduler.cleanup_interval", "6h")
	viper.SetDefault("scheduler.access_flush_interval", "5m")
	viper.SetDefault("scheduler.consolidation_interval", "24h")
	// Heartbeat 默认值 / Heartbeat defaults
	viper.SetDefault("heartbeat.enabled", false)
	viper.SetDefault("heartbeat.interval", "6h")
	viper.SetDefault("heartbeat.contradiction_enabled", true)
	viper.SetDefault("heartbeat.contradiction_max_comparisons", 50)
	viper.SetDefault("heartbeat.decay_audit_min_age_days", 90)
	viper.SetDefault("heartbeat.decay_audit_threshold", 0.1)
	// Preprocess 默认值 / Preprocess defaults
	viper.SetDefault("retrieval.preprocess.enabled", true)
	viper.SetDefault("retrieval.preprocess.use_llm", false)
	viper.SetDefault("retrieval.preprocess.llm_timeout", "5s")
	viper.SetDefault("retrieval.preprocess.stopword_files", []string{"config/stopwords_en.txt", "config/stopwords_zh.txt"})
	// MCP 默认值 / MCP defaults
	viper.SetDefault("mcp.enabled", false)
	viper.SetDefault("mcp.port", 8081)
	viper.SetDefault("mcp.default_team_id", "default")
	viper.SetDefault("mcp.default_owner_id", "mcp-user")
	viper.SetDefault("mcp.cors_allowed_origin", "*")
	viper.SetDefault("mcp.api_token", "")
	// Queue 默认值 / Queue defaults
	viper.SetDefault("queue.enabled", true)
	viper.SetDefault("queue.poll_interval", "10s")
	viper.SetDefault("queue.max_retries", 3)
	viper.SetDefault("queue.stale_timeout", "5m")
	// Hooks 默认值 / Hooks defaults
	viper.SetDefault("hooks.enabled", false)
	viper.SetDefault("hooks.mcp_url", "http://localhost:8081")
	viper.SetDefault("hooks.skip_tools", []string{"Glob", "Grep", "ToolSearch", "TaskCreate", "TaskUpdate", "TaskList", "TaskGet", "TodoWrite"})
	viper.SetDefault("hooks.max_input_chars", 1000)
	viper.SetDefault("hooks.max_output_chars", 500)
	viper.SetDefault("hooks.inject_limit", 20)
	viper.SetDefault("hooks.summary_limit", 50)
	// Auth 默认值 / Auth defaults
	viper.SetDefault("auth.enabled", true)
	viper.SetDefault("auth.cors_allowed_origins", []string{"*"})
	// Document 默认值 / Document defaults
	viper.SetDefault("document.enabled", false)
	viper.SetDefault("document.max_concurrent", 3)
	viper.SetDefault("document.process_timeout", "10m")
	viper.SetDefault("document.max_file_size", 104857600) // 100MB
	viper.SetDefault("document.cleanup_after_parse", true)
	viper.SetDefault("document.keep_images", true)
	viper.SetDefault("document.allowed_types", []string{"pdf", "docx", "pptx", "xlsx", "md", "html", "txt", "png", "jpg", "jpeg"})
	viper.SetDefault("document.file_store.provider", "local")
	viper.SetDefault("document.file_store.local.base_dir", "./data/uploads")
	viper.SetDefault("document.docling.url", "http://localhost:5001")
	viper.SetDefault("document.docling.timeout", "120s")
	viper.SetDefault("document.tika.url", "http://localhost:9998")
	viper.SetDefault("document.tika.timeout", "60s")
	viper.SetDefault("document.chunking.max_tokens", 512)
	viper.SetDefault("document.chunking.overlap_tokens", 50)
	viper.SetDefault("document.chunking.context_prefix", true)
	viper.SetDefault("document.chunking.keep_table_intact", true)
	viper.SetDefault("document.chunking.keep_code_intact", true)

	// 从环境变量读取
	viper.AutomaticEnv()

	// 从配置文件读取
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./config")
	viper.AddConfigPath("./deploy")

	err := viper.ReadInConfig()
	if err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return fmt.Errorf("error reading config file: %w", err)
		}
		// 配置文件不存在，使用默认值
	}

	// 解析配置
	err = viper.Unmarshal(&AppConfig)
	if err != nil {
		return fmt.Errorf("unable to decode config into struct: %w", err)
	}

	// 兼容旧配置 / Backward compatibility: server.auth_enabled → auth.enabled
	if AppConfig.Server.AuthEnabled && !AppConfig.Auth.Enabled {
		logger.Warn("server.auth_enabled is deprecated, use auth.enabled instead")
		AppConfig.Auth.Enabled = true
	}

	// 确保数据目录存在
	dataDir := filepath.Dir(AppConfig.Storage.SQLite.Path)
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return fmt.Errorf("failed to create data directory: %w", err)
	}

	if AppConfig.Partition.Enabled {
		partitionDir := filepath.Dir(AppConfig.Partition.CatalogPath)
		if err := os.MkdirAll(partitionDir, 0755); err != nil {
			return fmt.Errorf("failed to create partition directory: %w", err)
		}
	}

	return nil
}

// GetConfig 获取全局配置 / Get global configuration
func GetConfig() Config {
	return AppConfig
}
