package eval_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	eval "iclude/testing/eval"

	"github.com/stretchr/testify/require"
)

// ─── Wikipedia 拉取工具 / Wikipedia fetch helpers ─────────────────────────────

// fetchWikiSummary 从 Wikipedia MediaWiki action API 拉取文章介绍段（兼容性更好）
// Fetch article intro via MediaWiki action API (more compatible than REST API).
// lang: "en" | "zh" | "ja" | ...
func fetchWikiSummary(lang, title string) (string, error) {
	// MediaWiki action API — extracts intro section as plain text
	url := fmt.Sprintf(
		"https://%s.wikipedia.org/w/api.php?action=query&titles=%s&prop=extracts&exintro=true&explaintext=true&format=json&redirects=1",
		lang, strings.ReplaceAll(title, " ", "_"),
	)
	client := &http.Client{Timeout: 15 * time.Second}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "LocalMemEval/1.0 (https://github.com/local-mem; eval test)")

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch %s/%s: %w", lang, title, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("fetch %s/%s: status %d", lang, title, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return "", err
	}

	// 解析 query.pages.{id}.extract / Parse query.pages.{id}.extract
	var result struct {
		Query struct {
			Pages map[string]struct {
				Title   string `json:"title"`
				Extract string `json:"extract"`
			} `json:"pages"`
		} `json:"query"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parse %s/%s: %w", lang, title, err)
	}
	for _, page := range result.Query.Pages {
		if page.Extract == "" {
			return "", fmt.Errorf("empty extract for %s/%s", lang, title)
		}
		// 截取前 3000 字节避免单文章过大 / Cap at 3000 chars to avoid oversized chunks
		text := page.Extract
		if len([]rune(text)) > 3000 {
			runes := []rune(text)[:3000]
			text = string(runes) + "..."
		}
		return fmt.Sprintf("# %s\n\n%s", page.Title, text), nil
	}
	return "", fmt.Errorf("no pages returned for %s/%s", lang, title)
}

// fetchWikiSections 拉取文章的多个 section（通过 mobile-sections API）
// Fetch multiple sections via mobile-sections API for richer content.
func fetchWikiSections(lang, title string, maxSections int) (string, error) {
	url := fmt.Sprintf("https://%s.wikipedia.org/api/rest_v1/page/mobile-sections/%s",
		lang, strings.ReplaceAll(title, " ", "_"))

	client := &http.Client{Timeout: 20 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("fetch sections %s: %w", title, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("sections %s: status %d", title, resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // 4 MB cap
	if err != nil {
		return "", err
	}

	// 解析 lead.sections[].text 和 remaining.sections[].text
	// Parse lead.sections[].text and remaining.sections[].text
	var raw struct {
		Lead struct {
			Sections []struct {
				Line string `json:"line"`
				Text string `json:"text"`
			} `json:"sections"`
		} `json:"lead"`
		Remaining struct {
			Sections []struct {
				Line string `json:"line"`
				Text string `json:"text"`
			} `json:"sections"`
		} `json:"remaining"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return "", fmt.Errorf("parse sections %s: %w", title, err)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "# %s\n\n", title)

	count := 0
	for _, s := range raw.Lead.Sections {
		text := stripHTMLBasic(s.Text)
		if text != "" {
			sb.WriteString(text)
			sb.WriteString("\n\n")
			count++
		}
	}
	for _, s := range raw.Remaining.Sections {
		if count >= maxSections {
			break
		}
		text := stripHTMLBasic(s.Text)
		if text == "" {
			continue
		}
		if s.Line != "" {
			fmt.Fprintf(&sb, "## %s\n\n", s.Line)
		}
		sb.WriteString(text)
		sb.WriteString("\n\n")
		count++
	}
	return sb.String(), nil
}

// stripHTMLBasic 去除基础 HTML 标签（<p>, <b>, <i>, <a> 等）
// Strip basic HTML tags — keeps text content.
func stripHTMLBasic(s string) string {
	var out strings.Builder
	inTag := false
	for _, r := range s {
		switch {
		case r == '<':
			inTag = true
		case r == '>':
			inTag = false
			out.WriteRune(' ')
		case !inTag:
			out.WriteRune(r)
		}
	}
	return strings.TrimSpace(out.String())
}

// ─── 真实数据测试 / Real data test ────────────────────────────────────────────

// TestDocKBEval_Real 使用仓库自身文档 + Wikipedia 技术文章跑真实检索评测
// Real doc KB eval: uses the repo's own docs + Wikipedia technical articles.
// Requires network access for Wikipedia fetch. Skips individual articles on failure.
func TestDocKBEval_Real(t *testing.T) {
	if testing.Short() {
		t.Skip("skip: real eval requires network + Qdrant (omit -short to run)")
	}
	eval.LoadTestConfig()

	repoRoot := filepath.Join("..", "..")
	var docs []eval.DocInput

	// ── 1. 加载仓库自身文档 / Load repo docs ─────────────────────────────────
	repoDocPaths := []struct {
		path       string
		name       string
		isMarkdown bool
	}{
		{filepath.Join(repoRoot, "CLAUDE.md"), "CLAUDE.md", true},
		{filepath.Join(repoRoot, "docs", "eval-quick-start.md"), "eval-quick-start.md", true},
		{filepath.Join(repoRoot, "docs", "i18n", "README.zh.md"), "README.zh.md", true},
	}
	for _, d := range repoDocPaths {
		content, err := os.ReadFile(d.path)
		if err != nil {
			t.Logf("skip %s: %v", d.name, err)
			continue
		}
		docs = append(docs, eval.DocInput{Name: d.name, Content: string(content), IsMarkdown: d.isMarkdown})
		t.Logf("loaded repo doc: %s (%d bytes)", d.name, len(content))
	}

	// ── 2. 拉取 Wikipedia 技术文章 / Fetch Wikipedia articles ─────────────────
	wikiArticles := []struct {
		lang    string
		title   string
		sections int
	}{
		{"en", "SQLite", 4},
		{"en", "Qdrant", 3},
		{"zh", "向量数据库", 3},
		{"en", "Full-text_search", 3},
	}
	for _, a := range wikiArticles {
		var content string
		var err error
		// 先试 sections API，失败降级到 summary
		content, err = fetchWikiSections(a.lang, a.title, a.sections)
		if err != nil {
			t.Logf("sections fetch failed for %s/%s, trying summary: %v", a.lang, a.title, err)
			content, err = fetchWikiSummary(a.lang, a.title)
		}
		if err != nil {
			t.Logf("skip wiki %s/%s: %v", a.lang, a.title, err)
			continue
		}
		name := fmt.Sprintf("wiki_%s_%s.md", a.lang, strings.ReplaceAll(a.title, "/", "_"))
		docs = append(docs, eval.DocInput{Name: name, Content: content, IsMarkdown: true})
		t.Logf("fetched wiki: %s (%d bytes)", name, len(content))
	}

	if len(docs) == 0 {
		t.Fatal("no documents loaded — cannot run eval")
	}
	t.Logf("Total: %d documents", len(docs))

	// ── 3. 评测用例（覆盖中英文 + 语义 gap + 跨文档）────────────────────────
	cases := []eval.DocEvalCase{
		// 仓库文档：CLAUDE.md
		{Query: "这个项目的 Go 模块名是什么", Keywords: []string{"iclude"}},
		{Query: "how to build the project", Keywords: []string{"make build", "dist"}},
		{Query: "MCP server 默认端口是多少", Keywords: []string{"8081"}},
		{Query: "SQLite WAL 模式有什么作用", Keywords: []string{"WAL", "并发", "concurrent"}},
		{Query: "session lifecycle states", Keywords: []string{"created", "active", "finalizing", "finalized"}},
		{Query: "memory 的 retention tier 有哪些", Keywords: []string{"permanent", "long_term", "standard", "short_term"}},

		// 仓库文档：README.zh.md / eval-quick-start.md
		{Query: "如何运行 LoCoMo 评测", Keywords: []string{"locomo", "LoCoMo", "eval"}},
		{Query: "支持哪些 embedding 提供商", Keywords: []string{"OpenAI", "Ollama", "embedding"}},

		// Wikipedia：SQLite
		{Query: "SQLite 由谁创建", Keywords: []string{"D. Richard Hipp", "Hipp"}},
		{Query: "SQLite 适合哪些场景", Keywords: []string{"embedded", "lightweight", "serverless"}},

		// Wikipedia：向量数据库 / Full-text search
		{Query: "什么是向量数据库", Keywords: []string{"向量", "嵌入", "语义", "embedding"}},
		{Query: "全文检索的核心算法是什么", Keywords: []string{"BM25", "TF-IDF", "inverted index", "倒排"}},

		// 跨语言语义 gap 测试（英文查询 vs 中文文档）
		{Query: "How does the system handle semantic search", Keywords: []string{"语义", "向量", "semantic", "vector"}},
		{Query: "数据库索引与倒排索引的区别", Keywords: []string{"index", "inverted", "full-text", "索引"}},
	}

	tmpDB := filepath.Join(t.TempDir(), "doceval_real.db")
	report, err := eval.RunDocKBEval(context.Background(), docs, cases, tmpDB)
	require.NoError(t, err)

	eval.PrintDocEvalReport(report)

	t.Logf("=== Final: HitRate=%.1f%%  MRR=%.3f  Hits=%d/%d ===",
		report.HitRate, report.MRR, report.Hits, report.Total)

	// 软性阈值：真实数据多样，60% 即可，关键看 miss 的原因
	// Soft threshold: real data is diverse; 60% floor, investigate misses
	if report.HitRate < 60.0 {
		t.Errorf("hit rate %.1f%% below 60%% floor — check pipeline routing or chunking", report.HitRate)
	}
}
