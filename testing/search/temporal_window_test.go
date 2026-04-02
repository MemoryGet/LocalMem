package search_test

import (
	"testing"
	"time"

	"iclude/internal/search"
)

func TestResolveTemporalWindow(t *testing.T) {
	now := time.Date(2026, 4, 2, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		query      string
		wantDays   int
		wantOffset int
	}{
		// Chinese
		{"今天", "今天讨论了什么", 1, 0},
		{"昨天", "昨天的会议记录", 1, -1},
		{"前天", "前天发生了什么", 1, -2},
		{"本周", "本周做了哪些事", 7, 0},
		{"上周", "上周的进度", 7, -7},
		{"本月", "本月的目标", 30, 0},
		{"上个月", "上个月的总结", 30, -30},
		{"最近几天", "最近几天有什么变化", 7, 0},
		{"最近", "最近做了什么", 30, 0},
		{"今年", "今年的计划", 365, 0},
		{"去年", "去年的成果", 365, -365},

		// English
		{"today", "what did we discuss today", 1, 0},
		{"yesterday", "yesterday's meeting notes", 1, -1},
		{"this week", "this week's progress", 7, 0},
		{"last week", "what happened last week", 7, -7},
		{"last month", "last month summary", 30, -30},
		{"this month", "this month goals", 30, 0},
		{"this year", "this year plan", 365, 0},
		{"last year", "last year results", 365, -365},

		// Default
		{"generic temporal", "recent changes in the system", 30, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			center, window := search.ResolveTemporalWindow(tt.query, now)

			gotDays := int(window.Hours() / 24)
			if gotDays != tt.wantDays {
				t.Errorf("window: got %d days, want %d", gotDays, tt.wantDays)
			}

			offsetDays := int(center.Sub(now).Hours() / 24)
			if offsetDays != tt.wantOffset {
				t.Errorf("offset: got %d days, want %d", offsetDays, tt.wantOffset)
			}
		})
	}
}
