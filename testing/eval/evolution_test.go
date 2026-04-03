package eval_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"iclude/testing/eval"
)

// TestEvolutionAwareRetrieval 验证 memory_class 权重对检索质量的影响
// Verifies that memory_class weights improve retrieval quality for evolved memories
func TestEvolutionAwareRetrieval(t *testing.T) {
	// Build dataset with three-layer memories
	ds := buildEvolutionDataset()

	// Run A: All memories as episodic (flat baseline)
	flatDS := cloneDatasetFlat(ds)
	flatRunner, flatCleanup := createRunner(t, "flat")
	defer flatCleanup()
	flatReport, err := flatRunner.Run(context.Background(), flatDS, "fts-flat")
	if err != nil {
		t.Fatalf("flat run: %v", err)
	}

	// Run B: Memories with correct class labels (evolved)
	evolvedRunner, evolvedCleanup := createRunner(t, "evolved")
	defer evolvedCleanup()
	evolvedReport, err := evolvedRunner.Run(context.Background(), ds, "fts-evolved")
	if err != nil {
		t.Fatalf("evolved run: %v", err)
	}

	// Print both reports
	eval.PrintReport(flatReport)
	eval.PrintReport(evolvedReport)

	// Compare
	fmt.Printf("\n=== Evolution Impact ===\n")
	fmt.Printf("  Flat HitRate:    %.1f%% MRR: %.3f\n", flatReport.Metrics.HitRate, flatReport.Metrics.MRR)
	fmt.Printf("  Evolved HitRate: %.1f%% MRR: %.3f\n", evolvedReport.Metrics.HitRate, evolvedReport.Metrics.MRR)
	fmt.Printf("  Delta HitRate:   %+.1f%%\n", evolvedReport.Metrics.HitRate-flatReport.Metrics.HitRate)
	fmt.Printf("  Delta MRR:       %+.3f\n", evolvedReport.Metrics.MRR-flatReport.Metrics.MRR)

	// Evolved should be >= flat (class weights should not hurt)
	if evolvedReport.Metrics.MRR < flatReport.Metrics.MRR-0.01 {
		t.Errorf("evolved MRR (%.3f) significantly worse than flat (%.3f)", evolvedReport.Metrics.MRR, flatReport.Metrics.MRR)
	}
}

func createRunner(t *testing.T, suffix string) (*eval.Runner, func()) {
	t.Helper()
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, fmt.Sprintf("eval_%s.db", suffix))
	r, cleanup, err := eval.NewRunner(dbPath, "fts")
	if err != nil {
		t.Fatalf("create runner %s: %v", suffix, err)
	}
	return r, cleanup
}

func cloneDatasetFlat(ds *eval.EvalDataset) *eval.EvalDataset {
	flat := &eval.EvalDataset{
		Name:        ds.Name + " (flat)",
		Description: ds.Description,
		Cases:       ds.Cases,
	}
	flat.SeedMemories = make([]eval.SeedMemory, len(ds.SeedMemories))
	for i, s := range ds.SeedMemories {
		flat.SeedMemories[i] = eval.SeedMemory{
			Content:     s.Content,
			Kind:        s.Kind,
			SubKind:     s.SubKind,
			MemoryClass: "", // force episodic
		}
	}
	return flat
}

// buildEvolutionDataset 构建三层演化评测数据集
// 每组主题包含：多条 episodic（原始事件）+ 1条 semantic（观察规律）+ 1条 procedural（策略结论）
// 查询设计为：语义更贴近 semantic/procedural 表述
func buildEvolutionDataset() *eval.EvalDataset {
	seeds := []eval.SeedMemory{
		// === Topic 1: 前端技术栈 ===
		{Content: "2024年3月15日团队讨论了React框架用于新项目的可能性", Kind: "fact", MemoryClass: "episodic"},
		{Content: "2024年4月2日将商品详情页从Vue重写为React完成上线", Kind: "fact", MemoryClass: "episodic"},
		{Content: "2024年5月10日新入职前端工程师反馈React比Vue上手更快", Kind: "fact", MemoryClass: "episodic"},
		{Content: "2024年6月前端组全面采用React hooks替代class组件", Kind: "fact", MemoryClass: "episodic"},
		{Content: "团队在前端技术栈上逐步从Vue迁移到React，效率和满意度均有提升", Kind: "consolidated", MemoryClass: "semantic"},
		{Content: "新前端项目应默认使用React加TypeScript技术栈，Vue仅用于维护旧项目", Kind: "mental_model", MemoryClass: "procedural"},

		// === Topic 2: 数据库选型 ===
		{Content: "2024年1月PostgreSQL在高并发写入场景出现锁等待超时", Kind: "fact", MemoryClass: "episodic"},
		{Content: "2024年2月测试ClickHouse处理日志分析查询速度比PostgreSQL快20倍", Kind: "fact", MemoryClass: "episodic"},
		{Content: "2024年3月将监控日志存储从PostgreSQL迁移到ClickHouse", Kind: "fact", MemoryClass: "episodic"},
		{Content: "2024年4月ClickHouse集群稳定运行日均处理10亿条日志", Kind: "fact", MemoryClass: "episodic"},
		{Content: "分析型查询应使用ClickHouse而非PostgreSQL，事务型业务保留PostgreSQL", Kind: "consolidated", MemoryClass: "semantic"},
		{Content: "数据库选型策略：OLTP用PostgreSQL、OLAP用ClickHouse、缓存用Redis，禁止混用", Kind: "mental_model", MemoryClass: "procedural"},

		// === Topic 3: 部署流程 ===
		{Content: "2024年5月一次手动部署导致生产环境配置错误停机2小时", Kind: "fact", MemoryClass: "episodic"},
		{Content: "2024年6月引入GitHub Actions自动化CI/CD流水线", Kind: "fact", MemoryClass: "episodic"},
		{Content: "2024年7月所有服务迁移到容器化部署使用Docker和Kubernetes", Kind: "fact", MemoryClass: "episodic"},
		{Content: "2024年8月部署频率从每月一次提升到每日多次零回滚", Kind: "fact", MemoryClass: "episodic"},
		{Content: "自动化CI/CD和容器化显著提升了部署频率和稳定性", Kind: "consolidated", MemoryClass: "semantic"},
		{Content: "所有服务必须通过CI/CD流水线部署禁止手动操作生产环境", Kind: "mental_model", MemoryClass: "procedural"},

		// === Topic 4: 团队协作 ===
		{Content: "2024年3月开始每日站会经常超时讨论偏离主题", Kind: "fact", MemoryClass: "episodic"},
		{Content: "2024年4月改为异步文字站会每人发Slack消息汇报三件事", Kind: "fact", MemoryClass: "episodic"},
		{Content: "2024年5月团队反馈异步站会节省时间效率提升", Kind: "fact", MemoryClass: "episodic"},
		{Content: "2024年6月取消周五全员大会改为双周一次", Kind: "fact", MemoryClass: "episodic"},
		{Content: "异步沟通比同步会议更适合分布式团队节省时间且不影响信息同步", Kind: "consolidated", MemoryClass: "semantic"},
		{Content: "日常沟通默认异步Slack消息，仅在需要实时讨论时召开视频会议", Kind: "mental_model", MemoryClass: "procedural"},

		// === Topic 5: 安全实践 ===
		{Content: "2024年2月一个API密钥被提交到GitHub公开仓库导致安全事件", Kind: "fact", MemoryClass: "episodic"},
		{Content: "2024年3月引入git-secrets预提交钩子扫描敏感信息", Kind: "fact", MemoryClass: "episodic"},
		{Content: "2024年4月所有密钥迁移到HashiCorp Vault统一管理", Kind: "fact", MemoryClass: "episodic"},
		{Content: "2024年5月通过安全审计零密钥泄露事件", Kind: "fact", MemoryClass: "episodic"},
		{Content: "密钥管理从代码仓库迁移到Vault后安全性显著提升", Kind: "consolidated", MemoryClass: "semantic"},
		{Content: "所有密钥必须存放在Vault中禁止硬编码到源码或环境变量文件", Kind: "mental_model", MemoryClass: "procedural"},
	}

	// Queries designed to match semantic/procedural content better
	cases := []eval.EvalCase{
		// Topic 1 queries — target semantic/procedural
		{Query: "我们的前端技术栈策略是什么", Expected: []string{"React", "TypeScript"}, Category: "strategy", Difficulty: "medium"},
		{Query: "前端框架的选择标准", Expected: []string{"React"}, Category: "strategy", Difficulty: "medium"},
		{Query: "Vue和React团队用哪个", Expected: []string{"React", "Vue"}, Category: "preference", Difficulty: "easy"},

		// Topic 2 queries
		{Query: "数据库选型原则是什么", Expected: []string{"PostgreSQL", "ClickHouse"}, Category: "strategy", Difficulty: "medium"},
		{Query: "分析查询应该用什么数据库", Expected: []string{"ClickHouse"}, Category: "strategy", Difficulty: "easy"},
		{Query: "OLTP和OLAP分别用什么", Expected: []string{"PostgreSQL", "ClickHouse"}, Category: "strategy", Difficulty: "medium"},

		// Topic 3 queries
		{Query: "部署流程有什么规定", Expected: []string{"CI/CD", "禁止手动"}, Category: "strategy", Difficulty: "medium"},
		{Query: "能不能手动部署到生产环境", Expected: []string{"禁止", "CI/CD"}, Category: "strategy", Difficulty: "easy"},
		{Query: "我们的部署方式是什么", Expected: []string{"Docker", "Kubernetes", "CI/CD"}, Category: "strategy", Difficulty: "medium"},

		// Topic 4 queries
		{Query: "团队沟通方式的规范", Expected: []string{"异步", "Slack"}, Category: "strategy", Difficulty: "medium"},
		{Query: "什么时候应该开会什么时候用消息", Expected: []string{"异步", "视频会议"}, Category: "strategy", Difficulty: "hard"},
		{Query: "站会是怎么做的", Expected: []string{"异步", "Slack"}, Category: "preference", Difficulty: "easy"},

		// Topic 5 queries
		{Query: "密钥管理的规则是什么", Expected: []string{"Vault", "禁止"}, Category: "strategy", Difficulty: "medium"},
		{Query: "API密钥应该放在哪里", Expected: []string{"Vault"}, Category: "strategy", Difficulty: "easy"},
		{Query: "怎么防止密钥泄露", Expected: []string{"Vault", "git-secrets"}, Category: "strategy", Difficulty: "medium"},

		// Cross-topic queries
		{Query: "团队有哪些最佳实践和策略", Expected: []string{"CI/CD", "异步", "Vault", "React"}, Category: "cross-topic", Difficulty: "hard"},
		{Query: "技术决策的总体原则", Expected: []string{"PostgreSQL", "ClickHouse", "React"}, Category: "cross-topic", Difficulty: "hard"},
	}

	return &eval.EvalDataset{
		Name:         "evolution-aware",
		Description:  "Three-layer memory evolution test (episodic/semantic/procedural)",
		SeedMemories: seeds,
		Cases:        cases,
	}
}
