// Package memory 噪声过滤 / Noise pre-filter for ingest boundary
package memory

import "strings"

// IsNoiseContent 检查内容是否为噪声 / Check if content is noise (too short or matches pattern)
func IsNoiseContent(content string, minLength int, patterns []string) bool {
	if minLength > 0 && len([]rune(content)) < minLength {
		return true
	}
	trimmed := strings.TrimSpace(content)
	for _, p := range patterns {
		if trimmed == p {
			return true
		}
	}
	return false
}
