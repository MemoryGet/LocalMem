// Package tokenizer 分词器接口与实现 / Tokenizer interface and implementations
package tokenizer

import (
	"bufio"
	"os"
	"strings"
)

// StopFilter 停用词过滤器 / Stop word filter
// 支持从文件加载 + 内置默认词表双层保底
type StopFilter struct {
	words map[string]bool
}

// 内置默认停用词（保底）/ Built-in default stop words (fallback)
var defaultStopWordsEN = []string{
	"the", "a", "an", "is", "are", "was", "were", "be", "been", "being",
	"have", "has", "had", "do", "does", "did", "will", "would", "could",
	"should", "may", "might", "shall", "can",
	"of", "in", "to", "for", "with", "on", "at", "by", "from", "as", "into",
	"through", "during", "it", "its", "this", "that", "these", "those",
	"and", "or", "but", "not", "no",
	"i", "me", "my", "we", "our", "you", "your", "he", "she", "they", "them",
}

var defaultStopWordsZH = []string{
	"的", "了", "在", "是", "我", "有", "和", "就", "不", "人",
	"都", "一", "个", "上", "也", "很", "到", "说", "要", "去",
	"你", "会", "着", "没", "看", "好", "自", "这", "他", "她",
	"它", "被", "把", "那", "而", "所", "与", "给", "让", "用",
}

// NewStopFilter 创建停用词过滤器 / Create stop word filter
// paths 为空时使用内置默认词表；不为空时从文件加载并合并内置词表
func NewStopFilter(paths ...string) *StopFilter {
	sf := &StopFilter{words: make(map[string]bool)}

	// 先加载内置默认词 / Load built-in defaults first
	for _, w := range defaultStopWordsEN {
		sf.words[w] = true
	}
	for _, w := range defaultStopWordsZH {
		sf.words[w] = true
	}

	// 从文件加载（合并，不覆盖）/ Load from files (merge)
	for _, path := range paths {
		if path == "" {
			continue
		}
		if err := sf.loadFile(path); err != nil {
			// 文件加载失败不影响服务，保底词表已加载
			continue
		}
	}

	return sf
}

// loadFile 从文本文件加载停用词（每行一个词）/ Load stop words from text file (one per line)
func (sf *StopFilter) loadFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		word := strings.TrimSpace(scanner.Text())
		if word == "" || strings.HasPrefix(word, "#") {
			continue // 跳过空行和注释
		}
		sf.words[strings.ToLower(word)] = true
	}
	return scanner.Err()
}

// IsStopWord 判断是否为停用词 / Check if a word is a stop word
func (sf *StopFilter) IsStopWord(word string) bool {
	return sf.words[strings.ToLower(word)]
}

// Count 返回停用词数量 / Return stop word count
func (sf *StopFilter) Count() int {
	return len(sf.words)
}
