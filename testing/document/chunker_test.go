package document_test

import (
	"strings"
	"testing"

	"iclude/internal/document"
)

func TestTextChunker_BasicSplit(t *testing.T) {
	content := strings.Repeat("Hello world. ", 200)
	chunker := document.NewTextChunker()
	opts := document.ChunkOptions{MaxTokens: 100, OverlapTokens: 10}

	chunks := chunker.Chunk(content, opts)
	if len(chunks) < 2 {
		t.Errorf("expected multiple chunks, got %d", len(chunks))
	}
	for i, c := range chunks {
		if c.Index != i {
			t.Errorf("chunk %d has wrong index %d", i, c.Index)
		}
		if c.ChunkType != "text" {
			t.Errorf("expected chunk_type text, got %s", c.ChunkType)
		}
	}
}

func TestTextChunker_EmptyContent(t *testing.T) {
	chunker := document.NewTextChunker()
	opts := document.ChunkOptions{MaxTokens: 100}
	chunks := chunker.Chunk("", opts)
	if len(chunks) != 0 {
		t.Errorf("expected 0 chunks for empty content, got %d", len(chunks))
	}
}

func TestMarkdownChunker_HeadingSplit(t *testing.T) {
	content := "# Chapter 1\n\nIntroduction paragraph.\n\n## Section 1.1\n\nDetail about section one.\n\n## Section 1.2\n\nDetail about section two.\n\n# Chapter 2\n\nAnother chapter."

	chunker := document.NewMarkdownChunker()
	opts := document.ChunkOptions{
		MaxTokens:       500,
		KeepTableIntact: true,
		KeepCodeIntact:  true,
	}
	chunks := chunker.Chunk(content, opts)

	if len(chunks) < 3 {
		t.Errorf("expected at least 3 chunks, got %d", len(chunks))
	}

	hasHeading := false
	for _, c := range chunks {
		if c.Heading != "" {
			hasHeading = true
		}
	}
	if !hasHeading {
		t.Error("expected at least one chunk with heading chain")
	}
}

func TestMarkdownChunker_TableIntact(t *testing.T) {
	content := "# Data\n\nSome text before table.\n\n| Col1 | Col2 |\n|------|------|\n| A    | B    |\n| C    | D    |\n\nSome text after table."

	chunker := document.NewMarkdownChunker()
	opts := document.ChunkOptions{MaxTokens: 500, KeepTableIntact: true}
	chunks := chunker.Chunk(content, opts)

	foundTable := false
	for _, c := range chunks {
		if c.ChunkType == "table" {
			foundTable = true
			if !strings.Contains(c.RawContent, "| Col1") {
				t.Error("table chunk should contain table header")
			}
			if !strings.Contains(c.RawContent, "| C") {
				t.Error("table chunk should contain all rows")
			}
		}
	}
	if !foundTable {
		t.Error("expected a table chunk")
	}
}

func TestMarkdownChunker_CodeBlockIntact(t *testing.T) {
	content := "# Code\n\nSome intro.\n\n```go\nfunc main() {\n\tfmt.Println(\"hello\")\n}\n```\n\nAfter code."

	chunker := document.NewMarkdownChunker()
	opts := document.ChunkOptions{MaxTokens: 500, KeepCodeIntact: true}
	chunks := chunker.Chunk(content, opts)

	foundCode := false
	for _, c := range chunks {
		if c.ChunkType == "code" {
			foundCode = true
			if !strings.Contains(c.RawContent, "func main()") {
				t.Error("code chunk should contain full code block")
			}
		}
	}
	if !foundCode {
		t.Error("expected a code chunk")
	}
}

func TestMarkdownChunker_ContextPrefix(t *testing.T) {
	content := "# Overview\n\nSome content here."

	chunker := document.NewMarkdownChunker()
	opts := document.ChunkOptions{
		MaxTokens:     500,
		ContextPrefix: true,
		DocName:       "架构设计.pdf",
	}
	chunks := chunker.Chunk(content, opts)

	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}
	if !strings.HasPrefix(chunks[0].Content, "【架构设计.pdf") {
		t.Errorf("expected context prefix, got %q", chunks[0].Content[:40])
	}
}
