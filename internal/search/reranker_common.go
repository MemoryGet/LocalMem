package search

import (
	"regexp"
	"strings"
	"unicode"
)

var rerankSplitPattern = regexp.MustCompile(`[^\p{L}\p{N}\p{Han}]+`)

func splitRerankTerms(text string) []string {
	normalized := normalizeRerankText(text)
	if normalized == "" {
		return nil
	}
	return strings.Fields(normalized)
}

func normalizeRerankText(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	if text == "" {
		return ""
	}
	text = rerankSplitPattern.ReplaceAllString(text, " ")
	return strings.Join(strings.Fields(text), " ")
}

func isHanOnly(runes []rune) bool {
	if len(runes) == 0 {
		return false
	}
	for _, r := range runes {
		if !unicode.Is(unicode.Han, r) {
			return false
		}
	}
	return true
}
