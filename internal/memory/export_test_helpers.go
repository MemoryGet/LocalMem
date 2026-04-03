// Package memory 测试辅助导出（供外部测试包使用）/ Test helper exports for external test packages
package memory

// BuildExtractPromptForTest 导出 buildExtractPrompt 供测试使用 / Export buildExtractPrompt for testing
func BuildExtractPromptForTest(e *Extractor) string {
	return e.buildExtractPrompt()
}

// MapKeysForTest 导出 mapKeys 供测试使用 / Export mapKeys for testing
func MapKeysForTest(m map[string]bool) []string {
	return mapKeys(m)
}
