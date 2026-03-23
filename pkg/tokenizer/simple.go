package tokenizer

import (
	"context"
	"strings"
	"unicode"
)

// SimpleTokenizer 简单分词器（回退方案）/ Simple tokenizer (fallback)
// 按 Unicode 类别拆分：CJK 逐字、非 CJK 按空白/标点分词
// 无外部依赖，保证基本可用性
type SimpleTokenizer struct{}

// NewSimpleTokenizer 创建简单分词器 / Create a simple tokenizer
func NewSimpleTokenizer() *SimpleTokenizer {
	return &SimpleTokenizer{}
}

// Tokenize 简单分词 / Simple tokenization
// CJK 字符逐字拆分，英文按空白分词，过滤标点
func (t *SimpleTokenizer) Tokenize(_ context.Context, text string) (string, error) {
	if text == "" {
		return "", nil
	}

	var tokens []string
	var wordBuf strings.Builder

	flushWord := func() {
		if wordBuf.Len() > 0 {
			tokens = append(tokens, wordBuf.String())
			wordBuf.Reset()
		}
	}

	for _, r := range text {
		if isCJK(r) {
			// CJK 字符：先输出累积的非 CJK 词，再单独输出
			flushWord()
			tokens = append(tokens, string(r))
		} else if unicode.IsLetter(r) || unicode.IsDigit(r) {
			// 字母/数字：累积为一个词
			wordBuf.WriteRune(r)
		} else {
			// 空白/标点：分隔
			flushWord()
		}
	}
	flushWord()

	return JoinTokens(tokens), nil
}

// Name 返回分词器名称 / Return tokenizer name
func (t *SimpleTokenizer) Name() string {
	return "simple"
}

// isCJK 判断是否为 CJK 字符 / Check if rune is CJK
func isCJK(r rune) bool {
	return unicode.Is(unicode.Han, r) ||
		unicode.Is(unicode.Hangul, r) ||
		unicode.Is(unicode.Hiragana, r) ||
		unicode.Is(unicode.Katakana, r)
}
