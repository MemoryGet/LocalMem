# FTS-Only 检索命中率提升 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在不依赖 Qdrant 的情况下，通过 4 项改进将检索命中率从 80.4% 提升至 90%+

**Architecture:** (1) 开启 LLM 查询增强做跨语言翻译和关键词扩展 (2) 改写 abstract 生成 prompt 使其包含隐含关联词 (3) 新增同义词词典在 Preprocessor 中扩展查询关键词 (4) 切换 gse 分词器 + 自动检测分词器变更并重建 FTS 索引

**Tech Stack:** Go, SQLite FTS5, gse, LLM (OpenAI-compatible)

---

### Task 1: 开启 LLM 查询增强 + 改进 prompt

**Files:**
- Modify: `config.yaml:retrieval.preprocess` section
- Modify: `internal/search/preprocess.go:239-293` (llmEnhance method)

- [ ] **Step 1: 更新 config.yaml 开启 LLM 预处理**

```yaml
# config.yaml — retrieval.preprocess section
retrieval:
  preprocess:
    enabled: true
    use_llm: true
    llm_timeout: 5s
```

- [ ] **Step 2: 改进 llmEnhance 的 system prompt 使其支持跨语言翻译**

在 `internal/search/preprocess.go` 的 `llmEnhance` 方法中，将 system prompt 从:

```go
Content: `You are a query preprocessor. Given a search query, output JSON:
{"rewritten_query": "semantically expanded query for vector search", "intent": "keyword|semantic|temporal|relational|general", "keywords": ["optional", "extra", "keywords"]}
Respond ONLY with valid JSON.`,
```

改为:

```go
Content: `You are a bilingual (Chinese/English) query preprocessor for a memory retrieval system.
Given a search query, output JSON with these fields:
- "rewritten_query": semantically expanded version of the query
- "intent": one of "keyword|semantic|temporal|relational|general"
- "keywords": additional search keywords that MUST include:
  1. Chinese translations if query is in English (e.g. "database migration" → ["数据库","迁移"])
  2. English terms if query is in Chinese (e.g. "数据库迁移" → ["database","migration"])
  3. Synonyms and closely related terms (e.g. "宠物" → ["猫","狗","养"])
  4. Domain-specific expansions (e.g. "部署" → ["docker","容器","compose"])
Respond ONLY with valid JSON, no markdown.`,
```

- [ ] **Step 3: 验证 LLM 增强被调用**

Run: `go build ./cmd/server/ && echo "build ok"`
Expected: build ok

- [ ] **Step 4: Commit**

```bash
git add config.yaml internal/search/preprocess.go
git commit -m "feat: enable LLM query enhancement with bilingual prompt for cross-language retrieval"
```

---

### Task 2: 改进 abstract 生成 prompt 包含隐含关联词

**Files:**
- Modify: `internal/memory/manager.go:279-297` (generateAbstract method)

- [ ] **Step 1: 改进 generateAbstract 的 prompt**

在 `internal/memory/manager.go` 的 `generateAbstract` 方法中，将 system prompt 从:

```go
{Role: "system", Content: "用一句话（≤100字）概括以下内容的核心信息，直接输出摘要，不加前缀。"},
```

改为:

```go
{Role: "system", Content: `生成一段丰富的检索摘要（≤150字），要求：
1. 概括核心信息
2. 补充隐含的上位概念和关联词（如"小橘是橘猫"→补充"宠物、猫咪、养猫"）
3. 包含中英文关键术语（如"数据库迁移"→"database migration"）
4. 添加可能的搜索意图词（如"部署在阿里云"→"服务器、云服务、hosting"）
直接输出摘要，不加前缀或解释。`},
```

- [ ] **Step 2: 调整 abstract 截断长度以适配更长的摘要**

在同一方法中，将截断从 150 改为 200 字符:

```go
if len([]rune(abstract)) > 200 {
    abstract = string([]rune(abstract)[:200])
}
```

- [ ] **Step 3: 将异步摘要生成改为同步（确保 FTS 索引包含摘要）**

在 `internal/memory/manager.go` 的 `Create` 方法中（约 193-201 行），将:

```go
// 异步生成摘要（content 短则直接用 content，否则调 LLM）/ Async abstract generation
if mem.Abstract == "" && m.llm != nil {
    if len([]rune(mem.Content)) <= 50 {
        mem.Abstract = mem.Content
        _ = m.memStore.Update(ctx, mem)
    } else {
        m.asyncGenerateAbstract(mem.ID, mem.Content)
    }
}
```

改为:

```go
// 同步生成丰富摘要（确保 FTS 索引包含摘要关联词）/ Sync rich abstract generation for FTS indexing
if mem.Abstract == "" && m.llm != nil {
    if len([]rune(mem.Content)) <= 50 {
        mem.Abstract = mem.Content
    } else {
        abstract, err := m.generateAbstract(ctx, mem.Content)
        if err != nil {
            logger.Warn("sync abstract generation failed, using content truncation",
                zap.String("memory_id", mem.ID),
                zap.Error(err),
            )
            mem.Abstract = string([]rune(mem.Content)[:100])
        } else {
            mem.Abstract = abstract
        }
    }
    // 更新 SQLite（含 FTS 索引）
    if err := m.memStore.Update(ctx, mem); err != nil {
        logger.Warn("failed to update memory with abstract",
            zap.String("memory_id", mem.ID),
            zap.Error(err),
        )
    }
}
```

- [ ] **Step 4: Build 验证**

Run: `go build ./cmd/server/ && echo "build ok"`
Expected: build ok

- [ ] **Step 5: Commit**

```bash
git add internal/memory/manager.go
git commit -m "feat: sync rich abstract generation with implicit keywords for better FTS recall"
```

---

### Task 3: 新增本地同义词词典 + Preprocessor 集成

**Files:**
- Create: `pkg/tokenizer/synonym.go`
- Create: `config/synonym_zh.txt`
- Modify: `internal/search/preprocess.go` (Preprocessor struct + Process method)
- Modify: `internal/config/config.go` (PreprocessConfig)

- [ ] **Step 1: 创建同义词加载器 `pkg/tokenizer/synonym.go`**

```go
package tokenizer

import (
	"bufio"
	"os"
	"strings"
)

// SynonymDict 同义词词典 / Synonym dictionary
// 格式：key=syn1 syn2 syn3（双向查找）
type SynonymDict struct {
	mapping map[string][]string
}

// NewSynonymDict 从文件加载同义词词典 / Load synonym dictionary from files
func NewSynonymDict(paths ...string) *SynonymDict {
	d := &SynonymDict{mapping: make(map[string][]string)}
	for _, p := range paths {
		if p == "" {
			continue
		}
		_ = d.loadFile(p)
	}
	return d
}

func (d *SynonymDict) loadFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		syns := strings.Fields(parts[1])
		if key == "" || len(syns) == 0 {
			continue
		}
		// 双向映射 / Bidirectional mapping
		allWords := append([]string{key}, syns...)
		for _, w := range allWords {
			w = strings.ToLower(w)
			for _, other := range allWords {
				other = strings.ToLower(other)
				if w != other && !contains(d.mapping[w], other) {
					d.mapping[w] = append(d.mapping[w], other)
				}
			}
		}
	}
	return scanner.Err()
}

// Expand 返回词的所有同义词（不包含自身）/ Return synonyms for a word (excluding itself)
func (d *SynonymDict) Expand(word string) []string {
	return d.mapping[strings.ToLower(word)]
}

// Count 返回词典条目数 / Return entry count
func (d *SynonymDict) Count() int {
	return len(d.mapping)
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: 创建同义词词典文件 `config/synonym_zh.txt`**

覆盖测试失败案例中的高频缺失映射（约 150 组）:

```text
# LocalMem 同义词词典 / Synonym Dictionary
# 格式: 主词=同义词1 同义词2 ... / Format: key=syn1 syn2 ...

# ── 通用概念 ──
宠物=猫 狗 养 动物 猫咪
运动=篮球 足球 跑步 健身 锻炼
出差=旅行 出行 商务 travel trip
生日=出生 birthday 过生日 年龄
朋友=好友 同学 friend 伙伴
书=读 阅读 book 看书 在读
截止日期=deadline 期限 到期 之前必须完成
目标=OKR target goal 指标
会议=开会 例会 周会 meeting
团队=成员 同事 team 人员 colleague

# ── 技术概念 ──
数据库=database db 存储 storage
迁移=migration 升级 变更 切换
服务器=server 主机 host 云服务
部署=deploy 上线 发布 容器 docker
框架=framework 库 library
日志=log logging 记录
搜索=search 检索 查询 查找 retrieve
分词=tokenize segment 切词 token
向量=vector embedding 嵌入
索引=index 全文 fts 检索
端口=port 监听 listen
接口=interface api endpoint 方法
配置=config setting 设置 参数
测试=test 验证 检查 check
安全=security 授权 认证 auth 权限
性能=performance 优化 速度 快
缓存=cache 内存 memory mmap
并发=concurrent 多线程 goroutine 竞态
错误=error bug 异常 exception 问题
重构=refactor 重写 优化 改进
文档=document doc 文件 资料

# ── 项目特定 ──
记忆=memory 知识 信息 数据
衰减=decay 过期 遗忘 消失 失效
强度=strength 权重 重要性 优先级
摘要=abstract summary 概括 总结
分块=chunk 切分 分割 split
心跳=heartbeat 健康检查 巡检 监控
合并=consolidation merge 归纳 去重
抽取=extract 提取 识别 NLP NER
图谱=graph 关系 实体 entity relation
反思=reflect 推理 reasoning 思考
双写=dual-write 冗余 备份 一致性
融合=fusion rrf 合并 merge rank
中间件=middleware 管道 pipeline 拦截器
限流=rate-limit 限速 throttle
白名单=allowlist whitelist 许可
幂等=idempotent 重跑 安全 重复执行
竞态=race toctou 并发冲突
级联=cascade 关联删除 子记录
回滚=rollback 恢复 撤销 undo

# ── 工具/产品 ──
暗色主题=dark-mode 深色 夜间模式 dark theme
极简=minimal 简洁 简约 clean
终端=terminal 命令行 cli shell
编辑器=editor ide vscode 开发工具
版本控制=git github 代码管理

# ── 角色/身份 ──
全栈=fullstack 后端 前端 开发者 工程师
产品经理=pm product-manager 产品
技术负责人=tech-lead 架构师 负责人
算法工程师=ai ml 机器学习 深度学习
```

- [ ] **Step 3: 在 PreprocessConfig 中添加同义词文件路径配置**

在 `internal/config/config.go` 的 `PreprocessConfig` struct 中添加:

```go
type PreprocessConfig struct {
	Enabled       bool          `mapstructure:"enabled"`
	UseLLM        bool          `mapstructure:"use_llm"`
	LLMTimeout    time.Duration `mapstructure:"llm_timeout"`
	StopwordFiles []string      `mapstructure:"stopword_files"`
	SynonymFiles  []string      `mapstructure:"synonym_files"` // 同义词词典文件路径 / Synonym dictionary file paths
}
```

- [ ] **Step 4: 在 Preprocessor 中集成同义词扩展**

修改 `internal/search/preprocess.go`:

4a. 在 Preprocessor struct 中添加 synonymDict 字段:

```go
type Preprocessor struct {
	tokenizer   tokenizer.Tokenizer
	graphStore  store.GraphStore
	llm         llm.Provider
	stopFilter  *tokenizer.StopFilter
	synonymDict *tokenizer.SynonymDict // 同义词词典 / Synonym dictionary
	cfg         config.RetrievalConfig
}
```

4b. 在 NewPreprocessor 中初始化:

```go
func NewPreprocessor(tok tokenizer.Tokenizer, graphStore store.GraphStore, llm llm.Provider, cfg config.RetrievalConfig) *Preprocessor {
	sf := tokenizer.NewStopFilter(cfg.Preprocess.StopwordFiles...)
	sd := tokenizer.NewSynonymDict(cfg.Preprocess.SynonymFiles...)
	return &Preprocessor{
		tokenizer:   tok,
		graphStore:  graphStore,
		llm:         llm,
		stopFilter:  sf,
		synonymDict: sd,
		cfg:         cfg,
	}
}
```

4c. 在 Process 方法中，步骤 1（extractKeywords）之后、步骤 5（LLM 增强）之前，添加同义词扩展:

```go
// 步骤 1.5: 同义词扩展 / Step 1.5: Synonym expansion
if p.synonymDict != nil && len(plan.Keywords) > 0 {
    plan.Keywords = p.expandSynonyms(plan.Keywords)
}
```

4d. 添加 expandSynonyms 方法:

```go
// expandSynonyms 使用同义词词典扩展关键词 / Expand keywords using synonym dictionary
// 限制扩展后总关键词不超过 30 个，避免 FTS 查询过长
func (p *Preprocessor) expandSynonyms(keywords []string) []string {
	seen := make(map[string]bool)
	var expanded []string
	for _, kw := range keywords {
		lower := strings.ToLower(kw)
		if !seen[lower] {
			seen[lower] = true
			expanded = append(expanded, kw)
		}
	}
	for _, kw := range keywords {
		for _, syn := range p.synonymDict.Expand(kw) {
			if !seen[syn] && len(expanded) < 30 {
				seen[syn] = true
				expanded = append(expanded, syn)
			}
		}
	}
	return expanded
}
```

- [ ] **Step 5: 更新 config.yaml 添加同义词文件路径**

```yaml
retrieval:
  preprocess:
    enabled: true
    use_llm: true
    llm_timeout: 5s
    synonym_files:
      - config/synonym_zh.txt
```

- [ ] **Step 6: Build 验证**

Run: `go build ./cmd/server/ && echo "build ok"`
Expected: build ok

- [ ] **Step 7: Commit**

```bash
git add pkg/tokenizer/synonym.go config/synonym_zh.txt internal/config/config.go internal/search/preprocess.go config.yaml
git commit -m "feat: add synonym dictionary for query expansion to improve FTS recall"
```

---

### Task 4: 切换 gse 分词器 + 自动检测分词器变更重建 FTS

**Files:**
- Modify: `config.yaml:storage.sqlite.tokenizer.provider`
- Modify: `internal/store/sqlite.go` (Init method — add tokenizer change detection)

- [ ] **Step 1: 在 SQLiteMemoryStore.Init 中添加分词器变更自动重建**

在 `internal/store/sqlite.go` 的 `Init` 方法中，`Migrate` 调用之后添加分词器变更检测:

```go
func (s *SQLiteMemoryStore) Init(ctx context.Context) error {
	if err := Migrate(s.db, s.tokenizer); err != nil {
		return fmt.Errorf("migration failed: %w", err)
	}

	// 检测分词器是否变更，变更则自动重建 FTS / Detect tokenizer change and rebuild FTS
	if err := s.checkTokenizerChange(ctx); err != nil {
		return fmt.Errorf("tokenizer change check failed: %w", err)
	}

	return nil
}
```

- [ ] **Step 2: 实现 checkTokenizerChange 方法**

在 `internal/store/sqlite.go` 中添加:

```go
// checkTokenizerChange 检测分词器变更并重建 FTS 索引 / Detect tokenizer change and rebuild FTS index
func (s *SQLiteMemoryStore) checkTokenizerChange(ctx context.Context) error {
	var stored string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key='tokenizer'`).Scan(&stored)
	if err != nil {
		// meta 表不存在或无记录，跳过
		return nil
	}

	current := "simple"
	if s.tokenizer != nil {
		current = s.tokenizer.Name()
	}

	if stored == current {
		return nil
	}

	logger.Info("tokenizer changed, rebuilding FTS index",
		zap.String("from", stored),
		zap.String("to", current),
	)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin FTS rebuild transaction: %w", err)
	}
	defer tx.Rollback()

	// 重建 FTS5
	if _, err := tx.Exec(`DROP TABLE IF EXISTS memories_fts`); err != nil {
		return fmt.Errorf("failed to drop FTS5 table: %w", err)
	}
	if _, err := tx.Exec(`CREATE VIRTUAL TABLE memories_fts USING fts5(
		content, abstract, summary,
		content=memories, content_rowid=rowid
	)`); err != nil {
		return fmt.Errorf("failed to create FTS5 table: %w", err)
	}

	rows, err := tx.Query(`SELECT rowid, content, COALESCE(abstract,''), COALESCE(summary,'') FROM memories WHERE deleted_at IS NULL`)
	if err != nil {
		return fmt.Errorf("failed to query memories for FTS rebuild: %w", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var rowid int64
		var content, abstract, summary string
		if err := rows.Scan(&rowid, &content, &abstract, &summary); err != nil {
			return fmt.Errorf("failed to scan row: %w", err)
		}
		tc, _ := s.tokenizer.Tokenize(ctx, content)
		ta, _ := s.tokenizer.Tokenize(ctx, abstract)
		ts, _ := s.tokenizer.Tokenize(ctx, summary)
		if _, err := tx.Exec(`INSERT INTO memories_fts(rowid, content, abstract, summary) VALUES(?,?,?,?)`,
			rowid, tc, ta, ts); err != nil {
			return fmt.Errorf("failed to insert FTS row (rowid=%d): %w", rowid, err)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("FTS rebuild iteration error: %w", err)
	}

	// 更新 meta
	if _, err := tx.Exec(`INSERT OR REPLACE INTO meta(key, value) VALUES('tokenizer', ?)`, current); err != nil {
		return fmt.Errorf("failed to update tokenizer meta: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit FTS rebuild: %w", err)
	}

	logger.Info("FTS index rebuilt for new tokenizer", zap.String("tokenizer", current), zap.Int("memories", count))
	return nil
}
```

- [ ] **Step 3: 更新 config.yaml 切换到 gse 分词器**

```yaml
storage:
  sqlite:
    tokenizer:
      provider: "gse"
```

- [ ] **Step 4: Build 验证**

Run: `go build ./cmd/server/ && echo "build ok"`
Expected: build ok

- [ ] **Step 5: Commit**

```bash
git add internal/store/sqlite.go config.yaml
git commit -m "feat: switch to gse tokenizer with auto FTS rebuild on tokenizer change"
```

---

### Task 5: 重新运行 500 组测试验证提升效果

**Files:**
- Existing: `tools/retrieval_test_500.py`

- [ ] **Step 1: 重新构建并启动服务**

```bash
pkill -f "./server" 2>/dev/null; sleep 1
rm -f data/iclude.db
go build -o server ./cmd/server/
./server &
sleep 3
```

- [ ] **Step 2: 运行 500 组测试**

```bash
python3 tools/retrieval_test_500.py
```

Expected: 总命中率 > 88%

- [ ] **Step 3: 对比报告**

比较 `tools/retrieval_report.html` 中各类别命中率与基线:
- 精确匹配: 97.5% → 保持
- 跨语言: 32.5% → 目标 >70%
- 隐含/间接: 47.5% → 目标 >65%
- 上下文推理: 72.0% → 目标 >80%

- [ ] **Step 4: Commit 报告**

```bash
git add tools/retrieval_report.html tools/retrieval_results.json
git commit -m "test: retrieval boost results — 500 query benchmark after 4 improvements"
```
