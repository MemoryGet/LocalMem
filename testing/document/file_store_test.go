package document_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"iclude/internal/document"
)

func TestLocalFileStore_SaveAndGet(t *testing.T) {
	tmpDir := t.TempDir()
	fs := document.NewLocalFileStore(tmpDir)
	ctx := context.Background()

	content := []byte("hello world")
	path, err := fs.Save(ctx, "doc123", "test.pdf", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("saved file does not exist")
	}

	expectedDir := filepath.Join(tmpDir, "doc123")
	if filepath.Dir(path) != expectedDir {
		t.Errorf("expected dir %s, got %s", expectedDir, filepath.Dir(path))
	}

	rc, err := fs.Get(ctx, path)
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}
	defer rc.Close()

	got, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("ReadAll failed: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Errorf("content mismatch: got %q, want %q", got, content)
	}
}

func TestLocalFileStore_Delete(t *testing.T) {
	tmpDir := t.TempDir()
	fs := document.NewLocalFileStore(tmpDir)
	ctx := context.Background()

	path, err := fs.Save(ctx, "doc456", "test.txt", bytes.NewReader([]byte("data")))
	if err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	if err := fs.Delete(ctx, filepath.Dir(path)); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	if _, err := os.Stat(filepath.Dir(path)); !os.IsNotExist(err) {
		t.Error("directory should be removed after Delete")
	}
}

func TestLocalFileStore_GetNotFound(t *testing.T) {
	tmpDir := t.TempDir()
	fs := document.NewLocalFileStore(tmpDir)
	ctx := context.Background()

	_, err := fs.Get(ctx, filepath.Join(tmpDir, "nonexist", "file.txt"))
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}
