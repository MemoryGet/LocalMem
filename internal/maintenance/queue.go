// Package maintenance 后台维护任务（实体抽取/向量化回填等）/ Background maintenance tasks (entity extraction / vectorization backfill).
package maintenance

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// queryIDs 执行单列 ID 查询并返回结果 / Run a single-column id query and return all rows.
func queryIDs(ctx context.Context, db *sql.DB, query string, args ...any) ([]string, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query ids: %w", err)
	}
	defer rows.Close()

	ids := make([]string, 0)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan id: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate ids: %w", err)
	}
	return ids, nil
}

// loadContents 通过 IN 子句加载 ID → content 映射到 dst / Load id → content via IN-clause query into dst map.
func loadContents(ctx context.Context, db *sql.DB, ids []string, dst map[string]string) error {
	if len(ids) == 0 {
		return nil
	}
	placeholders := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		placeholders[i] = "?"
		args[i] = id
	}
	query := fmt.Sprintf("SELECT id, content FROM memories WHERE id IN (%s)", strings.Join(placeholders, ","))

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("load contents: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var id, content string
		if err := rows.Scan(&id, &content); err != nil {
			return fmt.Errorf("scan content: %w", err)
		}
		dst[id] = content
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate content rows: %w", err)
	}
	return nil
}

// QueueFile 检查点队列文件管理器 / Checkpoint queue file manager.
//
// 文件格式（每行一个 ID，# 开头为注释行）/ File format (one ID per line, lines starting with `#` are comments):
//
//	# Backfill Queue — N remaining
//	# Each line is a memory ID. Delete file to skip backfill.
//
//	uuid1
//	uuid2
//
// Load 返回 nil 表示文件不存在（与空切片不同）/ Load returns nil when file is absent (distinct from empty slice).
// Save 在 ids 为空时删除文件 / Save deletes the file when ids is empty.
type QueueFile struct {
	Path string
}

// NewQueueFile 在指定目录创建队列文件管理器 / Create a queue file manager under the given directory.
func NewQueueFile(dir, name string) *QueueFile {
	return &QueueFile{Path: filepath.Join(dir, name)}
}

// Load 读取队列文件中的 ID 列表 / Load the list of IDs from the queue file.
// 文件不存在时返回 (nil, nil)；空文件返回 ([]string{}, nil) / Returns (nil, nil) when the file is absent; ([]string{}, nil) when empty.
func (q *QueueFile) Load() ([]string, error) {
	data, err := os.ReadFile(q.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read queue file %s: %w", q.Path, err)
	}

	ids := make([]string, 0)
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		ids = append(ids, line)
	}
	return ids, nil
}

// Save 将 ID 列表写入队列文件；ids 为空时删除文件 / Persist the ID list to the queue file; deletes the file when ids is empty.
func (q *QueueFile) Save(ids []string) error {
	if len(ids) == 0 {
		if err := os.Remove(q.Path); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove queue file %s: %w", q.Path, err)
		}
		return nil
	}

	if err := os.MkdirAll(filepath.Dir(q.Path), 0o755); err != nil {
		return fmt.Errorf("ensure queue dir: %w", err)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "# Backfill Queue — %d remaining\n", len(ids))
	sb.WriteString("# Each line is a memory ID. Delete file to skip backfill.\n\n")
	for _, id := range ids {
		sb.WriteString(id)
		sb.WriteByte('\n')
	}

	if err := os.WriteFile(q.Path, []byte(sb.String()), 0o644); err != nil {
		return fmt.Errorf("write queue file %s: %w", q.Path, err)
	}
	return nil
}

// Exists 判断队列文件是否存在 / Report whether the queue file currently exists.
func (q *QueueFile) Exists() bool {
	_, err := os.Stat(q.Path)
	return err == nil
}
