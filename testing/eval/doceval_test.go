package eval_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	eval "iclude/testing/eval"

	"github.com/stretchr/testify/require"
)

// docSampleCases 针对 testdata/doceval/localmem_config_guide.md 的评测用例
// Evaluation cases for the built-in sample document.
var docSampleCases = []eval.DocEvalCase{
	{Query: "SQLite 的默认数据库路径在哪里", Keywords: []string{"memories.db", "sqlite.path", "数据库路径"}},
	{Query: "Qdrant 向量维度应该设置多少", Keywords: []string{"4096", "dimension", "向量维度"}},
	{Query: "如何配置 embedding 模型", Keywords: []string{"embedding", "provider", "base_url", "Qwen3"}},
	{Query: "What pipeline is recommended for document knowledge base", Keywords: []string{"semantic", "文档知识库"}},
	{Query: "如何开启 API 认证", Keywords: []string{"auth.enabled", "api_keys", "API Key", "认证"}},
	{Query: "vector query instruction 有什么作用", Keywords: []string{"指令前缀", "instruction", "查询侧", "非对称"}},
	{Query: "BM25 权重如何配置", Keywords: []string{"bm25_weights", "content", "summary", "权重"}},
	{Query: "多租户数据隔离怎么实现", Keywords: []string{"team_id", "owner_id", "隔离", "多租户"}},
	{Query: "Qdrant 使用什么距离度量", Keywords: []string{"余弦", "Cosine", "相似度"}},
	{Query: "embedding 批量写入并发数怎么确定", Keywords: []string{"rate limit", "429", "指数", "并发"}},
}

// loadDocTestdata 加载 testdata/doceval/ 目录下的所有 .md/.txt 文档
func loadDocTestdata(t *testing.T) []eval.DocInput {
	t.Helper()
	dir := filepath.Join("testdata", "doceval")
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Skipf("skip: testdata/doceval not found: %v", err)
	}
	var docs []eval.DocInput
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := filepath.Ext(e.Name())
		if ext != ".md" && ext != ".txt" && ext != ".markdown" {
			continue
		}
		content, err := os.ReadFile(filepath.Join(dir, e.Name()))
		require.NoError(t, err)
		docs = append(docs, eval.DocInput{
			Name:       e.Name(),
			Content:    string(content),
			IsMarkdown: ext == ".md" || ext == ".markdown",
		})
	}
	return docs
}

// TestDocKBEval_Sample 使用内置示例文档跑文档 KB 检索评测（不需要外部依赖）
// Runs doc KB eval with the built-in sample document. Always runnable — no Qdrant required for pure FTS.
func TestDocKBEval_Sample(t *testing.T) {
	eval.LoadTestConfig()

	docs := loadDocTestdata(t)
	if len(docs) == 0 {
		t.Skip("skip: no documents found in testdata/doceval")
	}
	t.Logf("Loaded %d document(s)", len(docs))

	tmpDB := filepath.Join(t.TempDir(), "doceval_sample.db")
	report, err := eval.RunDocKBEval(context.Background(), docs, docSampleCases, tmpDB)
	require.NoError(t, err)

	eval.PrintDocEvalReport(report)

	t.Logf("HitRate=%.1f%%  MRR=%.3f  Hits=%d/%d  Duration=%s",
		report.HitRate, report.MRR, report.Hits, report.Total, report.Duration)
	for _, c := range report.Cases {
		if !c.Hit {
			t.Logf("  MISS: %s", c.Query)
		}
	}

	if report.HitRate < 60.0 {
		t.Errorf("hit rate %.1f%% below 60%% — document retrieval pipeline may be broken", report.HitRate)
	}
}

// TestDocKBEval_Custom 使用真实文档目录跑评测
// Run doc KB eval against real documents. Set DOC_EVAL_DIR to a directory of .md/.txt files.
// Customize the eval cases by editing docCustomCases below.
func TestDocKBEval_Custom(t *testing.T) {
	docDir := os.Getenv("DOC_EVAL_DIR")
	if docDir == "" {
		t.Skip("skip: set DOC_EVAL_DIR=/path/to/your/docs to run against real documents")
	}

	eval.LoadTestConfig()

	entries, err := os.ReadDir(docDir)
	require.NoError(t, err, "read DOC_EVAL_DIR")

	var docs []eval.DocInput
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		ext := filepath.Ext(e.Name())
		if ext != ".md" && ext != ".txt" && ext != ".markdown" {
			continue
		}
		content, err := os.ReadFile(filepath.Join(docDir, e.Name()))
		if err != nil {
			t.Logf("skip %s: %v", e.Name(), err)
			continue
		}
		docs = append(docs, eval.DocInput{
			Name:       e.Name(),
			Content:    string(content),
			IsMarkdown: ext == ".md" || ext == ".markdown",
		})
	}

	if len(docs) == 0 {
		t.Skip("no .md/.txt files found in DOC_EVAL_DIR")
	}
	t.Logf("Loaded %d documents from %s", len(docs), docDir)

	// ── 替换为你自己的评测用例 / Replace with your own eval cases ──────────────
	cases := docCustomCases
	if len(cases) == 0 {
		cases = docSampleCases // 兜底使用示例用例 / fallback to sample cases
	}

	tmpDB := filepath.Join(t.TempDir(), "doceval_custom.db")
	report, err := eval.RunDocKBEval(context.Background(), docs, cases, tmpDB)
	require.NoError(t, err)

	eval.PrintDocEvalReport(report)
	t.Logf("HitRate=%.1f%%  MRR=%.3f  Total=%d", report.HitRate, report.MRR, report.Total)
}

// docCustomCases 自定义评测用例（针对你的真实文档）
// Custom eval cases for your actual documents — edit these to match your KB content.
var docCustomCases = []eval.DocEvalCase{
	// 示例：替换为你文档中真实存在的查询 / Replace with real queries from your documents
	// {Query: "如何重置密码", Keywords: []string{"重置密码", "忘记密码", "password reset"}},
	// {Query: "API rate limit 是多少", Keywords: []string{"rate limit", "限流", "qps"}},
}
