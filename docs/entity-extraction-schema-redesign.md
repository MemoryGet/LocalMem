# 实体抽取：结构化 Schema 改造方案

> 文档日期：2026-05-07  
> 问题：批量抽取输出为扁平列表，归属靠文本模糊匹配，超时风险高，准确率偏低

---

## 1. 现状问题

### 当前输入格式（拼接文本，无结构）

```
记忆0内容
---
记忆1内容
---
记忆2内容
... × 100 条
```

### 当前输出格式（扁平列表，归属不明）

```json
{
  "entities": [
    {"name": "Rachel", "entity_type": "person", "description": "real estate agent"},
    {"name": "bike",   "entity_type": "object",  "description": "vehicle"}
  ],
  "relations": [
    {"source": "Rachel", "target": "house", "relation_type": "showed"}
  ]
}
```

**问题**：Rachel 属于第几条记忆？靠 `matchEntitiesToMemories` 扫描每条记忆内容反向查找 —— 多条记忆都提到 "Rachel" 时归属出错。

---

## 2. 改造目标

1. **输入有 index 标记**：LLM 知道每条内容的编号
2. **输出按 index 分组**：实体直接对齐到来源记忆，消除模糊归属
3. **使用 OpenAI JSON Schema 强制结构**：替换 `json_object` 为 `json_schema`，杜绝格式幻觉
4. **批次大小降到 30-50 条**：减少输出 token，避免超时

---

## 3. 新输入格式

### System Prompt

```
You are a knowledge extraction engine. For each numbered memory item, extract entities and relations.

Entity types: person, location, organization, event, object, concept, time, other
Relation types: knows, visited, owns, works_at, participated_in, located_in, created, used, related_to

Rules:
- Process each memory item independently by its index
- Only extract entities clearly stated in that item's text
- Do not infer entities across different items
- Each entity: name (canonical form), entity_type, brief description
- Each relation: source entity name, target entity name, relation_type
- Return results indexed to match the input
```

### User Message（结构化 JSON 输入）

```json
{
  "memories": [
    {
      "index": 0,
      "content": "I visited a 3-bedroom house on Maple St with Rachel today. She's my real estate agent."
    },
    {
      "index": 1,
      "content": "Took my bike to the repair shop on Oak Ave. Front wheel needed replacement."
    },
    {
      "index": 2,
      "content": "Had a team meeting with Sarah and John to discuss the Q3 roadmap."
    }
  ]
}
```

---

## 4. 新输出 JSON Schema

### OpenAI Strict Schema 定义

```json
{
  "type": "json_schema",
  "json_schema": {
    "name": "batch_extraction_result",
    "strict": true,
    "schema": {
      "type": "object",
      "properties": {
        "results": {
          "type": "array",
          "items": {
            "type": "object",
            "properties": {
              "index": {
                "type": "integer",
                "description": "Must match the input memory index"
              },
              "entities": {
                "type": "array",
                "items": {
                  "type": "object",
                  "properties": {
                    "name":        { "type": "string" },
                    "entity_type": { "type": "string",
                                     "enum": ["person","location","organization",
                                              "event","object","concept","time","other"] },
                    "description": { "type": "string" }
                  },
                  "required": ["name", "entity_type", "description"],
                  "additionalProperties": false
                }
              },
              "relations": {
                "type": "array",
                "items": {
                  "type": "object",
                  "properties": {
                    "source":        { "type": "string" },
                    "target":        { "type": "string" },
                    "relation_type": { "type": "string",
                                       "enum": ["knows","visited","owns","works_at",
                                                "participated_in","located_in",
                                                "created","used","related_to"] }
                  },
                  "required": ["source", "target", "relation_type"],
                  "additionalProperties": false
                }
              }
            },
            "required": ["index", "entities", "relations"],
            "additionalProperties": false
          }
        }
      },
      "required": ["results"],
      "additionalProperties": false
    }
  }
}
```

### 对应输出示例

```json
{
  "results": [
    {
      "index": 0,
      "entities": [
        {"name": "Rachel",              "entity_type": "person",   "description": "real estate agent"},
        {"name": "Maple St house",      "entity_type": "location", "description": "3-bedroom house on Maple St"}
      ],
      "relations": [
        {"source": "Rachel", "target": "Maple St house", "relation_type": "visited"}
      ]
    },
    {
      "index": 1,
      "entities": [
        {"name": "bike",              "entity_type": "object",   "description": "personal bicycle"},
        {"name": "Oak Ave repair shop","entity_type": "location", "description": "bike repair shop on Oak Ave"}
      ],
      "relations": [
        {"source": "bike", "target": "Oak Ave repair shop", "relation_type": "visited"}
      ]
    },
    {
      "index": 2,
      "entities": [
        {"name": "Sarah", "entity_type": "person", "description": "team member"},
        {"name": "John",  "entity_type": "person", "description": "team member"}
      ],
      "relations": [
        {"source": "Sarah", "target": "John", "relation_type": "knows"}
      ]
    }
  ]
}
```

---

## 5. 代码改动范围

### 5.1 新增 Go 结构体

```go
// internal/memory/extractor.go

// batchExtractInput 批量抽取结构化输入 / Structured input for batch extraction
type batchExtractInput struct {
    Memories []batchMemoryItem `json:"memories"`
}

type batchMemoryItem struct {
    Index   int    `json:"index"`
    Content string `json:"content"`
}

// batchExtractOutput 批量抽取结构化输出（按 index 分组）/ Structured output indexed by memory
type batchExtractOutput struct {
    Results []batchExtractResult `json:"results"`
}

type batchExtractResult struct {
    Index     int                 `json:"index"`
    Entities  []extractedEntity   `json:"entities"`
    Relations []extractedRelation `json:"relations"`
}
```

### 5.2 修改 callBatchLLM

```go
func (e *Extractor) callBatchLLM(ctx context.Context, items []model.BatchExtractItem) (*batchExtractOutput, int, error) {
    // 构建结构化输入 / Build structured input
    input := batchExtractInput{
        Memories: make([]batchMemoryItem, len(items)),
    }
    for i, item := range items {
        input.Memories[i] = batchMemoryItem{Index: i, Content: item.Content}
    }
    inputJSON, _ := json.Marshal(input)

    messages := []llm.ChatMessage{
        {Role: "system", Content: e.buildBatchExtractPrompt()},
        {Role: "user",   Content: string(inputJSON)},
    }

    req := &llm.ChatRequest{
        Messages:       messages,
        ResponseFormat: batchExtractionSchema,  // json_schema 替代 json_object
        Temperature:    &temp,
    }

    resp, err := e.llm.Chat(ctx, req)
    // 直接解析为 batchExtractOutput，无需 matchEntitiesToMemories
    var output batchExtractOutput
    json.Unmarshal([]byte(resp.Content), &output)
    return &output, resp.TotalTokens, nil
}
```

### 5.3 `ResponseFormat` 扩展

当前 `llm.ResponseFormat` 只支持 `type: "json_object"`，需要扩展支持 `json_schema`：

```go
// internal/llm/types.go (或 openai.go)
type ResponseFormat struct {
    Type       string      `json:"type"`                  // "json_object" | "json_schema"
    JSONSchema *JSONSchema `json:"json_schema,omitempty"` // 仅 json_schema 时使用
}

type JSONSchema struct {
    Name   string          `json:"name"`
    Strict bool            `json:"strict"`
    Schema json.RawMessage `json:"schema"`
}
```

### 5.4 批次大小调整

```go
// internal/memory/extractor.go
// defaultBatchTokenThreshold 从 32000 降到合理值
// 目标：每批 30-50 条，约 3000-5000 input tokens + 输出可控
const defaultBatchTokenThreshold = 8000  // 32000 → 8000
```

---

## 6. 改动收益对比

| 维度 | 当前方案 | 改造后 |
|------|----------|--------|
| 归属准确性 | 模糊文本匹配，多记忆含同一实体时出错 | index 精准对齐，零歧义 |
| 输出格式稳定性 | `json_object` 可返回任意结构 | `json_schema strict` 强制格式 |
| 超时风险 | 100条/批，输出 token 多，易超时 | 30-50条/批，输出可控 |
| 三级 fallback 解析 | 需要正则+重试补救 | 直接反序列化，不需要 fallback |
| `matchEntitiesToMemories` | 必须，逻辑复杂 | 删除，index 直接映射 |
| 批次数量（2950条）| 29 批 | ~75 批（每批40条） |
| 预期实体抽取覆盖率 | ~63%（1872/2950） | ~80%+ |

---

## 7. 注意事项

1. **`json_schema` strict 模式要求**：所有字段加 `"additionalProperties": false`，enum 值必须完整列出
2. **模型兼容性**：`json_schema` strict 需要 gpt-4o / gpt-5.x 系列，旧版 gpt-4 不支持
3. **向后兼容**：单条 `Extract()` 不变，只改 `callBatchLLM` 和 `ExtractBatch`
4. **relation_type 枚举**：应从 `config.yaml` 的 `extract.relation_types` 动态注入 schema，不要硬编码
