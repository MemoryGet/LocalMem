# Reflect Engine 设计规格 / Reflect Engine Design Spec

**日期 / Date:** 2026-03-19
**状态 / Status:** Approved
**阶段 / Phase:** Phase 2 Task 1
**预估工期 / Estimated effort:** 3 weeks

## 概述 / Overview

Reflect Engine 是 IClude Phase 2 的核心功能——一个受限的专用反思 Agent，对已有记忆做多步推理，自动生成心智模型（mental_model）记忆。借鉴 Hindsight 的 Reflect 能力，与原规划"多轮思考型检索"合并设计。

Reflect Engine is the core feature of IClude Phase 2 — a constrained, purpose-built reflection agent that performs multi-step reasoning over existing memories and automatically generates mental model memories. Inspired by Hindsight's Reflect capability, merged with the originally planned "iterative retriever" design.

## 设计决策 / Design Decisions

| 决策项 / Decision | 选择 / Choice | 原因 / Rationale |
|-------------------|---------------|-------------------|
| LLM 调用层 / LLM call layer | 新建独立 `internal/llm/` 包 / New independent `internal/llm/` package | Reflect 和 Extractor 都需要 Chat 能力，独立包更清晰 / Both Reflect and Extractor need Chat capability, separate package is cleaner |
| 架构定位 / Architecture | 独立 `ReflectEngine` struct / Independent `ReflectEngine` struct | 逻辑较重，避免 Manager 膨胀超 1000 行 / Heavy logic, avoids Manager exceeding 1000-line limit |
| 写回策略 / Write-back strategy | 可选 `auto_save`（默认 true）/ Optional `auto_save` (default true) | 大多数场景需要写回，调试/预览可关闭 / Most scenarios need write-back, debug/preview can disable |
| Prompt 策略 / Prompt strategy | 硬编码模板 / Hardcoded templates | Phase 2 先跑通，YAGNI / Get it working first in Phase 2, YAGNI |
| LLM Provider | 仅 OpenAI 兼容 API / OpenAI-compatible API only | 覆盖 95% 场景，config 中 base_url 可切换 / Covers 95% of use cases, switchable via base_url in config |
| LLM 输出格式 / LLM output format | JSON Schema 约束 + response_format / JSON Schema constraint + response_format | 结构化解析更可靠 / Structured parsing is more reliable |
| 解析高可用 / Parsing HA | 三级 fallback + 循环保护 / 3-level fallback + loop protection | 确保 LLM 输出异常不会卡死或崩溃 / Ensures LLM output anomalies won't hang or crash |

## 1. `internal/llm/` — LLM Chat 抽象层 / LLM Chat Abstraction Layer

### 1.1 接口定义 / Interface Definition（`provider.go`）

```go
package llm

// ChatMessage LLM对话消息 / LLM chat message
type ChatMessage struct {
    Role    string // system / user / assistant
    Content string
}

// ResponseFormat LLM响应格式约束 / LLM response format constraint
type ResponseFormat struct {
    Type string // "json_object"
}

// ChatRequest LLM对话请求 / LLM chat request
type ChatRequest struct {
    Messages       []ChatMessage
    ResponseFormat *ResponseFormat // 可选，nil则不约束 / optional, nil means no constraint
    Temperature    *float64        // 可选，nil则使用模型默认值 / optional, nil uses model default
    MaxTokens      int             // 可选，0则不限制单次输出长度 / optional, 0 means no per-call limit
}

// ChatResponse LLM对话响应 / LLM chat response
type ChatResponse struct {
    Content          string
    PromptTokens     int
    CompletionTokens int
    TotalTokens      int
}

// Provider LLM推理接口 / LLM inference provider interface
type Provider interface {
    Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
}
```

### 1.2 OpenAI 兼容实现 / OpenAI-Compatible Implementation（`openai.go`）

```go
// OpenAIProvider OpenAI兼容LLM实现 / OpenAI-compatible LLM provider
// 覆盖 OpenAI/DeepSeek/Moonshot/vLLM/Ollama 等所有兼容接口
// Covers OpenAI/DeepSeek/Moonshot/vLLM/Ollama and all compatible APIs
type OpenAIProvider struct {
    baseURL    string        // 默认 https://api.openai.com/v1 / default https://api.openai.com/v1
    apiKey     string
    model      string
    httpClient *http.Client  // 超时 60s / timeout 60s
}

func NewOpenAIProvider(baseURL, apiKey, model string) *OpenAIProvider
func (p *OpenAIProvider) Chat(ctx context.Context, req *ChatRequest) (*ChatResponse, error)
// 调用 POST {baseURL}/chat/completions
// Calls POST {baseURL}/chat/completions
// 支持 response_format: { type: "json_object" }
// Supports response_format: { type: "json_object" }
```

## 2. `ReflectEngine` — 核心反思引擎 / Core Reflect Engine

### 2.1 结构体 / Struct（`internal/memory/reflect.go`）

```go
type ReflectEngine struct {
    retriever *search.Retriever
    manager   *Manager
    llm       llm.Provider
    cfg       ReflectConfig // 从 config 注入 / injected from config
}

func NewReflectEngine(retriever *search.Retriever, manager *Manager, llm llm.Provider, cfg ReflectConfig) *ReflectEngine
func (e *ReflectEngine) Reflect(ctx context.Context, req *model.ReflectRequest) (*model.ReflectResponse, error)
```

### 2.2 请求/响应模型 / Request/Response Models（`internal/model/request.go` 新增 / additions）

```go
// ReflectRequest 反思请求 / Reflect request
type ReflectRequest struct {
    Question  string // 用户问题 / user question
    Scope     string // 命名空间过滤 / namespace filter
    TeamID    string
    MaxRounds   int    // 最大推理轮数，默认 3 / max reasoning rounds, default 3
    TokenBudget int    // 总 token 预算（累计所有轮次），默认 4096 / total token budget (cumulative across rounds), default 4096
    AutoSave  *bool  // 是否自动写回 mental_model，默认 true / auto write-back mental_model, default true
}

// ReflectResponse 反思响应 / Reflect response
type ReflectResponse struct {
    Result      string         // 最终推理结论 / final reasoning conclusion
    NewMemoryID string         // 写回的记忆ID（auto_save=false时为空）/ write-back memory ID (empty when auto_save=false)
    Trace       []ReflectRound // 每轮溯源 / per-round trace
    Sources     []string       // 参与推理的记忆ID列表（去重）/ deduplicated source memory IDs
    Metadata    ReflectMeta
}

// ReflectRound 反思轮次记录 / Reflect round trace
type ReflectRound struct {
    Round        int      // 轮次 / round number
    Query        string   // 本轮检索 query / this round's retrieval query
    RetrievedIDs []string // 召回的记忆ID / retrieved memory IDs
    Reasoning    string   // LLM 本轮推理内容 / LLM reasoning for this round
    NeedMore     bool     // LLM 是否判断需要更多信息 / whether LLM needs more info
    ParseMethod  string   // "json" | "extract" | "retry" | "fallback"
    TokensUsed   int      // 本轮消耗 token / tokens consumed this round
}

// ReflectMeta 反思元数据 / Reflect metadata
type ReflectMeta struct {
    RoundsUsed     int  // 实际使用轮数 / actual rounds used
    TotalTokens    int  // 总消耗 token / total tokens consumed
    ParseFallbacks int  // 降级次数，>0 说明 LLM 输出质量有问题 / fallback count, >0 indicates LLM output quality issues
    Timeout        bool // 是否因超时结束 / whether terminated due to timeout
    QueryDeduped   bool // 是否因重复 query 提前结束 / whether terminated due to duplicate query
}
```

### 2.3 核心循环流程 / Core Loop Flow

```
输入 / Input: ReflectRequest{Question, Scope, MaxRounds, TokenBudget, AutoSave}

初始化 / Initialize:
  query = req.Question
  seenQueries = {query: true}
  totalTokens = 0
  trace = []
  allSourceIDs = set{}
  totalCtx = context.WithTimeout(ctx, maxRounds × 30s)

循环 / Loop: round = 1..maxRounds:
  roundCtx = context.WithTimeout(totalCtx, 30s)

  1) 检索 / Retrieve:
     Retriever.Retrieve(query, scope, limit=10) → memories[]
     收集 ID 到 allSourceIDs / collect IDs into allSourceIDs

  2) 构造 LLM 请求 / Build LLM request:
     system prompt: 硬编码反思引擎指令 / hardcoded reflect engine instructions
     user prompt: 问题 + 格式化的记忆列表 / question + formatted memory list
     response_format: { type: "json_object" }

  3) LLM 调用 / LLM call:
     llm.Chat(roundCtx, request) → response
     totalTokens += response.TotalTokens
     检查 token 预算 / check token budget

  4) 解析输出（三级 fallback）/ Parse output (3-level fallback):
     L1: json.Unmarshal → validate()
     L2: 正则提取 JSON 片段 → Unmarshal → validate() / regex extract JSON fragment
     L3: 追加提示重试 1 次 → Unmarshal → validate() / append hint and retry once
     L4: 降级为 conclusion / degrade to conclusion

  5) 记录 trace[round] / record trace[round]

  6) 判断 / Decide:
     - action == "conclusion" → 跳出 / break
     - action == "need_more":
       - next_query 在 seenQueries 中 → 标记 deduped，跳出 / mark deduped, break
       - 否则 → query = next_query，继续 / otherwise continue

循环结束后 / After loop:
  result = 最终 conclusion / final conclusion

  如果 auto_save != false / if auto_save != false:
    Manager.Create({
      Content:    result,
      Kind:       "mental_model",
      SourceType: "reflect",
      Scope:      req.Scope,
      TeamID:     req.TeamID,
      Metadata:   {"question": req.Question, "sources": allSourceIDs},
    })
    → newMemoryID

返回 / Return: ReflectResponse
```

### 2.4 System Prompt 摘要 / System Prompt Summary

Reflect Engine 使用硬编码的 system prompt，核心指令如下（实现时可调优措辞）：

The Reflect Engine uses hardcoded system prompts. Core instructions as follows (wording can be tuned during implementation):

```
System Prompt 核心要点 / Core points:
1. 角色定义：你是一个记忆分析引擎，专注于分析已有记忆间的关联和模式
   Role: You are a memory analysis engine focused on analyzing associations and patterns among existing memories
2. 任务：根据提供的记忆回答用户问题，不要编造不在记忆中的信息
   Task: Answer the user's question based on provided memories, do not fabricate information not in memories
3. 输出格式：必须输出严格的 JSON，包含 action/reasoning/conclusion/next_query 四个字段
   Output format: Must output strict JSON with action/reasoning/conclusion/next_query fields
4. 决策规则：信息充分时 action=conclusion，信息不足时 action=need_more 并给出新的检索关键词
   Decision rules: action=conclusion when info sufficient, action=need_more with new search keywords when insufficient
5. 推理要求：reasoning 字段必须解释记忆间的关联、矛盾、时间线，不能简单复述记忆内容
   Reasoning requirements: reasoning field must explain associations, contradictions, timelines between memories
```

> **注意 / Note:** ReflectEngine 会在 LLM 请求中设置 `Temperature: 0.1`（低随机性，追求确定性推理）。
> ReflectEngine sets `Temperature: 0.1` in LLM requests (low randomness for deterministic reasoning).

### 2.5 LLM 输出 JSON 结构 / LLM Output JSON Structure

```go
// reflectLLMOutput Reflect引擎LLM输出结构（内部使用）/ Reflect engine LLM output structure (internal)
type reflectLLMOutput struct {
    Action     string `json:"action"`      // "need_more" | "conclusion"
    NextQuery  string `json:"next_query"`  // action=need_more 时的新检索关键词 / new retrieval keywords when action=need_more
    Conclusion string `json:"conclusion"`  // action=conclusion 时的最终结论 / final conclusion when action=conclusion
    Reasoning  string `json:"reasoning"`   // 本轮推理过程 / reasoning process for this round
}
```

### 2.6 三级 Fallback 解析 / 3-Level Fallback Parsing

```go
// parseReflectOutput 解析LLM输出（三级降级）/ Parse LLM output (3-level fallback)
// 返回: (解析结果, 解析方式, 错误) / Returns: (parsed result, parse method, error)
func parseReflectOutput(raw string) (*reflectLLMOutput, string, error)

// validate 校验输出字段合法性 / Validate output field legality
// action 合法性 + need_more 必须有 next_query + conclusion 必须有内容
// action validity + need_more requires next_query + conclusion requires content
func (o *reflectLLMOutput) validate() error
```

### 2.7 循环保护机制 / Loop Protection

| 保护 / Protection | 说明 / Description |
|-------------------|--------------------|
| maxRounds | 硬上限，默认 3 / Hard limit, default 3 |
| maxTokens | token 预算累计，超出强制结束 / Cumulative token budget, force stop when exceeded |
| roundTimeout | 单轮超时 30s / Per-round timeout 30s |
| totalTimeout | 总超时 = maxRounds × 30s / Total timeout = maxRounds × 30s |
| seenQueries | query 去重，防止 LLM 重复生成相同 query / Query dedup, prevents LLM from generating duplicate queries |

## 3. API 层 / API Layer

### 3.1 Handler（`internal/api/reflect_handler.go`）

```go
// ReflectHandler 反思推理处理器 / Reflect reasoning handler
type ReflectHandler struct {
    engine *memory.ReflectEngine
}

func NewReflectHandler(engine *memory.ReflectEngine) *ReflectHandler

// Reflect 处理反思推理请求 / Handle reflect reasoning request
// POST /v1/reflect
func (h *ReflectHandler) Reflect(c *gin.Context)
```

### 3.2 请求/响应示例 / Request/Response Examples

**请求 / Request:**
```json
{
  "question": "用户擅长哪些技术？",
  "scope": "user/alice",
  "team_id": "team1",
  "max_rounds": 3,
  "token_budget": 4096,
  "auto_save": true
}
```

**响应 / Response:**
```json
{
  "code": 0,
  "data": {
    "result": "综合分析...",
    "new_memory_id": "uuid",
    "trace": [
      {
        "round": 1,
        "query": "用户擅长哪些技术？",
        "retrieved_ids": ["mem1", "mem2"],
        "reasoning": "从记忆中发现...",
        "need_more": true,
        "parse_method": "json",
        "tokens_used": 1200
      },
      {
        "round": 2,
        "query": "用户的项目经历",
        "retrieved_ids": ["mem3", "mem4"],
        "reasoning": "结合第一轮...",
        "need_more": false,
        "parse_method": "json",
        "tokens_used": 1500
      }
    ],
    "sources": ["mem1", "mem2", "mem3", "mem4"],
    "metadata": {
      "rounds_used": 2,
      "total_tokens": 2700,
      "parse_fallbacks": 0,
      "timeout": false,
      "query_deduped": false
    }
  }
}
```

### 3.3 路由注册 / Route Registration（`router.go` 修改 / modification）

```go
// RouterDeps 新增 / addition
ReflectEngine *memory.ReflectEngine // 可为 nil / may be nil

// 条件注册（nil check 门控）/ Conditional registration (nil check gating)
if deps.ReflectEngine != nil {
    reflectHandler := NewReflectHandler(deps.ReflectEngine)
    v1.POST("/reflect", reflectHandler.Reflect)
}
```

## 4. 启动集成 / Startup Integration（`cmd/server/main.go`）

```go
// 1. 初始化 LLM Provider（支持 OpenAI 和 Ollama 两种路径）
// Initialize LLM Provider (supports both OpenAI and Ollama paths)
var llmProvider llm.Provider
switch {
case cfg.LLM.OpenAI.APIKey != "":
    // OpenAI 及其兼容 API（DeepSeek/Moonshot 等）/ OpenAI and compatible APIs
    baseURL := cfg.LLM.OpenAI.BaseURL
    if baseURL == "" {
        baseURL = "https://api.openai.com/v1"
    }
    llmProvider = llm.NewOpenAIProvider(baseURL, cfg.LLM.OpenAI.APIKey, cfg.LLM.OpenAI.Model)
case cfg.LLM.Ollama.BaseURL != "":
    // Ollama 本地部署（无需 API Key，走 OpenAI 兼容端点）
    // Ollama local deployment (no API key needed, uses OpenAI-compatible endpoint)
    ollamaBase := strings.TrimSuffix(cfg.LLM.Ollama.BaseURL, "/") + "/v1"
    model := cfg.LLM.Ollama.Model
    if model == "" {
        model = cfg.LLM.OpenAI.Model // fallback
    }
    llmProvider = llm.NewOpenAIProvider(ollamaBase, "", model)
}

// 2. 初始化 ReflectEngine（需要 llmProvider 存在）/ Initialize ReflectEngine (requires llmProvider)
var reflectEngine *memory.ReflectEngine
if llmProvider != nil {
    reflectEngine = memory.NewReflectEngine(retriever, memManager, llmProvider, cfg.Reflect)
}

// 3. 注入 RouterDeps / Inject into RouterDeps
deps.ReflectEngine = reflectEngine
```

> **Ollama 兼容说明 / Ollama Compatibility Note:** Ollama 从 v0.1.14+ 支持 `/v1/chat/completions` OpenAI 兼容端点，因此统一使用 `OpenAIProvider` 实现，无需单独 adapter。启动时按优先级：有 API Key → 走 OpenAI 路径；无 API Key 但有 Ollama BaseURL → 走 Ollama 路径。

## 5. 配置变更 / Config Changes

### 5.1 `internal/config/config.go` 修改 / Modifications

```go
// OpenAIConfig 新增 BaseURL / Add BaseURL to OpenAIConfig
type OpenAIConfig struct {
    APIKey  string `mapstructure:"api_key"`
    BaseURL string `mapstructure:"base_url"` // 新增 / new
    Model   string `mapstructure:"model"`
}

// ReflectConfig 反思引擎配置（新增）/ Reflect engine config (new)
type ReflectConfig struct {
    MaxRounds    int           `mapstructure:"max_rounds"`    // 默认 3 / default 3
    TokenBudget  int           `mapstructure:"token_budget"`  // 默认 4096 / default 4096
    RoundTimeout time.Duration `mapstructure:"round_timeout"` // 默认 30s / default 30s
    AutoSave     bool          `mapstructure:"auto_save"`     // 默认 true / default true
}

// Config 顶层配置（新增 Reflect 字段）/ Top-level config (add Reflect field)
type Config struct {
    // ... 现有字段 / existing fields ...
    Reflect ReflectConfig `mapstructure:"reflect"` // 新增 / new
}
```

**Viper 默认值注册 / Viper defaults registration**（在 `LoadConfig()` 中添加 / add in `LoadConfig()`）:

```go
viper.SetDefault("reflect.max_rounds", 3)
viper.SetDefault("reflect.token_budget", 4096)
viper.SetDefault("reflect.round_timeout", "30s")
viper.SetDefault("reflect.auto_save", true)
viper.SetDefault("llm.openai.base_url", "")
```

### 5.2 `config.yaml` 新增段 / New config section

```yaml
llm:
  openai:
    base_url: "https://api.openai.com/v1"  # 新增 / new

reflect:                    # 新增段 / new section
  max_rounds: 3
  token_budget: 4096
  round_timeout: 30s
  auto_save: true
```

### 5.3 Sentinel Errors / 哨兵错误（`internal/model/errors.go` 新增 / additions）

```go
// Reflect 引擎错误 / Reflect engine errors
var (
    // ErrReflectTimeout 反思推理超时 / reflect reasoning timeout
    ErrReflectTimeout = errors.New("reflect: timeout exceeded")
    // ErrReflectTokenBudgetExceeded 反思推理token预算超出 / reflect token budget exceeded
    ErrReflectTokenBudgetExceeded = errors.New("reflect: token budget exceeded")
    // ErrReflectNoMemories 检索无结果 / no memories retrieved
    ErrReflectNoMemories = errors.New("reflect: no relevant memories found")
    // ErrReflectLLMFailed LLM调用失败 / LLM call failed
    ErrReflectLLMFailed = errors.New("reflect: llm call failed")
    // ErrReflectInvalidRequest 反思请求参数无效 / invalid reflect request
    ErrReflectInvalidRequest = errors.New("reflect: invalid request")
)
```

### 5.4 HTTP 状态码映射 / HTTP Status Code Mapping

| Sentinel Error | HTTP Status | 说明 / Description |
|---------------|-------------|---------------------|
| `ErrReflectInvalidRequest` | 400 Bad Request | 缺少 question 等必填字段 / missing required fields like question |
| `ErrReflectNoMemories` | 404 Not Found | 检索无相关记忆 / no relevant memories found |
| `ErrReflectTimeout` | 408 Request Timeout | 总超时或单轮超时 / total or per-round timeout |
| `ErrReflectTokenBudgetExceeded` | 200 OK（正常返回部分结果）/ 200 OK (return partial result) | token 预算耗尽但有结果 / budget exhausted but has result |
| `ErrReflectLLMFailed` | 502 Bad Gateway | 外部 LLM 调用失败 / external LLM call failure |

> **注意 / Note:** `ErrReflectTokenBudgetExceeded` 不视为错误——仍返回已有的推理结果，`metadata.timeout` 或 trace 中会标记。只有完全无法产出结果时才返回错误状态码。
> `ErrReflectTokenBudgetExceeded` is not treated as an error — partial results are still returned, marked in `metadata`. Error status codes are only returned when no result can be produced at all.

## 6. 测试计划 / Test Plan

### 6.1 ReflectEngine 测试 / ReflectEngine Tests（`testing/memory/reflect_test.go`）

| 用例 / Test Case | 说明 / Description |
|-------------------|---------------------|
| TestReflect_SingleRound | 单轮即得出结论 / Single round reaches conclusion |
| TestReflect_MultiRound | 多轮循环后得出结论 / Multi-round loop reaches conclusion |
| TestReflect_MaxRoundsExceeded | 达到最大轮数强制结束 / Force stop at max rounds |
| TestReflect_AutoSaveTrue | 自动写回 mental_model / Auto write-back mental_model |
| TestReflect_AutoSaveFalse | 不写回，new_memory_id 为空 / No write-back, empty new_memory_id |
| TestReflect_QueryDedup | 重复 query 提前结束 / Duplicate query early termination |
| TestReflect_EmptyRetrieval | 检索为空直接结论 / Empty retrieval leads to direct conclusion |
| TestReflect_TokenBudgetExceeded | token 预算超出强制结束 / Force stop when token budget exceeded |

### 6.2 JSON 解析高可用测试 / JSON Parsing HA Tests（`testing/memory/reflect_test.go`）

| 用例 / Test Case | 说明 / Description |
|-------------------|---------------------|
| TestParseReflectOutput_ValidJSON | 正常 JSON 解析 / Normal JSON parsing |
| TestParseReflectOutput_ExtractFromText | 从文本中提取 JSON 片段 / Extract JSON fragment from text |
| TestParseReflectOutput_InvalidAction | 非法 action 校验失败 / Invalid action validation failure |
| TestParseReflectOutput_Fallback | 全部失败降级为 conclusion / All failures degrade to conclusion |
| TestValidateReflectOutput_NeedMoreNoQuery | need_more 但缺 next_query / need_more without next_query |
| TestValidateReflectOutput_EmptyConclusion | conclusion 但内容为空 / conclusion with empty content |

### 6.3 LLM Provider 测试 / LLM Provider Tests（`testing/llm/provider_test.go`）

| 用例 / Test Case | 说明 / Description |
|-------------------|---------------------|
| TestOpenAIProvider_Chat_Success | 正常调用 / Successful call |
| TestOpenAIProvider_Chat_Timeout | 超时处理 / Timeout handling |
| TestOpenAIProvider_Chat_APIError | API 返回错误码 / API error response |
| TestOpenAIProvider_Chat_JSONResponse | response_format=json_object 约束 / response_format constraint |

### 6.4 API 集成测试 / API Integration Tests（`testing/api/reflect_test.go`）

| 用例 / Test Case | 说明 / Description |
|-------------------|---------------------|
| TestReflectAPI_Success | 完整 HTTP 请求链路，mock LLM，验证 200 + JSON 格式 / Full HTTP request chain with mock LLM, verify 200 + JSON format |
| TestReflectAPI_MissingQuestion | 缺少 question 字段，验证 400 / Missing question field, verify 400 |
| TestReflectAPI_LLMFailure | LLM 调用失败，验证 502 / LLM call failure, verify 502 |
| TestReflectAPI_Timeout | 超时场景，验证 408 / Timeout scenario, verify 408 |

### 6.5 ParseMethod 常量 / ParseMethod Constants

```go
// 定义在 internal/memory/reflect.go 中 / Defined in internal/memory/reflect.go
const (
    ParseMethodJSON    = "json"     // L1: 直接 JSON 解析成功 / direct JSON parse success
    ParseMethodExtract = "extract"  // L2: 从文本中提取 JSON 片段 / extracted JSON fragment from text
    ParseMethodRetry   = "retry"    // L3: 追加提示后重试成功 / retry with hint succeeded
    ParseMethodFallback = "fallback" // L4: 全部失败，降级为 conclusion / all failed, degraded to conclusion
)
```

## 8. 文件变更清单（更新）/ File Change List (Updated)

### 新增 7 个文件 / 7 New Files

| 文件 / File | 说明 / Description |
|-------------|---------------------|
| `internal/llm/provider.go` | Provider 接口 + 数据类型定义 / Provider interface + data type definitions |
| `internal/llm/openai.go` | OpenAI 兼容实现 / OpenAI-compatible implementation |
| `internal/memory/reflect.go` | ReflectEngine 核心 / ReflectEngine core |
| `internal/api/reflect_handler.go` | POST /v1/reflect Handler |
| `testing/llm/provider_test.go` | LLM Provider 测试 / LLM Provider tests |
| `testing/memory/reflect_test.go` | ReflectEngine + 解析高可用测试 / ReflectEngine + parsing HA tests |
| `testing/api/reflect_test.go` | API 集成测试 / API integration tests |

### 修改 5 个文件 / 5 Modified Files

| 文件 / File | 变更 / Changes |
|-------------|----------------|
| `internal/model/request.go` | 新增 ReflectRequest/Response/Round/Meta / Add ReflectRequest/Response/Round/Meta |
| `internal/model/errors.go` | 新增 Reflect sentinel errors / Add Reflect sentinel errors |
| `internal/config/config.go` | OpenAIConfig.BaseURL + ReflectConfig + Config.Reflect + Viper defaults / Add fields and defaults |
| `internal/api/router.go` | RouterDeps.ReflectEngine + 条件注册 / RouterDeps.ReflectEngine + conditional registration |
| `cmd/server/main.go` | 初始化 LLM Provider（OpenAI/Ollama 双路径）→ ReflectEngine → 注入 / Initialize LLM Provider (dual path) → ReflectEngine → inject |

## 9. 性能目标 / Performance Targets

| 指标 / Metric | 目标 / Target |
|---------------|---------------|
| Reflect 平均完成时间 / Reflect avg completion | ≤ 5s（3 轮 / 3 rounds） |
| 单轮超时 / Per-round timeout | 30s |
| 总超时 / Total timeout | maxRounds × 30s |
| Token 预算 / Token budget | 默认 4096 / default 4096 |
