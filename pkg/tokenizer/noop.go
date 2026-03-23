package tokenizer

import "context"

// NoopTokenizer 透传分词器 / Pass-through tokenizer
// 不做任何分词处理，原样返回。用于禁用分词功能时。
type NoopTokenizer struct{}

// NewNoopTokenizer 创建透传分词器 / Create a no-op tokenizer
func NewNoopTokenizer() *NoopTokenizer {
	return &NoopTokenizer{}
}

// Tokenize 原样返回文本 / Return text as-is
func (t *NoopTokenizer) Tokenize(_ context.Context, text string) (string, error) {
	return text, nil
}

// Name 返回分词器名称 / Return tokenizer name
func (t *NoopTokenizer) Name() string {
	return "noop"
}
