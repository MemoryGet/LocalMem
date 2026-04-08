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
