// internal/document/chunker.go
package document

import (
	"fmt"
	"strings"
)

// Chunker 分块器接口 / Chunker interface
type Chunker interface {
	Chunk(content string, opts ChunkOptions) []Chunk
}

// ChunkOptions 分块配置 / Chunk options
type ChunkOptions struct {
	MaxTokens       int
	OverlapTokens   int
	ContextPrefix   bool
	DocName         string
	KeepTableIntact bool
	KeepCodeIntact  bool
}

// Chunk 分块结果 / Chunk with metadata
type Chunk struct {
	Content    string
	RawContent string
	Index      int
	Heading    string
	ChunkType  string // "text" | "table" | "code" | "list"
	PageStart  int
	TokenCount int
}

// estimateTokens 估算 token 数（CJK 感知）/ Estimate token count (CJK-aware)
func estimateTokens(s string) int {
	if len(s) == 0 {
		return 0
	}
	cjk := 0
	total := 0
	for _, r := range s {
		total++
		if r >= 0x2E80 && r <= 0x9FFF || r >= 0xF900 && r <= 0xFAFF {
			cjk++
		}
	}
	// CJK: ~1.5 chars/token; non-CJK: ~4 chars/token
	nonCJK := total - cjk
	return cjk*2/3 + nonCJK/4 + 1
}

// tokensToChars 将 token 数转为近似字符数 / Convert tokens to approximate char count
func tokensToChars(tokens int) int {
	return tokens * 3
}

// --- TextChunker ---

// TextChunker 纯文本分块器 / Plain text chunker with overlap
type TextChunker struct{}

// NewTextChunker 创建文本分块器 / Create text chunker
func NewTextChunker() *TextChunker {
	return &TextChunker{}
}

// Chunk 递归字符分块 / Recursive character splitting with overlap
func (c *TextChunker) Chunk(content string, opts ChunkOptions) []Chunk {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	if opts.MaxTokens <= 0 {
		opts.MaxTokens = 512
	}

	maxChars := tokensToChars(opts.MaxTokens)
	overlapChars := tokensToChars(opts.OverlapTokens)

	segments := recursiveSplit(content, maxChars)

	var chunks []Chunk
	for _, seg := range segments {
		raw := strings.TrimSpace(seg)
		if raw == "" {
			continue
		}
		chunks = append(chunks, Chunk{
			RawContent: raw,
			Content:    raw,
			Index:      len(chunks),
			ChunkType:  "text",
			TokenCount: estimateTokens(raw),
		})
	}

	if overlapChars > 0 && len(chunks) > 1 {
		for i := 1; i < len(chunks); i++ {
			prevRunes := []rune(chunks[i-1].RawContent)
			ol := overlapChars
			if ol > len(prevRunes) {
				ol = len(prevRunes)
			}
			overlapText := string(prevRunes[len(prevRunes)-ol:])
			chunks[i].RawContent = overlapText + "\n" + chunks[i].RawContent
			chunks[i].Content = chunks[i].RawContent
			chunks[i].TokenCount = estimateTokens(chunks[i].RawContent)
		}
	}

	return chunks
}

// recursiveSplit 递归分割文本 / Recursively split text by separators
func recursiveSplit(text string, maxChars int) []string {
	runes := []rune(text)
	if len(runes) <= maxChars {
		return []string{text}
	}

	separators := []string{"\n\n", "\n", "。", ". ", " "}
	for _, sep := range separators {
		parts := strings.Split(text, sep)
		if len(parts) <= 1 {
			continue
		}

		var result []string
		var current strings.Builder
		var currentRuneLen int
		for _, part := range parts {
			partRuneLen := len([]rune(part))
			sepRuneLen := len([]rune(sep))
			if currentRuneLen > 0 && currentRuneLen+sepRuneLen+partRuneLen > maxChars {
				result = append(result, current.String())
				current.Reset()
				currentRuneLen = 0
			}
			if currentRuneLen > 0 {
				current.WriteString(sep)
				currentRuneLen += sepRuneLen
			}
			current.WriteString(part)
			currentRuneLen += partRuneLen
		}
		if current.Len() > 0 {
			result = append(result, current.String())
		}

		var final []string
		for _, r := range result {
			if len([]rune(r)) > maxChars {
				final = append(final, recursiveSplit(r, maxChars)...)
			} else {
				final = append(final, r)
			}
		}
		return final
	}

	// 硬切兜底（rune 安全）/ Hard cut fallback (rune-safe)
	var result []string
	for len(runes) > maxChars {
		result = append(result, string(runes[:maxChars]))
		runes = runes[maxChars:]
	}
	if len(runes) > 0 {
		result = append(result, string(runes))
	}
	return result
}

// --- MarkdownChunker ---

// MarkdownChunker Markdown 结构感知分块器 / Markdown structure-aware chunker
type MarkdownChunker struct {
	textChunker *TextChunker
}

// NewMarkdownChunker 创建 Markdown 分块器 / Create markdown chunker
func NewMarkdownChunker() *MarkdownChunker {
	return &MarkdownChunker{textChunker: NewTextChunker()}
}

type section struct {
	heading   string
	content   string
	chunkType string
}

// Chunk 三层分块 / 3-layer chunking pipeline
func (c *MarkdownChunker) Chunk(content string, opts ChunkOptions) []Chunk {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}
	if opts.MaxTokens <= 0 {
		opts.MaxTokens = 512
	}

	// Layer 1: 结构切分
	sections := c.splitByStructure(content, opts)

	// Layer 2: 超长段递归切分
	maxChars := tokensToChars(opts.MaxTokens)
	var expanded []section
	for _, sec := range sections {
		if len(sec.content) <= maxChars || sec.chunkType == "table" || sec.chunkType == "code" {
			expanded = append(expanded, sec)
		} else {
			subChunks := c.textChunker.Chunk(sec.content, opts)
			for _, sc := range subChunks {
				expanded = append(expanded, section{
					heading:   sec.heading,
					content:   sc.RawContent,
					chunkType: sec.chunkType,
				})
			}
		}
	}

	// Layer 3: 上下文前缀增强
	var chunks []Chunk
	for _, sec := range expanded {
		raw := strings.TrimSpace(sec.content)
		if raw == "" {
			continue
		}

		final := raw
		if opts.ContextPrefix && opts.DocName != "" {
			prefix := fmt.Sprintf("【%s", opts.DocName)
			if sec.heading != "" {
				prefix += " > " + sec.heading
			}
			prefix += "】\n"
			final = prefix + raw
		}

		chunks = append(chunks, Chunk{
			Content:    final,
			RawContent: raw,
			Index:      len(chunks),
			Heading:    sec.heading,
			ChunkType:  sec.chunkType,
			TokenCount: estimateTokens(final),
		})
	}

	return chunks
}

// splitByStructure Layer 1: 按 Markdown 结构切分 / Split by markdown structure
func (c *MarkdownChunker) splitByStructure(content string, opts ChunkOptions) []section {
	lines := strings.Split(content, "\n")
	var sections []section
	var currentHeadings []string
	var currentLines []string
	currentType := "text"
	inCodeBlock := false
	inTable := false

	flushCurrent := func() {
		text := strings.TrimSpace(strings.Join(currentLines, "\n"))
		if text != "" {
			heading := strings.Join(currentHeadings, " > ")
			sections = append(sections, section{
				heading:   heading,
				content:   text,
				chunkType: currentType,
			})
		}
		currentLines = nil
		currentType = "text"
	}

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// 代码块检测
		if strings.HasPrefix(trimmed, "```") {
			if inCodeBlock {
				currentLines = append(currentLines, line)
				if opts.KeepCodeIntact {
					flushCurrent()
				}
				inCodeBlock = false
				continue
			}
			if opts.KeepCodeIntact {
				flushCurrent()
				currentType = "code"
			}
			inCodeBlock = true
			currentLines = append(currentLines, line)
			continue
		}
		if inCodeBlock {
			currentLines = append(currentLines, line)
			continue
		}

		// 表格检测
		if strings.HasPrefix(trimmed, "|") && strings.Contains(trimmed, "|") {
			if !inTable {
				if opts.KeepTableIntact {
					flushCurrent()
					currentType = "table"
				}
				inTable = true
			}
			currentLines = append(currentLines, line)
			continue
		}
		if inTable {
			inTable = false
			if opts.KeepTableIntact {
				flushCurrent()
			}
		}

		// 标题检测
		if strings.HasPrefix(trimmed, "#") {
			level := 0
			for _, ch := range trimmed {
				if ch == '#' {
					level++
				} else {
					break
				}
			}
			if level >= 1 && level <= 6 {
				flushCurrent()
				title := strings.TrimSpace(trimmed[level:])
				if level <= len(currentHeadings) {
					currentHeadings = currentHeadings[:level-1]
				}
				for len(currentHeadings) < level-1 {
					currentHeadings = append(currentHeadings, "")
				}
				currentHeadings = append(currentHeadings[:level-1], title)
				continue
			}
		}

		currentLines = append(currentLines, line)
	}
	flushCurrent()

	return sections
}
