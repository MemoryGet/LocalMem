// Package tokenutil 提供 token 估算工具 / Token estimation utilities
package tokenutil

// EstimateTokens 估算 token 数（CJK 感知）/ Estimate token count (CJK-aware)
// 混合策略：CJK 字符 1 rune ≈ 1 token；非 CJK 字符按 4 chars ≈ 1 token（GPT BPE 经验值）
// 误差范围约 ±10% / ~±10% error for GPT-family tokenizers
func EstimateTokens(text string) int {
	if text == "" {
		return 0
	}
	var cjkCount, nonCJKChars int
	for _, r := range text {
		if isCJK(r) {
			cjkCount++
		} else {
			nonCJKChars++
		}
	}
	// GPT BPE 对英文/标点/数字平均约 4 字符 = 1 token / GPT BPE averages ~4 chars per token for non-CJK
	nonCJKTokens := (nonCJKChars + 3) / 4
	return cjkCount + nonCJKTokens
}

// isCJK 判断是否为 CJK 统一汉字或日韩字符 / Check if rune is CJK/Hangul/Kana character
func isCJK(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) || // CJK Unified Ideographs
		(r >= 0x3400 && r <= 0x4DBF) || // CJK Extension A
		(r >= 0x20000 && r <= 0x2A6DF) || // CJK Extension B
		(r >= 0xAC00 && r <= 0xD7AF) || // Hangul Syllables
		(r >= 0x3040 && r <= 0x30FF) // Hiragana + Katakana
}
