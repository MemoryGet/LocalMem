// Package hashutil 内容哈希工具 / Content hashing utilities for deduplication
package hashutil

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// whitespaceRe 预编译空白字符正则 / Pre-compiled whitespace regex
var whitespaceRe = regexp.MustCompile(`\s+`)

// NormalizeForHash 归一化文本用于哈希 / Normalize text for hashing
// 去除首尾空白、转小写、折叠连续空白为单个空格
func NormalizeForHash(content string) string {
	s := strings.TrimSpace(content)
	s = strings.ToLower(s)
	s = whitespaceRe.ReplaceAllString(s, " ")
	return s
}

// ContentHash 计算内容的 SHA-256 哈希 / Compute SHA-256 hash of normalized content
func ContentHash(content string) string {
	h := sha256.Sum256([]byte(NormalizeForHash(content)))
	return hex.EncodeToString(h[:])
}
