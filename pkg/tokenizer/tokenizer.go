// Package tokenizer 分词器接口与实现 / Tokenizer interface and implementations
// 可拔插设计，不影响主功能 / Pluggable design, does not affect core functionality
package tokenizer

import (
	"context"
	"strings"
)

// Tokenizer 分词器接口 / Tokenizer interface
type Tokenizer interface {
	// Tokenize 对文本分词，返回空格分隔的词序列 / Tokenize text, return space-separated token sequence
	Tokenize(ctx context.Context, text string) (string, error)

	// Name 分词器名称 / Tokenizer name
	Name() string
}

// JoinTokens 将分词结果合并为空格分隔字符串 / Join tokens into space-separated string
func JoinTokens(tokens []string) string {
	return strings.Join(tokens, " ")
}
