# OpenClaw 借鉴：记忆系统增强设计文档

## 概述

借鉴 OpenClaw 的记忆认知架构，对 IClude 进行 8 项增强，分 4 个优先级阶段交付。核心目标：让 IClude 从"被动存取"进化为"主动认知"——写入时能去重门控，检索时能多样化排序，后台能自动归纳和巡检。

## 动机

| IClude 现状 | OpenClaw 已有 | 差距 |
|-------------|--------------|------|
| 来什么存什么，无写入门控 | Gate aggressively — 不是什么都存 | 缺记忆门控 |
| FTS5 依赖外部 Jieba HTTP 微服务 | N/A（Python 生态原生分词） | 外部依赖 + 分词不一致风险 |
| `strength × exp(-decay × h)`，无访问频率因子 | `base × e^(-0.03×days) × log2(access+1) × type_weight` | 缺访问强化 |
| reinforce 手动，tier 不自动升级 | Crystallization: 30 天后自动升为 permanent trait | 缺自动晶化 |
| RRF 结果可能包含近似重复 | MMR 多样性重排 | 缺 MMR |
| 无后台定时任务 | HEARTBEAT 每 30 分钟心跳 | 缺调度器 |
| 记忆只增不减，靠衰减自然淡化 | Compactor 自动归纳摘要 | 缺记忆归纳 |
| Reflect 需手动调用 | Agent 空闲时自主探索知识库 | 缺自主学习 |

## 架构总览

```
写入路径 (增强):
  Request → 哈希去重(P0) → [余弦去重(P1)] → 评分算tier → gse分词 → SQLite + Qdrant

检索路径 (增强):
  Query → Preprocessor → FTS5(gse) + Qdrant + Graph
       → RRF 融合 → RRF归一化 → MMR重排(P1)
       → 强度加权(含访问频率) → Token裁剪 → 返回

后台路径 (新增):
  Scheduler(P2) → CleanupExpired | Consolidation(P2) | Heartbeat(P3)
```

### 依赖关系

```
P0: gse分词 ←(独立)
P0: 访问频率加权 ←(独立，但自增机制依赖P1)
P0: 哈希去重 ←(独立)
P1: 余弦去重 ← P0(哈希去重先过滤)
P1: 自动晶化 ←(独立)
P1: MMR重排 ← 需 VectorStore.GetVectors 新方法
P1: AccessCount自增 ← P2(调度器) 做异步批量更新
P2: 调度器 ←(独立，基础设施)
P2: 记忆归纳 ← P2(调度器)
P3: HEARTBEAT ← P2(调度器) + search.Retriever + memory.Manager
```

---

## P0：gse 原生分词替代 Jieba HTTP

### 问题

1. Jieba HTTP 是外部 Python 微服务，增加部署和运维复杂度
2. 网络故障时 fallback 到 SimpleTokenizer（CJK 逐字切分），FTS5 写入和查询分词不一致
3. SimpleTokenizer 逐字切分导致 BM25 评分区分度极低

### 方案

引入 [gse](https://github.com/go-ego/gse)（Go 原生分词库，无 CGo，自带词典）。

#### 新文件：`pkg/tokenizer/gse.go`

```go
type GseTokenizer struct {
    seg       gse.Segmenter
    stopwords map[string]bool
}

func NewGseTokenizer(dictPath string, stopwordFiles []string) (*GseTokenizer, error) {
    var seg gse.Segmenter
    var err error
    if dictPath != "" {
        err = seg.LoadDict(dictPath)
    } else {
        err = seg.LoadDict() // 内置词典
    }
    if err != nil {
        return nil, fmt.Errorf("failed to load gse dictionary: %w", err)
    }
    sw := loadStopwords(stopwordFiles)
    return &GseTokenizer{seg: seg, stopwords: sw}, nil
}

func (t *GseTokenizer) Tokenize(ctx context.Context, text string) (string, error) {
    if text == "" {
        return "", nil
    }
    segments := t.seg.Cut(text, true) // 精确模式
    var filtered []string
    for _, s := range segments {
        s = strings.TrimSpace(s)
        if s != "" && !t.stopwords[s] {
            filtered = append(filtered, s)
        }
    }
    return JoinTokens(filtered), nil
}

func (t *GseTokenizer) Name() string { return "gse" }
```

#### 配置

```yaml
storage:
  sqlite:
    tokenizer:
      provider: gse          # gse | jieba | simple | noop
      dict_path: ""          # 空=内置词典
      stopword_files:
        - config/stopwords_zh.txt
        - config/stopwords_en.txt
```

#### FTS5 迁移 (V3→V4)

切换分词器后，FTS5 索引内 token 与新查询 token 不匹配。需要全量重建。

```go
// 注意：Migrate 签名需改为 Migrate(db *sql.DB, tok tokenizer.Tokenizer) error
// 所有调用方（InitStores / NewSQLiteMemoryStore）需同步更新
func migrateV3ToV4(tx *sql.Tx, tok tokenizer.Tokenizer) error {
    ctx := context.Background()

    // 1. 创建 meta 表，记录当前 tokenizer 名称
    if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT)`); err != nil {
        return fmt.Errorf("failed to create meta table: %w", err)
    }
    if _, err := tx.Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES('tokenizer', ?)`, tok.Name()); err != nil {
        return fmt.Errorf("failed to record tokenizer: %w", err)
    }

    // 2. 删除旧 FTS5
    if _, err := tx.Exec(`DROP TABLE IF EXISTS memories_fts`); err != nil {
        return fmt.Errorf("failed to drop old FTS5 table: %w", err)
    }

    // 3. 重建 FTS5
    if _, err := tx.Exec(`CREATE VIRTUAL TABLE memories_fts USING fts5(
        content, abstract, summary,
        content=memories, content_rowid=rowid
    )`); err != nil {
        return fmt.Errorf("failed to create new FTS5 table: %w", err)
    }

    // 4. 全量重新分词插入
    rows, err := tx.Query(`SELECT rowid, content, COALESCE(abstract,''), COALESCE(summary,'') FROM memories WHERE deleted_at IS NULL`)
    if err != nil {
        return fmt.Errorf("failed to query memories for FTS5 rebuild: %w", err)
    }
    defer rows.Close()

    for rows.Next() {
        var rowid int64
        var content, abstract, summary string
        if err := rows.Scan(&rowid, &content, &abstract, &summary); err != nil {
            return fmt.Errorf("failed to scan memory row: %w", err)
        }
        tc, _ := tok.Tokenize(ctx, content)   // 分词失败回退原文由 Tokenize 内部处理
        ta, _ := tok.Tokenize(ctx, abstract)
        ts, _ := tok.Tokenize(ctx, summary)
        if _, err := tx.Exec(`INSERT INTO memories_fts(rowid, content, abstract, summary) VALUES(?,?,?,?)`,
            rowid, tc, ta, ts); err != nil {
            return fmt.Errorf("failed to insert FTS5 row (rowid=%d): %w", rowid, err)
        }
    }
    return rows.Err()
}
```

#### 启动变更检测

在 `InitStores` 中，读取 `meta` 表的 tokenizer 值，与当前配置比较。不一致时自动触发 V4 迁移重建。

#### 影响范围

| 文件 | 变更 |
|------|------|
| `pkg/tokenizer/gse.go` | 新增 |
| `internal/store/factory.go` | 新增 gse provider 分支 |
| `internal/store/sqlite_migration.go` | `Migrate` 签名改为 `Migrate(db *sql.DB, tok tokenizer.Tokenizer) error`；新增 V4 迁移 + tokenizer 变更检测 |
| `internal/config/config.go` | `TokenizerConfig` 新增 `DictPath string` + `StopwordFiles []string` 字段 |
| `go.mod` | 新增 `github.com/go-ego/gse` 依赖 |
| `config/config.yaml` | 新增 dict_path、stopword_files 配置 |

#### 注意事项

- gse `LoadDict()` 耗时 1-3 秒，仅启动时调用一次
- gse `Segmenter` 初始化后并发安全（内部 trie 只读）
- 内存占用约 50-70MB（内置词典）
- Jieba HTTP tokenizer 保留为可选 provider

---

## P0：访问频率加权

### 问题

当前衰减公式 `strength × exp(-decayRate × hours)` 没有访问频率因子。被频繁检索命中的重要记忆和从未被访问的记忆衰减速度相同。

### 方案

#### 修正公式

```
effectiveStrength = strength × exp(-decayRate × hours) × (1 + α × log2(accessCount + 1))
```

- `α = 0.15`（阻尼系数，可配置）
- accessCount=0 时乘数=1.0（不惩罚新记忆）
- accessCount=1000 时乘数≈2.5（有上界，不会压倒衰减）

| accessCount | 乘数 (α=0.15) |
|-------------|---------------|
| 0           | 1.00          |
| 1           | 1.15          |
| 5           | 1.39          |
| 20          | 1.66          |
| 100         | 2.00          |
| 1000        | 2.50          |

#### 代码变更：`internal/memory/lifecycle.go`

```go
func CalculateEffectiveStrength(strength, decayRate float64, lastAccessedAt *time.Time, retentionTier string, accessCount int, accessAlpha float64) float64 {
    if retentionTier == model.TierPermanent {
        return strength
    }
    if lastAccessedAt == nil {
        return strength
    }
    hours := time.Since(*lastAccessedAt).Hours()
    if hours < 0 {
        hours = 0
    }
    decay := strength * math.Exp(-decayRate*hours)
    accessBoost := 1.0 + accessAlpha*math.Log2(float64(accessCount)+1.0)
    return decay * accessBoost
}
```

#### 配置

```yaml
retrieval:
  access_alpha: 0.15  # 访问频率阻尼系数
```

#### AccessCount 自增机制（P1 交付）

当前 `access_count` 字段存在但从未被自动递增。P0 阶段先改公式（向后兼容：accessCount=0 时乘数=1.0），P1 阶段实现异步批量更新：

1. 检索命中时，将 memory ID 写入内存 buffer（`chan string`，带容量限制）
2. P2 调度器定时（每 5 分钟）批量 `UPDATE memories SET access_count = access_count + ? WHERE id = ?`
3. 这避免了每次检索都写 DB 的性能问题

#### accessAlpha 传递路径

`ApplyStrengthWeighting` 当前是无状态函数，无法访问 config。需新增 `accessAlpha` 参数：

```go
// 修改前: func ApplyStrengthWeighting(results []*model.SearchResult) []*model.SearchResult
// 修改后: func ApplyStrengthWeighting(results []*model.SearchResult, accessAlpha float64) []*model.SearchResult
```

调用链：`Retriever.Retrieve()` (持有 config) → `ApplyStrengthWeighting(results, cfg.AccessAlpha)` → `CalculateEffectiveStrength(..., mem.AccessCount, accessAlpha)`

#### 影响范围

| 文件 | 变更 |
|------|------|
| `internal/memory/lifecycle.go` | 修改 `CalculateEffectiveStrength` 签名（+accessCount, +accessAlpha） |
| `internal/memory/lifecycle.go` | 修改 `ApplyStrengthWeighting` 签名（+accessAlpha） |
| `internal/search/retriever.go` | 调用 `ApplyStrengthWeighting` 时传入 `cfg.AccessAlpha` |
| `internal/config/config.go` | `RetrievalConfig` 新增 `AccessAlpha float64` 字段 |

---

## P0：哈希去重

### 问题

当前写入路径没有任何去重机制。相同内容重复写入会产生冗余记忆，浪费存储并污染检索结果。

### 方案

在 `Manager.Create()` 入口增加 SHA-256 内容哈希去重。

#### 流程

```
Create请求 → normalize(content) → SHA-256 → 查询 content_hash 索引
  ├─ 已存在 → reinforce 现有记忆 + 合并 metadata → 返回现有 ID
  └─ 不存在 → 正常写入 + 存储 hash
```

#### 数据库变更 (V4 迁移内)

```sql
ALTER TABLE memories ADD COLUMN content_hash TEXT DEFAULT '';
CREATE UNIQUE INDEX idx_memories_content_hash ON memories(content_hash) WHERE content_hash != '' AND deleted_at IS NULL;
-- 注意：partial unique index 需要 SQLite 3.8.0+（Go mattn/go-sqlite3 已满足）
-- 边界情况：soft-delete 记忆被 Restore() 时，若同 hash 的新记忆已存在，
-- 会触发 UNIQUE 冲突。Restore 逻辑需先检查 hash 冲突，冲突时合并而非报错。
```

#### 归一化规则

```go
// 包级别预编译，避免每次调用都创建 regexp
var whitespaceRe = regexp.MustCompile(`\s+`)

func normalizeForHash(content string) string {
    s := strings.TrimSpace(content)
    s = strings.ToLower(s)
    s = whitespaceRe.ReplaceAllString(s, " ")
    return s
}

func contentHash(content string) string {
    h := sha256.Sum256([]byte(normalizeForHash(content)))
    return hex.EncodeToString(h[:])
}
```

#### CreateBatch 覆盖

`IngestConversation` 调用 `CreateBatch` 绕过了单条 `Create`。哈希去重逻辑需提取为共享函数 `dedup()`，在两个路径中都调用。

#### 影响范围

| 文件 | 变更 |
|------|------|
| `internal/memory/manager.go` | `Create` 和 `CreateBatch` 入口加 dedup |
| `internal/memory/dedup.go` | 新增：归一化 + 哈希计算 + 去重逻辑 |
| `internal/store/sqlite.go` | 新增 `GetByContentHash(ctx, hash) (*Memory, error)`；未找到时返回 `(nil, model.ErrMemoryNotFound)` |
| `internal/store/interfaces.go` | `MemoryStore` 接口新增方法 |
| `internal/store/sqlite_migration.go` | V4 迁移新增 content_hash 列 + 索引 |
| `internal/model/memory.go` | Memory 结构体新增 `ContentHash` 字段 |

---

## P1：余弦相似度去重

### 问题

哈希去重只能捕获精确重复。语义相同但表述不同的记忆仍会重复写入。

### 方案

在哈希去重通过后（无精确匹配），用 Qdrant 做语义相似度检查。

#### 双阈值策略

| 相似度范围 | 动作 |
|-----------|------|
| ≥ 0.95 | **跳过** — 近似重复，reinforce 现有记忆 |
| 0.85 - 0.95 | **可选 LLM 判断** — 语义相关但可能有差异 |
| < 0.85 | **直接写入** — 足够不同 |

```
Create请求 → 哈希去重(通过) → Embed → Qdrant.Search(topK=1)
  ├─ sim ≥ 0.95 → reinforce 最相似记忆
  ├─ 0.85 ≤ sim < 0.95 → (可选LLM) → merge 或 create
  └─ sim < 0.85 → 正常写入
```

#### 配置

```yaml
storage:
  dedup:
    enabled: true
    hash_enabled: true            # P0 哈希去重
    vector_enabled: true          # P1 余弦去重
    skip_threshold: 0.95          # 直接跳过
    merge_threshold: 0.85         # 进入合并判断
    use_llm_for_merge: false      # 是否用 LLM 判断 0.85-0.95 区间
```

#### 注意事项

- VectorStore 为 nil 时跳过余弦去重（best-effort）
- 复用写入路径的 embedding：当前 `Manager.Create()` 在 SQLite 写入后才 embed+upsert Qdrant。余弦去重需要 embedding 在 SQLite 写入**之前**。因此需调整 Create 流程为：**embed → 哈希去重 → 余弦去重 → SQLite 写入 → Qdrant upsert**
- CreateBatch 路径：不逐条做余弦去重（太慢），改为批量写入后异步去重
- 阈值依赖 embedding 模型，换模型需重新校准
- 并发写入可能出现 check-then-act 竞态——接受，由后续归纳兜底

#### 影响范围

| 文件 | 变更 |
|------|------|
| `internal/memory/dedup.go` | 扩展：余弦去重逻辑 |
| `internal/memory/manager.go` | `Create` 写入前调用 vectorDedup |
| `internal/config/config.go` | 新增 dedup 配置节 |

---

## P1：自动晶化 (Auto-Crystallization)

### 问题

有价值的记忆需要用户手动设置 `retention_tier=permanent`。频繁被 reinforce 的记忆应该自动升级。

### 方案

在 `Manager.Reinforce()` 中，reinforce 成功后检查是否满足晶化条件，满足则自动升级 tier。

#### 三重条件（防"人气陷阱"）

```go
func ShouldCrystallize(m *model.Memory, cfg CrystallizationConfig) bool {
    return m.ReinforcedCount >= cfg.MinReinforceCount &&  // 默认 5
           m.Strength >= cfg.MinStrength &&                // 默认 0.7
           time.Since(m.CreatedAt) >= cfg.MinAge &&        // 默认 30 天
           m.RetentionTier != model.TierPermanent &&
           m.Kind != "ephemeral" && m.Kind != "conversation"
}
```

#### 升级路径

```
ephemeral → short_term → standard → long_term → permanent
```

每次晶化只升一级，不跳级。升级时同步调整 `decay_rate` 为新 tier 的默认值。

#### 配置

```yaml
crystallization:
  enabled: true
  min_reinforce_count: 5
  min_strength: 0.7
  min_age: "720h"  # 30 天
```

#### 获取 Reinforce 后的记忆状态

当前 `MemoryStore.Reinforce` 接口签名为 `Reinforce(ctx, id) error`，不返回更新后的状态。为避免接口 breaking change，**不修改接口签名**，改为在 `Manager.Reinforce()` 中 Reinforce 后调用 `Get(id)` 获取最新状态，再检查晶化条件。额外一次 Get 查询成本可忽略（reinforce 是低频操作）。

#### 影响范围

| 文件 | 变更 |
|------|------|
| `internal/memory/manager.go` | `Reinforce` 方法：调 store.Reinforce → Get → ShouldCrystallize → 可选 Update tier |
| `internal/memory/lifecycle.go` | 新增 `ShouldCrystallize` + `PromoteTier` |
| `internal/config/config.go` | 新增 crystallization 配置节 |

---

## P1：MMR 多样性重排

### 问题

RRF 融合后的结果可能包含语义近似的记忆（尤其是 FTS5 和 Qdrant 返回同一记忆的不同表述），占满结果集导致信息冗余。

### 方案

在 RRF 融合后、强度加权前，插入 MMR (Maximal Marginal Relevance) 重排步骤。

#### 公式

```
MMR(d) = λ × NormRRF(d) − (1 − λ) × max(cosineSim(d, dⱼ)) for dⱼ ∈ selected
```

- `λ = 0.7`（70% 相关性，30% 多样性）
- **RRF 分数需先归一化到 [0,1]**：`NormRRF(d) = score(d) / maxScore`

#### 实现

贪心迭代选择，O(n×k) 复杂度。典型 n=50 candidates, k=10 results → 500 次余弦计算 ≈ 1ms。

#### 获取 embedding 向量

需要新增 `VectorStore` 接口方法：

```go
// GetVectors 批量获取向量 / Batch retrieve vectors by memory IDs
GetVectors(ctx context.Context, ids []string) (map[string][]float32, error)
```

#### 处理 Graph 通道结果

Graph 通道结果没有 embedding。初期方案：**MMR 只对 FTS+Qdrant 结果做，Graph 结果直接追加到末尾**。Graph 的深度评分已天然有多样性（深层实体分数更低），暂不需要 MMR。

**SQLite-only 模式**：当 VectorStore 为 nil 时，无法获取 embedding 向量，MMR 自动跳过（即使 `mmr.enabled: true`）。此行为需在配置文档中说明。

#### 检索流程变更

```
FTS5 + Qdrant + Graph → RRF 融合 → 归一化 → MMR 重排 → 强度加权 → Token 裁剪
```

#### 配置

```yaml
retrieval:
  mmr:
    enabled: true
    lambda: 0.7
```

#### 影响范围

| 文件 | 变更 |
|------|------|
| `internal/search/mmr.go` | 新增：MMR 算法实现 |
| `internal/search/retriever.go` | `Retrieve` 中 RRF 后插入 MMR 步骤 |
| `internal/store/interfaces.go` | `VectorStore` 新增 `GetVectors` 方法 |
| `internal/store/qdrant.go` | 实现 `GetVectors` |
| `internal/config/config.go` | 新增 mmr 配置节 |

---

## P1：AccessCount 异步自增

### 问题

P0 的访问频率加权依赖 `access_count` 字段，但该字段当前从未被自动递增。不解决则 `accessCount` 始终为 0，公式中访问加成始终为 `1 + 0.15 × log2(1) = 1.0`（无效果，但不会出错）。P0 先改公式保证向后兼容，P1 补上自增后加权才真正生效。

### 方案

检索命中时收集 memory ID 到内存 buffer，由 P2 调度器定时批量更新。

```go
// 内存 buffer（有容量限制的 channel）
type AccessTracker struct {
    ch chan string // buffered channel, cap=10000
    store store.MemoryStore
}

// 检索命中时调用（非阻塞）
func (t *AccessTracker) Track(memoryID string) {
    select {
    case t.ch <- memoryID:
    default: // buffer 满了就丢弃，best-effort
    }
}

// 定时刷新（由调度器调用，每 5 分钟）
func (t *AccessTracker) Flush(ctx context.Context) error {
    counts := make(map[string]int)
    for {
        select {
        case id := <-t.ch:
            counts[id]++
        default:
            goto flush
        }
    }
flush:
    for id, delta := range counts {
        t.store.IncrementAccessCount(ctx, id, delta)
    }
    return nil
}
```

#### 影响范围

| 文件 | 变更 |
|------|------|
| `internal/memory/access_tracker.go` | 新增 |
| `internal/search/retriever.go` | 检索结果返回前调用 `tracker.Track()` |
| `internal/store/interfaces.go` | `MemoryStore` 新增 `IncrementAccessCount` |
| `internal/store/sqlite.go` | 实现批量 access_count 更新 |

---

## P2：后台调度器

### 问题

IClude 当前没有任何后台定时任务。CleanupExpired 需手动 HTTP 触发。P2 归纳和 P3 HEARTBEAT 都依赖定时执行能力。

### 方案

进程内 goroutine + `time.Ticker`，新增 `internal/scheduler/` 包。

#### 核心结构

```go
type Scheduler struct {
    tasks  []Task
    wg     sync.WaitGroup
    logger *zap.Logger
}

type Task struct {
    Name     string
    Interval time.Duration
    Fn       func(ctx context.Context) error
    running  atomic.Bool // 防止重叠执行
}

func (s *Scheduler) Run(ctx context.Context) {
    for i := range s.tasks {
        s.wg.Add(1)
        go s.runTask(ctx, &s.tasks[i])
    }
    s.wg.Wait()
}

func (s *Scheduler) runTask(ctx context.Context, t *Task) {
    defer s.wg.Done()
    ticker := time.NewTicker(t.Interval)
    defer ticker.Stop()
    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            if !t.running.CompareAndSwap(false, true) {
                continue // 上一轮还没跑完，跳过
            }
            if err := t.Fn(ctx); err != nil {
                s.logger.Warn("scheduler task failed", zap.String("task", t.Name), zap.Error(err))
            }
            t.running.Store(false)
        }
    }
}
```

#### 关机顺序

```
1. cancel scheduler context → 所有 ticker 停止
2. scheduler.wg.Wait() (timeout 3s) → 等待 in-flight 任务完成
3. srv.Shutdown(5s) → HTTP 优雅关闭
4. stores.Close() → 关闭 DB 连接
```

#### 初始注册任务

```go
scheduler.Register(Task{Name: "cleanup", Interval: 6*time.Hour, Fn: manager.CleanupExpired})
scheduler.Register(Task{Name: "access_flush", Interval: 5*time.Minute, Fn: tracker.Flush})
// P2 阶段追加:
scheduler.Register(Task{Name: "consolidation", Interval: 24*time.Hour, Fn: consolidator.Run})
// P3 阶段追加:
scheduler.Register(Task{Name: "heartbeat", Interval: 6*time.Hour, Fn: heartbeat.Run})
```

#### 配置

```yaml
scheduler:
  enabled: true
  cleanup_interval: "6h"
  access_flush_interval: "5m"
  consolidation_interval: "24h"
  heartbeat_interval: "6h"
```

#### 影响范围

| 文件 | 变更 |
|------|------|
| `internal/scheduler/scheduler.go` | 新增 |
| `cmd/server/main.go` | 启动时创建 + 注册任务 + 关机顺序调整 |
| `internal/config/config.go` | 新增 scheduler 配置节 |

---

## P2：记忆归纳 (Memory Consolidation)

### 问题

记忆只增不减，随时间积累大量语义重叠的记忆。检索结果中多条记忆表达同一事实的不同侧面，浪费 token budget。

### 方案

定时（每 24h）用层次聚类找到相似记忆簇，LLM 归纳为浓缩版永久记忆。

#### 流程

```
1. 候选选取:
   - retention_tier IN ('standard', 'long_term')
   - created_at < 7 天前
   - consolidated_into IS NULL（未被归纳过）
   - LIMIT 200（控制 LLM 成本）

2. 聚类:
   - 距离度量: 1 - cosine_similarity
   - 方法: 层次聚类 (average linkage)
   - 距离阈值: 0.25（即余弦相似度 ≥ 0.75）
   - 最小簇大小: 2

3. 对每个簇:
   - LLM 归纳: "将这 N 条相关记忆归纳为一条全面的记忆。保留所有独特事实，消除冗余。"
   - 创建新记忆:
     retention_tier: permanent
     kind: consolidated
     strength: max(簇内 strengths)，上限 1.0
     reinforced_count: sum(簇内 counts)
     source_type: consolidation
   - 原始记忆: 设置 consolidated_into = 新记忆 ID，然后 soft-delete
```

#### 数据库变更

```sql
ALTER TABLE memories ADD COLUMN consolidated_into TEXT DEFAULT '';
```

提供"爆炸还原"能力：如果归纳结果有误，可通过 `consolidated_into` 找到原始记忆并 restore。

#### 并发安全

- `atomic.Bool` 防止重叠执行
- 两阶段提交：先创建归纳记忆，再 soft-delete 原始记忆
- 检索可能读到已 soft-delete 的记忆（Qdrant 延迟同步）——现有 `deleted_at IS NULL` 过滤已覆盖 SQLite 侧
- Qdrant 向量清理：归纳过程直接调用 `Manager.SoftDelete`（内部已处理 Qdrant 删除），而非绕过 Manager 直接操作 store

#### 配置

```yaml
consolidation:
  enabled: true
  min_age_days: 7
  similarity_threshold: 0.75
  min_cluster_size: 2
  max_memories_per_run: 200
```

#### 影响范围

| 文件 | 变更 |
|------|------|
| `internal/memory/consolidation.go` | 新增：聚类 + LLM 归纳 + 写入 |
| `internal/store/sqlite_migration.go` | V4 新增 consolidated_into 列 |
| `internal/model/memory.go` | Memory 新增 `ConsolidatedInto` 字段 |

---

## P3：HEARTBEAT 自主学习

### 问题

IClude 是被动系统——只在用户请求时才工作。不能主动发现记忆库中的问题和模式。

### 方案

后台定时（每 6h）执行三项巡检任务，聚焦高价值、低风险的操作。

#### 三项巡检（不做模糊的"模式发现"）

**1. 矛盾检测 (Contradiction Detection)**

```
- 从知识图谱中找到共享实体的记忆对
- 用 embedding 相似度筛选高相似但内容不同的对
- LLM 判断是否矛盾
- 矛盾记忆标记 contradiction_group_id
- 不自动修改——通知用户决策
```

**2. 衰减审计 (Decay Audit)**

SQLite 没有内置 `EXP()` 函数。衰减计算在 Go 代码中完成：

```go
// 1. SQL 查询候选记忆（简单条件过滤）
// SELECT id, strength, decay_rate, last_accessed_at, reinforced_count
// FROM memories
// WHERE reinforced_count = 0
//   AND created_at < datetime('now', '-90 days')
//   AND deleted_at IS NULL
//
// 2. Go 代码中对每条候选调用 CalculateEffectiveStrength()
//    effectiveStrength < 0.1 → soft-delete 或降级到 ephemeral
```

不需要 LLM，纯规则计算。

**3. 孤儿清理 (Orphan Cleanup)**

```
- 找到关联已 soft-delete 记忆的实体/关系
- 清理无效的 memory-entity 关联
- 删除无任何关联的孤立实体
```

#### 包结构

新增 `internal/heartbeat/` 包（独立于 memory，避免循环依赖）：

```go
type HeartbeatEngine struct {
    memStore   store.MemoryStore
    graphStore store.GraphStore
    vecStore   store.VectorStore
    llm        llm.Provider
    cfg        HeartbeatConfig
}

func (h *HeartbeatEngine) Run(ctx context.Context) error {
    h.runDecayAudit(ctx)        // 便宜，始终执行
    h.runOrphanCleanup(ctx)     // 便宜，始终执行
    if h.cfg.ContradictionCheck {
        h.runContradictionCheck(ctx) // 昂贵，有限次数
    }
    return nil
}
```

#### 配置

```yaml
heartbeat:
  enabled: false  # 默认关闭
  interval: "6h"
  contradiction_check:
    enabled: true
    max_comparisons_per_run: 50
  decay_audit:
    min_age_days: 90
    min_strength_threshold: 0.1
```

#### 影响范围

| 文件 | 变更 |
|------|------|
| `internal/heartbeat/engine.go` | 新增 |
| `internal/heartbeat/contradiction.go` | 新增 |
| `internal/heartbeat/decay_audit.go` | 新增 |
| `internal/heartbeat/orphan_cleanup.go` | 新增 |
| `internal/config/config.go` | 新增 heartbeat 配置节 |
| `cmd/server/main.go` | 注册 heartbeat 到调度器 |

---

## 数据库迁移计划

为保持"每阶段独立可交付"，迁移拆分为 V4（P0）和 V5（P2）：

### V4 迁移（P0 交付）

```sql
-- 1. meta 表（存储 tokenizer 版本等元信息）
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT);

-- 2. memories 新增 content_hash 列
ALTER TABLE memories ADD COLUMN content_hash TEXT DEFAULT '';

-- 3. 索引（partial unique，需 SQLite 3.8.0+）
CREATE UNIQUE INDEX idx_memories_content_hash
  ON memories(content_hash) WHERE content_hash != '' AND deleted_at IS NULL;

-- 4. FTS5 重建（用新 tokenizer 全量重新分词，Go 代码循环）
DROP TABLE IF EXISTS memories_fts;
CREATE VIRTUAL TABLE memories_fts USING fts5(
    content, abstract, summary,
    content=memories, content_rowid=rowid
);
-- 全量重新插入（Go 代码循环，用新 tokenizer 分词）

-- 5. 回填 content_hash（Go 代码循环，对所有已有记忆计算 SHA-256）
```

### V5 迁移（P2 交付）

```sql
-- 记忆归纳审计字段
ALTER TABLE memories ADD COLUMN consolidated_into TEXT DEFAULT '';
```

---

## 测试要求

每个功能必须在 `testing/report/` 下创建对应的 `{feature}_test.go`，使用 `testreport.NewCase()` 包装。

| 功能 | 测试文件 | 关键用例 |
|------|---------|---------|
| gse 分词 | `testing/report/tokenizer_test.go` | CJK 分词质量、停用词过滤、与 Jieba 结果对比 |
| 访问频率加权 | `testing/report/lifecycle_test.go` | α 系数效果、边界值（count=0/1/1000） |
| 哈希去重 | `testing/report/dedup_test.go` | 精确重复检测、归一化一致性 |
| 余弦去重 | `testing/report/dedup_test.go` | 双阈值行为、VectorStore nil 降级 |
| 自动晶化 | `testing/report/lifecycle_test.go` | 三重条件验证、单级升级、kind 排除 |
| MMR | `testing/report/search_test.go` | λ 效果、RRF 归一化、Graph 结果处理 |
| 调度器 | `testing/report/scheduler_test.go` | 任务注册、重叠防护、优雅关机 |
| 归纳 | `testing/report/consolidation_test.go` | 聚类正确性、归纳质量、audit trail |
| HEARTBEAT | `testing/report/heartbeat_test.go` | 矛盾检测、衰减审计、孤儿清理 |
| V4 迁移 | `testing/report/migration_test.go` | V3→V4 升级、tokenizer 变更检测、content_hash 回填、FTS5 重建后检索验证 |

---

## 交付计划

| 阶段 | 预计工作量 | 里程碑 |
|------|-----------|--------|
| **P0** | gse 分词 + 访问频率 + 哈希去重 + V4 迁移 | 核心检索质量提升 |
| **P1** | 余弦去重 + 晶化 + MMR + AccessCount 自增 | 智能写入 + 检索多样性 |
| **P2** | 调度器 + 记忆归纳 | 后台自动维护 |
| **P3** | HEARTBEAT | 主动认知巡检 |

每个阶段独立可交付、可测试，后续阶段向前兼容。
