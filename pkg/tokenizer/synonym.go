package tokenizer

import (
	"bufio"
	"os"
	"strings"
)

// SynonymDict 同义词词典 / Synonym dictionary
// 格式：key=syn1 syn2 syn3（双向查找）/ Format: key=syn1 syn2 syn3 (bidirectional lookup)
type SynonymDict struct {
	mapping map[string][]string
}

// NewSynonymDict 从文件加载同义词词典 / Load synonym dictionary from files
func NewSynonymDict(paths ...string) *SynonymDict {
	d := &SynonymDict{mapping: make(map[string][]string)}
	for _, p := range paths {
		if p == "" {
			continue
		}
		_ = d.loadFile(p)
	}
	return d
}

func (d *SynonymDict) loadFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		syns := strings.Fields(parts[1])
		if key == "" || len(syns) == 0 {
			continue
		}
		// 双向映射 / Bidirectional mapping
		allWords := append([]string{key}, syns...)
		for _, w := range allWords {
			w = strings.ToLower(w)
			for _, other := range allWords {
				other = strings.ToLower(other)
				if w != other && !contains(d.mapping[w], other) {
					d.mapping[w] = append(d.mapping[w], other)
				}
			}
		}
	}
	return scanner.Err()
}

// Expand 返回词的所有同义词（不包含自身）/ Return synonyms for a word (excluding itself)
func (d *SynonymDict) Expand(word string) []string {
	return d.mapping[strings.ToLower(word)]
}

// Count 返回词典条目数 / Return entry count
func (d *SynonymDict) Count() int {
	return len(d.mapping)
}

func contains(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}
