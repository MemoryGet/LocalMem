package model

// DisclosureItem 披露条目 / Disclosure item at a specific detail level
type DisclosureItem struct {
	Memory      *Memory   `json:"memory"`
	Score       float64   `json:"score"`
	Source      string    `json:"source"`
	DetailLevel string    `json:"detail_level"` // full / summary / excerpt / pointer
	Entities    []*Entity `json:"entities,omitempty"`
	Content     string    `json:"content"` // 按 detail_level 裁剪的内容 / Content trimmed to detail level
	Tokens      int       `json:"tokens"`  // 实际 token 数 / Actual token count
}

// DisclosurePipeline 单条管线输出 / Single pipeline output
type DisclosurePipeline struct {
	Name       string            `json:"name"`       // core / context / entity / timeline
	Weight     float64           `json:"weight"`
	Budget     int               `json:"budget"`      // 分配的 token 预算 / Allocated token budget
	UsedTokens int               `json:"used_tokens"` // 实际使用 / Actually used
	Items      []*DisclosureItem `json:"items"`
}

// DisclosureResult 多管线渐进式披露结果 / Multi-pipeline progressive disclosure result
type DisclosureResult struct {
	Pipelines   []*DisclosurePipeline `json:"pipelines"`
	TotalBudget int                   `json:"total_budget"`
	TotalUsed   int                   `json:"total_used"`
	Overflow    []*DisclosureItem     `json:"overflow,omitempty"` // 超预算的扩展指针 / Over-budget expansion pointers
}
