package document_test

import (
	"context"
	"errors"
	"testing"

	"iclude/internal/document"
)

type mockParser struct {
	result *document.ParseResult
	err    error
	types  []string
}

func (m *mockParser) Parse(ctx context.Context, filePath string, docType string) (*document.ParseResult, error) {
	return m.result, m.err
}

func (m *mockParser) Supports(docType string) bool {
	for _, t := range m.types {
		if t == docType {
			return true
		}
	}
	return false
}

func TestParseRouter_PrimarySuccess(t *testing.T) {
	primary := &mockParser{
		result: &document.ParseResult{Content: "# Hello", Format: "markdown"},
		types:  []string{"pdf"},
	}
	fallback := &mockParser{
		result: &document.ParseResult{Content: "Hello", Format: "plaintext"},
		types:  []string{"pdf"},
	}
	router := document.NewParseRouter(primary, fallback)

	result, err := router.Parse(context.Background(), "/tmp/test.pdf", "pdf")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Format != "markdown" {
		t.Errorf("expected markdown from primary, got %s", result.Format)
	}
}

func TestParseRouter_FallbackOnPrimaryFailure(t *testing.T) {
	primary := &mockParser{
		err:   errors.New("docling unavailable"),
		types: []string{"pdf"},
	}
	fallback := &mockParser{
		result: &document.ParseResult{Content: "Hello plain", Format: "plaintext"},
		types:  []string{"pdf"},
	}
	router := document.NewParseRouter(primary, fallback)

	result, err := router.Parse(context.Background(), "/tmp/test.pdf", "pdf")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Format != "plaintext" {
		t.Errorf("expected plaintext from fallback, got %s", result.Format)
	}
}

func TestParseRouter_BothFail(t *testing.T) {
	primary := &mockParser{err: errors.New("docling down"), types: []string{"pdf"}}
	fallback := &mockParser{err: errors.New("tika down"), types: []string{"pdf"}}
	router := document.NewParseRouter(primary, fallback)

	_, err := router.Parse(context.Background(), "/tmp/test.pdf", "pdf")
	if err == nil {
		t.Fatal("expected error when both parsers fail")
	}
}

func TestParseRouter_PrimaryUnsupported_FallbackUsed(t *testing.T) {
	primary := &mockParser{types: []string{"pdf"}}
	fallback := &mockParser{
		result: &document.ParseResult{Content: "doc text", Format: "plaintext"},
		types:  []string{"docx"},
	}
	router := document.NewParseRouter(primary, fallback)

	result, err := router.Parse(context.Background(), "/tmp/test.docx", "docx")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Content != "doc text" {
		t.Errorf("expected fallback content, got %q", result.Content)
	}
}
