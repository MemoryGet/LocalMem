package strategy_test

import (
	"testing"

	"iclude/internal/search/strategy"
)

func TestRuleClassifier_Select(t *testing.T) {
	tests := []struct {
		name     string
		query    string
		intent   string
		expected string
	}{
		{"short query", "项目名", "", "fast"},
		{"very short", "abc", "", "fast"},
		{"temporal zh", "最近的项目进展", "", "exploration"},
		{"temporal en", "last week changes", "", "exploration"},
		{"relational zh", "这个模块依赖什么", "", "association"},
		{"relational en", "related to auth module", "", "association"},
		{"intent keyword", "点券余额字段", "keyword", "precision"},
		{"intent semantic", "类似的经验", "semantic", "semantic"},
		{"intent temporal", "时间查询", "temporal", "exploration"},
		{"intent relational", "关系查询", "relational", "association"},
		{"exploratory zh", "如何优化性能", "", "exploration"},
		{"exploratory en", "how to optimize", "", "exploration"},
		{"general fallback", "一段普通的查询文本超过五个字", "", "exploration"},
		{"general with intent", "一段普通文本", "general", "exploration"},
		{"empty query", "", "", "exploration"},
		// Aggregation patterns — only unambiguously computational markers route here
		{"aggregation total en", "how much total did I spend on bikes", "", "aggregation"},
		{"aggregation average", "what is the average age of my family", "", "aggregation"},
		{"aggregation sum", "sum of all charity donations", "", "aggregation"},
		{"aggregation combined", "combined expenses across all trips", "", "aggregation"},
		{"aggregation in total", "how many hours in total did I spend driving", "", "aggregation"},
		{"aggregation zh total", "我一共花了多少钱", "", "aggregation"},
		{"aggregation zh how many", "我去过多少个城市", "", "aggregation"},
		{"aggregation intent", "some query about things", "aggregation", "aggregation"},
		// "how many" alone is point-retrieval, not aggregation — routes to exploration
		{"how many point retrieval", "how many doctors did I visit", "", "exploration"},
		// "overall" alone routes to exploration (falls through to default)
		{"overall not aggregation", "overall spending this year", "", "exploration"},
		// Temporal anchor beats aggregation pattern
		{"how long temporal", "how long did last week's meeting take", "", "exploration"},
		// Historical listing queries: temporal scope word + listing intent → aggregation
		{"historical list basic", "之前我都做了哪些事情", "", "aggregation"},
		{"historical list yiqian", "以前都做了什么事", "", "aggregation"},
		{"historical list guoqu", "过去做过哪些任务", "", "aggregation"},
		{"historical list suoyou", "之前完成了哪些项目", "", "aggregation"},
		// Specific temporal anchor still wins even with listing intent
		{"temporal anchor with list", "上周我都做了哪些事情", "", "exploration"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := strategy.NewRuleClassifier("exploration")
			result := c.Select(tt.query, tt.intent)
			if result != tt.expected {
				t.Errorf("Select(%q, %q) = %q, want %q", tt.query, tt.intent, result, tt.expected)
			}
		})
	}
}

func TestRuleClassifier_CustomFallback(t *testing.T) {
	tests := []struct {
		name             string
		fallbackPipeline string
		query            string
		intent           string
		expected         string
	}{
		{"custom fallback used", "precision", "一段普通的查询文本超过五个字", "", "precision"},
		{"custom fallback with general intent", "semantic", "一段普通文本", "general", "semantic"},
		{"pattern still overrides", "precision", "最近的项目进展", "", "exploration"},
		{"short query still fast", "semantic", "abc", "", "fast"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := strategy.NewRuleClassifier(tt.fallbackPipeline)
			result := c.Select(tt.query, tt.intent)
			if result != tt.expected {
				t.Errorf("Select(%q, %q) = %q, want %q", tt.query, tt.intent, result, tt.expected)
			}
		})
	}
}
