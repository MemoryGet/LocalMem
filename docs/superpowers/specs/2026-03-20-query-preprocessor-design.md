# Query Preprocessor 设计文档

## 概述

在 `Retriever.Retrieve()` 入口插入可选的 Query 预处理步骤，将原始 query 转换为结构化的 `QueryPlan`，为三通道检索（FTS5/Qdrant/Graph）提供差异化输入和动态权重。

支持两种模式：
- **规则式**（默认）— 纯算法，零 LLM 调用，毫秒级延迟
- **LLM 增强**（config 开关）— 在规则式基础上叠加 LLM query rewriting + 意图分类，失败时静默降级到规则式

## 动机

当前检索器直接将原始 query 原样传递给所有通道，缺少 query 理解层：
- FTS5 和 Qdrant 收到相同的文本输入，但它们各自擅长的查询形式不同
- 通道权重固定，无法根据 query 特征动态调整
- 图谱通道依赖 FTS5 反查实体，无独立的实体快速匹配路径

## QueryPlan 结构

```go
type QueryIntent string

const (
    IntentKeyword    QueryIntent = "keyword"    // 精确查找
    IntentSemantic   QueryIntent = "semantic"   // 模糊/探索性
    IntentTemporal   QueryIntent = "temporal"   // 时间相关
    IntentRelational QueryIntent = "relational" // 关联查询
    IntentGeneral    QueryIntent = "general"    // 默认
)

type QueryPlan struct {
    OriginalQuery string         // 原始 query（保底）
    SemanticQuery string         // 语义通道用（LLM 改写或原始）
    Keywords      []string       // FTS5 通道用（分词提取）
    Entities      []string       // 图谱通道用（匹配到的实体 ID）
    Intent        QueryIntent    // 意图分类
    Weights       ChannelWeights // 动态权重
}

type ChannelWeights struct {
    FTS    float64
    Qdrant float64
    Graph  float64
}
```

## 处理流程

### 规则式（始终执行）

1. **分词提取关键词** — 复用项目已有的 `tokenizer.Tokenizer`（simple/jieba），调用 `Tokenize()` 返回空格分隔字符串，再 `strings.Fields()` 拆为 `[]string`，过滤空串后得到 `Keywords`
2. **实体快速匹配** — 用 `Keywords` 在 `GraphStore.ListEntities(ctx, scope, "", 100)` 做大小写不敏感的字符串匹配，命中的填入 `Entities`。`scope` 从调用方传入（来自 `req.Filters.Scope`），`limit=100` 与现有 `llmExtractEntities` 一致。GraphStore 为 nil 时跳过此步。
3. **规则意图分类** — 基于分词后的 token 数量（非原始字符数，兼容 CJK），按优先级匹配：
   - 包含时间关键词（"最近"/"上周"/"recent"/"last week"等）→ `temporal`
   - 包含关联词（"相关"/"关于"/"related to"等）→ `relational`
   - token 数 ≤5 且无停用词 → `keyword`
   - token 数 >15 或包含疑问探索词 → `semantic`
   - 其他 → `general`
4. **意图映射权重** — 意图乘以 config base weight 得到最终通道权重

### LLM 增强（config 开启时）

在规则式之后执行，覆盖部分字段：

1. 单次 LLM 调用，prompt 要求输出 JSON：
   ```json
   {
     "rewritten_query": "改写后的语义查询",
     "intent": "keyword|semantic|temporal|relational|general",
     "keywords": ["可选", "补充关键词"]
   }
   ```
2. `rewritten_query` → 覆盖 `SemanticQuery`
3. `intent` → 覆盖规则式的 `Intent`，重新映射权重
4. `keywords`（如有）→ 合并到 `Keywords`
5. LLM 调用失败/超时/解析错误 → 保留规则式结果，仅记 warn 日志

### 意图 → 权重系数映射

| Intent | FTS 系数 | Qdrant 系数 | Graph 系数 |
|--------|---------|------------|-----------|
| `keyword` | 1.5 | 0.6 | 1.2 |
| `semantic` | 0.6 | 1.5 | 0.8 |
| `temporal` | 1.3 | 0.8 | 0.6 |
| `relational` | 0.8 | 0.7 | 1.8 |
| `general` | 1.0 | 1.0 | 1.0 |

最终权重 = config base weight × 意图系数。例如 `fts_weight=1.0` + `semantic` 意图 → FTS 实际权重 = 1.0 × 0.6 = 0.6。

## 配置

```yaml
retrieval:
  # 已有字段
  fts_weight: 1.0
  qdrant_weight: 1.0
  graph_weight: 0.8
  # 新增
  preprocess:
    enabled: true       # 是否启用预处理（false 时行为与当前完全一致）
    use_llm: false      # 是否使用 LLM 增强
    llm_timeout: "5s"   # LLM 调用超时
```

对应 Go 结构体：

```go
type PreprocessConfig struct {
    Enabled    bool          `mapstructure:"enabled"`
    UseLLM     bool          `mapstructure:"use_llm"`
    LLMTimeout time.Duration `mapstructure:"llm_timeout"`
}
```

添加到 `RetrievalConfig` 中：

```go
type RetrievalConfig struct {
    // ... 已有字段
    Preprocess PreprocessConfig `mapstructure:"preprocess"`
}
```

## Retriever 集成

`Retrieve()` 方法改动：

```
preprocess.enabled=false → 行为完全不变，零改动
preprocess.enabled=true  → 先 Process(ctx, query, scope) 得到 QueryPlan：
  - FTS 通道：用 strings.Join(Keywords, " ") 作为 FTS5 MATCH 查询文本（空则回退 OriginalQuery）
    注：FTS5 对空格分隔的词做 OR 匹配，与分词后的 Keywords 语义一致
  - Qdrant 通道：用 SemanticQuery 生成 embedding（空则回退 OriginalQuery）
  - Graph 通道：优先用 Entities 跳过 FTS5 反查阶段；Entities 为空时走现有 FTS5 反查逻辑
  - RRF 融合：用 QueryPlan.Weights 替代 config 中的固定权重
  - req.Filters 继续传递到各通道的 Filtered 方法，与 QueryPlan 不冲突
```

## 依赖注入

`Preprocessor` 需要：
- `tokenizer.Tokenizer` — 分词（从 `store.Stores.Tokenizer` 获取，需在 `Stores` 结构体中新增公开字段）
- `store.GraphStore` — 实体匹配（可为 nil，nil 时跳过实体匹配）
- `llm.Provider` — LLM 增强（可为 nil，nil 时跳过 LLM，即使 `use_llm=true`）
- `config.PreprocessConfig` — 配置

### Tokenizer 暴露

当前 tokenizer 在 `store.InitStores()` 内部创建，作为 `SQLiteMemoryStore` 的私有字段。需要在 `store.Stores` 结构体中新增 `Tokenizer tokenizer.Tokenizer` 公开字段，在 `InitStores()` 中赋值，使 `main.go` 可以传递给 `Preprocessor`。

### Process() 方法签名

```go
func (p *Preprocessor) Process(ctx context.Context, query string, scope string) (*QueryPlan, error)
```

`scope` 由 `Retriever.Retrieve()` 从 `req.Filters.Scope` 提取后传入。

### main.go 构造

```go
var preprocessor *search.Preprocessor
if cfg.Retrieval.Preprocess.Enabled {
    preprocessor = search.NewPreprocessor(stores.Tokenizer, stores.GraphStore, llmProvider, cfg.Retrieval.Preprocess)
}
ret := search.NewRetriever(..., preprocessor)
```

`Retriever` 结构体新增 `preprocessor *Preprocessor` 字段（可为 nil）。

## 文件变更

| 文件 | 变更类型 | 说明 |
|------|---------|------|
| `internal/config/config.go` | 修改 | 新增 `PreprocessConfig` + `viper.SetDefault` 调用 |
| `internal/store/factory.go` | 修改 | `Stores` 结构体新增 `Tokenizer` 公开字段 |
| `internal/search/preprocess.go` | 新增 | `Preprocessor` 结构体、`Process()` 方法、规则引擎、LLM 增强、意图映射 |
| `internal/search/retriever.go` | 修改 | `NewRetriever` 接收 `*Preprocessor`；`Retrieve()` 调用预处理；各通道消费 `QueryPlan` |
| `cmd/server/main.go` | 修改 | 构造 `Preprocessor` 并注入 |
| `testing/search/preprocess_test.go` | 新增 | 表驱动测试：规则意图分类、关键词提取、实体匹配、LLM 增强降级 |

## 向后兼容

- `preprocess.enabled` 默认 `true`，`use_llm` 默认 `false` → 开箱即用规则式预处理
- `preprocess.enabled=false` → 检索行为与改动前完全一致
- 不改变任何 API 请求/响应结构
- 不改变任何现有配置字段的语义
