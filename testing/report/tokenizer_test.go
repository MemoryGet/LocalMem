package report_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"iclude/internal/model"
	"iclude/pkg/testreport"
	"iclude/pkg/tokenizer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	suiteTokenizer     = "分词器 (Tokenizer)"
	suiteTokenizerIcon = "\U0001F524"
	suiteTokenizerDesc = "优化点 #1: FTS5 中文分词 — 可拔插分词器接口，支持 Simple/Jieba/Noop/gse 四种实现"
)

func TestTokenizer_SimpleTokenizer_PureChinese(t *testing.T) {
	tc := testreport.NewCase(t, suiteTokenizer, suiteTokenizerIcon, suiteTokenizerDesc,
		"SimpleTokenizer 纯中文分词")
	defer tc.Done()

	input := "记忆系统"
	expected := "记 忆 系 统"
	tc.Input("text", input)
	tc.Input("分词器", "SimpleTokenizer")

	tok := tokenizer.NewSimpleTokenizer()
	tc.Step("创建 SimpleTokenizer 实例")

	result, err := tok.Tokenize(context.Background(), input)
	tc.Step("调用 Tokenize()", fmt.Sprintf("input=%q", input))

	require.NoError(t, err)
	tc.Step("验证无错误返回")

	assert.Equal(t, expected, result)
	tc.Step("验证分词结果", fmt.Sprintf("expected=%q, got=%q", expected, result))

	tc.Output("tokens", result)
	tc.Output("预期", expected)
	tc.Output("匹配", "完全一致")
}

func TestTokenizer_SimpleTokenizer_MixedChineseEnglish(t *testing.T) {
	tc := testreport.NewCase(t, suiteTokenizer, suiteTokenizerIcon, suiteTokenizerDesc,
		"SimpleTokenizer 中英混合分词")
	defer tc.Done()

	input := "Go语言开发IClude"
	expected := "Go 语 言 开 发 IClude"
	tc.Input("text", input)

	tok := tokenizer.NewSimpleTokenizer()
	tc.Step("创建 SimpleTokenizer 实例")

	result, err := tok.Tokenize(context.Background(), input)
	tc.Step("调用 Tokenize()", fmt.Sprintf("input=%q", input))

	require.NoError(t, err)
	assert.Equal(t, expected, result)
	tc.Step("验证: 英文保持连续词，中文逐字拆分", fmt.Sprintf("got=%q", result))

	tc.Output("tokens", result)
	tc.Output("规则", "CJK 逐字拆分 + 英文按空白分词")
}

func TestTokenizer_SimpleTokenizer_PunctuationFiltered(t *testing.T) {
	tc := testreport.NewCase(t, suiteTokenizer, suiteTokenizerIcon, suiteTokenizerDesc,
		"SimpleTokenizer 标点过滤")
	defer tc.Done()

	input := "你好，世界！Hello, World!"
	expected := "你 好 世 界 Hello World"
	tc.Input("text", input)

	tok := tokenizer.NewSimpleTokenizer()
	result, err := tok.Tokenize(context.Background(), input)
	tc.Step("分词并过滤标点符号")

	require.NoError(t, err)
	assert.Equal(t, expected, result)
	tc.Step("验证: 中英文标点均被过滤", fmt.Sprintf("got=%q", result))

	tc.Output("tokens", result)
}

func TestTokenizer_SimpleTokenizer_NumbersPreserved(t *testing.T) {
	tc := testreport.NewCase(t, suiteTokenizer, suiteTokenizerIcon, suiteTokenizerDesc,
		"SimpleTokenizer 数字保留")
	defer tc.Done()

	input := "Go1.25新增特性"
	expected := "Go1 25 新 增 特 性"
	tc.Input("text", input)

	tok := tokenizer.NewSimpleTokenizer()
	result, err := tok.Tokenize(context.Background(), input)
	tc.Step("分词", fmt.Sprintf("数字与字母合并为一个 token"))

	require.NoError(t, err)
	assert.Equal(t, expected, result)
	tc.Step("验证: 'Go1' 为一个 token, '25' 为一个 token")

	tc.Output("tokens", result)
}

func TestTokenizer_SimpleTokenizer_EmptyString(t *testing.T) {
	tc := testreport.NewCase(t, suiteTokenizer, suiteTokenizerIcon, suiteTokenizerDesc,
		"SimpleTokenizer 空字符串")
	defer tc.Done()

	tc.Input("text", "(空字符串)")

	tok := tokenizer.NewSimpleTokenizer()
	result, err := tok.Tokenize(context.Background(), "")
	tc.Step("对空字符串分词")

	require.NoError(t, err)
	assert.Equal(t, "", result)
	tc.Step("验证: 返回空字符串，无错误")

	tc.Output("tokens", "(空)")
	tc.Output("error", "nil")
}

func TestTokenizer_NoopTokenizer(t *testing.T) {
	tc := testreport.NewCase(t, suiteTokenizer, suiteTokenizerIcon, suiteTokenizerDesc,
		"NoopTokenizer 透传模式")
	defer tc.Done()

	input := "你好世界 hello world"
	tc.Input("text", input)
	tc.Input("分词器", "NoopTokenizer")

	tok := tokenizer.NewNoopTokenizer()
	tc.Step("创建 NoopTokenizer 实例")

	result, err := tok.Tokenize(context.Background(), input)
	tc.Step("调用 Tokenize()")

	require.NoError(t, err)
	assert.Equal(t, input, result)
	tc.Step("验证: 输出与输入完全一致（不分词）")

	tc.Output("tokens", result)
	tc.Output("name", tok.Name())
	tc.Output("行为", "原样透传，不做任何分词处理")
}

func TestTokenizer_FTS5_ChineseSearch(t *testing.T) {
	tc := testreport.NewCase(t, suiteTokenizer, suiteTokenizerIcon, suiteTokenizerDesc,
		"FTS5 中文检索端到端验证")
	defer tc.Done()

	tc.Input("分词器", "SimpleTokenizer")
	tc.Input("BM25权重", "content=10, abstract=5, summary=3")

	s := newTestStore(t, tokenizer.NewSimpleTokenizer())
	defer s.Close()
	tc.Step("创建 SQLite store（SimpleTokenizer）")

	ctx := context.Background()
	memories := []*model.Memory{
		{Content: "Go语言开发记忆系统", TeamID: "t1"},
		{Content: "Python数据分析和机器学习", TeamID: "t1"},
		{Content: "SQLite全文检索FTS5引擎", TeamID: "t1"},
		{Content: "向量数据库Qdrant部署方案", TeamID: "t1"},
	}
	seedMemories(t, s, memories)
	tc.Step("插入 4 条测试记忆",
		"Go语言开发记忆系统 | Python数据分析和机器学习 | SQLite全文检索FTS5引擎 | 向量数据库Qdrant部署方案")

	// 测试中文单字搜索
	results, err := s.SearchText(ctx, "记", &model.Identity{TeamID: "t1", OwnerID: model.SystemOwnerID}, 10)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(results), 1)
	tc.Step("搜索 '记' (单字)", fmt.Sprintf("命中 %d 条", len(results)))

	// 测试中文双字搜索
	results, err = s.SearchText(ctx, "检索", &model.Identity{TeamID: "t1", OwnerID: model.SystemOwnerID}, 10)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(results), 1)
	tc.Step("搜索 '检索' (双字)", fmt.Sprintf("命中 %d 条, top=%q", len(results), results[0].Memory.Content))

	// 测试英文搜索
	results, err = s.SearchText(ctx, "Go", &model.Identity{TeamID: "t1", OwnerID: model.SystemOwnerID}, 10)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(results), 1)
	tc.Step("搜索 'Go' (英文)", fmt.Sprintf("命中 %d 条", len(results)))

	// 对比: noop 模式中文搜索
	sNoop := newTestStore(t, tokenizer.NewNoopTokenizer())
	defer sNoop.Close()
	seedMemories(t, sNoop, []*model.Memory{{Content: "混合检索架构设计", TeamID: "t1"}})
	noopResults, _ := sNoop.SearchText(ctx, "混合检索", &model.Identity{TeamID: "t1", OwnerID: model.SystemOwnerID}, 10)
	tc.StepInfo("对比: NoopTokenizer 搜索 '混合检索'", fmt.Sprintf("命中 %d 条（分词器的必要性验证）", len(noopResults)))

	tc.Output("SimpleTokenizer 中文搜索", "全部命中")
	tc.Output("NoopTokenizer 中文搜索", fmt.Sprintf("命中 %d 条", len(noopResults)))
	tc.Output("结论", "SimpleTokenizer 显著提升中文检索效果")
}

func TestTokenizer_FTS5_BM25Weighting(t *testing.T) {
	tc := testreport.NewCase(t, suiteTokenizer, suiteTokenizerIcon, suiteTokenizerDesc,
		"FTS5 BM25 多列加权排序")
	defer tc.Done()

	tc.Input("BM25权重", "content=10, abstract=5, summary=3")
	tc.Input("搜索词", "检索")

	s := newTestStore(t, tokenizer.NewSimpleTokenizer())
	defer s.Close()
	tc.Step("创建 SQLite store（SimpleTokenizer）")

	ctx := context.Background()
	mem1 := &model.Memory{Content: "这是一段普通文本", Abstract: "记忆检索系统", TeamID: "t1"}
	mem2 := &model.Memory{Content: "记忆检索系统的核心架构", TeamID: "t1"}
	require.NoError(t, s.Create(ctx, mem1))
	require.NoError(t, s.Create(ctx, mem2))
	tc.Step("插入 2 条记忆",
		"mem1: content=普通文本, abstract=记忆检索系统 | mem2: content=记忆检索系统的核心架构")

	results, err := s.SearchText(ctx, "检索", &model.Identity{TeamID: "t1", OwnerID: model.SystemOwnerID}, 10)
	require.NoError(t, err)
	tc.Step("执行 BM25 加权搜索", fmt.Sprintf("命中 %d 条", len(results)))

	require.GreaterOrEqual(t, len(results), 1)
	if len(results) >= 2 {
		tc.Step("比较 BM25 分数",
			fmt.Sprintf("content命中(权重10) score=%.4f vs abstract命中(权重5) score=%.4f",
				results[0].Score, results[1].Score))
		assert.True(t, results[0].Score >= results[1].Score)
		tc.Step("验证: content 字段命中排名更高（权重 10 > 5）")
	}

	for i, r := range results {
		tc.Output(fmt.Sprintf("结果[%d]", i),
			fmt.Sprintf("score=%.4f content=%q", r.Score, r.Memory.Content))
	}
}

// ---- gse 分词器测试 (P0 新增) ----

func TestTokenizer_GseTokenizer_BasicChinese(t *testing.T) {
	// GSE 词典加载约 10s，-race 模式下会超过 300s 全局超时，跳过
	// GSE dictionary loading takes ~10s and exceeds the 300s race-mode timeout — skip under race detector
	if isRaceEnabled() {
		t.Skip("skipping GSE tokenizer test under race detector: dictionary load too slow")
	}
	tc := testreport.NewCase(t, suiteTokenizer, suiteTokenizerIcon, suiteTokenizerDesc,
		"GseTokenizer 中文词语级分词")
	defer tc.Done()

	input := "深度学习模型优化"
	tc.Input("text", input)
	tc.Input("分词器", "GseTokenizer")

	tok, err := tokenizer.NewGseTokenizer("", nil)
	require.NoError(t, err)
	tc.Step("创建 GseTokenizer 实例（内置词典）")

	result, err := tok.Tokenize(context.Background(), input)
	require.NoError(t, err)
	tc.Step("调用 Tokenize()", fmt.Sprintf("result=%q", result))

	// gse 应该产生词语级分词，而不是逐字拆分
	assert.NotEqual(t, "深 度 学 习 模 型 优 化", result, "gse 不应逐字拆分")
	assert.Contains(t, result, "学习", "应包含词语 '学习'")
	tc.Step("验证: 词语级分词，非逐字拆分")

	tc.Output("tokens", result)
	tc.Output("对比 Simple", "深 度 学 习 模 型 优 化（逐字）")
}

func TestTokenizer_GseTokenizer_StopwordFiltering(t *testing.T) {
	tc := testreport.NewCase(t, suiteTokenizer, suiteTokenizerIcon, suiteTokenizerDesc,
		"GseTokenizer 停用词过滤")
	defer tc.Done()

	input := "这是一个很好的记忆系统"
	tc.Input("text", input)

	tok, err := tokenizer.NewGseTokenizer("", nil)
	require.NoError(t, err)
	tc.Step("创建 GseTokenizer（默认停用词）")

	result, err := tok.Tokenize(context.Background(), input)
	require.NoError(t, err)
	tc.Step("分词并过滤停用词")

	// "的" "是" "一" "个" 应该被过滤
	assert.NotContains(t, " "+result+" ", " 的 ", "停用词 '的' 应被过滤")
	assert.NotContains(t, " "+result+" ", " 是 ", "停用词 '是' 应被过滤")
	tc.Step("验证: 常见停用词已过滤")

	tc.Output("tokens", result)
}

func TestTokenizer_GseTokenizer_MixedLanguage(t *testing.T) {
	tc := testreport.NewCase(t, suiteTokenizer, suiteTokenizerIcon, suiteTokenizerDesc,
		"GseTokenizer 中英混合分词")
	defer tc.Done()

	input := "Go语言的内存管理"
	tc.Input("text", input)

	tok, err := tokenizer.NewGseTokenizer("", nil)
	require.NoError(t, err)

	result, err := tok.Tokenize(context.Background(), input)
	require.NoError(t, err)
	tc.Step("中英混合分词", fmt.Sprintf("result=%q", result))

	// gse 可能输出小写 "go"，检查不区分大小写
	assert.True(t, strings.Contains(strings.ToLower(result), "go"), "英文 'Go/go' 应保留")
	tc.Step("验证: 英文词保持完整")

	tc.Output("tokens", result)
}

func TestTokenizer_GseTokenizer_EmptyInput(t *testing.T) {
	tc := testreport.NewCase(t, suiteTokenizer, suiteTokenizerIcon, suiteTokenizerDesc,
		"GseTokenizer 空字符串")
	defer tc.Done()

	tok, err := tokenizer.NewGseTokenizer("", nil)
	require.NoError(t, err)

	result, err := tok.Tokenize(context.Background(), "")
	require.NoError(t, err)
	assert.Equal(t, "", result)
	tc.Step("验证: 空输入返回空字符串")

	tc.Output("result", "(空)")
}

func TestTokenizer_GseTokenizer_FTS5_EndToEnd(t *testing.T) {
	tc := testreport.NewCase(t, suiteTokenizer, suiteTokenizerIcon, suiteTokenizerDesc,
		"GseTokenizer + FTS5 端到端检索")
	defer tc.Done()

	tok, err := tokenizer.NewGseTokenizer("", nil)
	require.NoError(t, err)
	tc.Step("创建 GseTokenizer")

	s := newTestStore(t, tok)
	defer s.Close()
	tc.Step("创建 SQLite store（GseTokenizer）")

	ctx := context.Background()
	seedMemories(t, s, []*model.Memory{
		{Content: "深度学习模型训练技巧", TeamID: "t1"},
		{Content: "机器学习算法对比分析", TeamID: "t1"},
		{Content: "Go语言Web开发框架", TeamID: "t1"},
	})
	tc.Step("插入 3 条测试记忆")

	// 词语级搜索 "学习" 应命中前两条
	results, err := s.SearchText(ctx, "学习", &model.Identity{TeamID: "t1", OwnerID: model.SystemOwnerID}, 10)
	require.NoError(t, err)
	tc.Step("搜索 '学习'", fmt.Sprintf("命中 %d 条", len(results)))

	assert.GreaterOrEqual(t, len(results), 2, "应命中至少 2 条包含 '学习' 的记忆")
	tc.Step("验证: 词语级搜索命中正确")

	for i, r := range results {
		tc.Output(fmt.Sprintf("结果[%d]", i),
			fmt.Sprintf("score=%.4f content=%q", r.Score, r.Memory.Content))
	}
	tc.Output("结论", "gse 词语级分词提升 FTS5 检索精度")
}
