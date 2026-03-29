package document

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// FileStore 文件存储接口 / File storage interface
type FileStore interface {
	// Save 保存文件 / Save file, returns absolute path
	Save(ctx context.Context, docID string, filename string, reader io.Reader) (string, error)
	// Get 读取文件 / Read file by path
	Get(ctx context.Context, path string) (io.ReadCloser, error)
	// Delete 删除文件目录 / Delete file or directory
	Delete(ctx context.Context, path string) error
}

// LocalFileStore 本地文件存储 / Local filesystem storage
type LocalFileStore struct {
	baseDir string
}

// NewLocalFileStore 创建本地文件存储 / Create local file store
func NewLocalFileStore(baseDir string) *LocalFileStore {
	return &LocalFileStore{baseDir: baseDir}
}

// Save 保存文件到本地 / Save file to local filesystem
func (s *LocalFileStore) Save(ctx context.Context, docID string, filename string, reader io.Reader) (string, error) {
	dir := filepath.Join(s.baseDir, docID)
	if err := s.validatePath(dir); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create upload dir: %w", err)
	}

	destPath := filepath.Join(dir, filepath.Base(filename))
	f, err := os.Create(destPath)
	if err != nil {
		return "", fmt.Errorf("failed to create file: %w", err)
	}
	defer f.Close()

	if _, err := io.Copy(f, reader); err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	return destPath, nil
}

// validatePath 校验路径在 baseDir 范围内 / Validate path is within baseDir
func (s *LocalFileStore) validatePath(path string) error {
	absBase, err := filepath.Abs(s.baseDir)
	if err != nil {
		return fmt.Errorf("failed to resolve base dir: %w", err)
	}
	absPath, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return fmt.Errorf("failed to resolve path: %w", err)
	}
	if !strings.HasPrefix(absPath, absBase+string(os.PathSeparator)) && absPath != absBase {
		return fmt.Errorf("path traversal detected")
	}
	return nil
}

// Get 读取本地文件 / Read file from local filesystem
func (s *LocalFileStore) Get(ctx context.Context, path string) (io.ReadCloser, error) {
	if err := s.validatePath(path); err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}
	return f, nil
}

// Delete 删除本地文件或目录 / Delete local file or directory
func (s *LocalFileStore) Delete(ctx context.Context, path string) error {
	if err := s.validatePath(path); err != nil {
		return err
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("failed to delete path: %w", err)
	}
	return nil
}
