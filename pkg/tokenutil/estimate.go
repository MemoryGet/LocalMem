// Package tokenutil 提供 token 估算工具 / Token estimation utilities
package tokenutil

// EstimateTokens 估算 token 数（CJK 感知）/ Estimate token count (CJK-aware)
// 混合策略：CJK 字符 1 rune ≈ 1 token；英文/数字按空白分词后每词 ≈ 1.3 token
// 误差范围约 ±15%，远优于纯 rune 计数对英文的低估 / ~±15% error, much better than pure rune for English
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}
	var cjkCount, wordLen, wordCount int
	for _, r := range text {
		if isCJK(r) {
			// 每个 CJK 字符约 1 token / Each CJK char ≈ 1 token
			if wordLen > 0 {
				wordCount++
				wordLen = 0
			}
			cjkCount++
		} else if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			if wordLen > 0 {
				wordCount++
				wordLen = 0
			}
		} else {
			wordLen++
		}
	}
	if wordLen > 0 {
		wordCount++
	}
	// 英文词平均 1.3 token（含标点分割）/ English words avg 1.3 tokens (accounts for punctuation splits)
	englishTokens := int(float64(wordCount)*1.3 + 0.5)
	return cjkCount + englishTokens
}

// isCJK 判断是否为 CJK 统一汉字或日韩字符 / Check if rune is CJK/Hangul/Kana character
func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || // CJK Unified Ideographs
		(r >= 0x3400 && r <= 0x4DBF) || // CJK Extension A
		(r >= 0x20000 && r <= 0x2A6DF) || // CJK Extension B
		(r >= 0xAC00 && r <= 0xD7AF) || // Hangul Syllables
		(r >= 0x3040 && r <= 0x30FF) // Hiragana + Katakana
}
