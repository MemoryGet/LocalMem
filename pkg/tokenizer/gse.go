package tokenizer

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-ego/gse"
)

// GseTokenizer Go 原生分词器 / Go-native tokenizer based on gse
// 无外部依赖，进程内分词，支持自定义词典和停用词过滤
type GseTokenizer struct {
	seg        gse.Segmenter
	stopFilter *StopFilter
}

// NewGseTokenizer 创建 gse 分词器 / Create a gse tokenizer
// dictPath: 自定义词典路径，空串使用内置词典 / Custom dict path, empty for built-in
// stopwordFiles: 停用词文件路径 / Stopword file paths
func NewGseTokenizer(dictPath string, stopwordFiles []string) (*GseTokenizer, error) {
	var seg gse.Segmenter
	var err error
	if dictPath != "" {
		err = seg.LoadDict(dictPath)
	} else {
		err = seg.LoadDict()
	}
	if err != nil {
		return nil, fmt.Errorf("failed to load gse dictionary: %w", err)
	}

	sf := NewStopFilter(stopwordFiles...)
	return &GseTokenizer{seg: seg, stopFilter: sf}, nil
}

// Tokenize 分词并过滤停用词 / Tokenize text and filter stop words
func (t *GseTokenizer) Tokenize(_ context.Context, text string) (string, error) {
	if text == "" {
		return "", nil
	}

	segments := t.seg.Cut(text, true) // 精确模式 / Exact mode

	var filtered []string
	for _, s := range segments {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if t.stopFilter.IsStopWord(s) {
			continue
		}
		filtered = append(filtered, s)
	}

	return JoinTokens(filtered), nil
}

// Name 返回分词器名称 / Return tokenizer name
func (t *GseTokenizer) Name() string {
	return "gse"
}
