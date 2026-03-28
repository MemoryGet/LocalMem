package store_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"iclude/internal/model"
	"iclude/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestStore(t *testing.T) (store.MemoryStore, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	s, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, nil)
	require.NoError(t, err)

	err = s.Init(context.Background())
	require.NoError(t, err)

	return s, func() {
		s.Close()
		os.RemoveAll(dir)
	}
}

func TestSQLiteMemoryStore_Create(t *testing.T) {
	tests := []struct {
		name    string
		mem     *model.Memory
		wantErr bool
		errIs   error
	}{
		{
			name:    "happy path",
			mem:     &model.Memory{Content: "hello world", TeamID: "team1"},
			wantErr: false,
		},
		{
			name:    "empty content",
			mem:     &model.Memory{Content: ""},
			wantErr: true,
			errIs:   model.ErrInvalidInput,
		},
		{
			name:    "with metadata",
			mem:     &model.Memory{Content: "test", Metadata: map[string]any{"key": "val"}},
			wantErr: false,
		},
		{
			name:    "nil metadata",
			mem:     &model.Memory{Content: "test"},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, cleanup := setupTestStore(t)
			defer cleanup()

			err := s.Create(context.Background(), tt.mem)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errIs != nil {
					assert.ErrorIs(t, err, tt.errIs)
				}
				return
			}
			require.NoError(t, err)
			assert.NotEmpty(t, tt.mem.ID)
			assert.False(t, tt.mem.CreatedAt.IsZero())
			assert.True(t, tt.mem.IsLatest)
		})
	}
}

func TestSQLiteMemoryStore_Get(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(s store.MemoryStore) string // returns ID
		id      string
		wantErr bool
		errIs   error
	}{
		{
			name: "existing memory",
			setup: func(s store.MemoryStore) string {
				mem := &model.Memory{Content: "test content"}
				s.Create(context.Background(), mem)
				return mem.ID
			},
			wantErr: false,
		},
		{
			name:    "not found",
			setup:   func(s store.MemoryStore) string { return "nonexistent-id" },
			wantErr: true,
			errIs:   model.ErrMemoryNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, cleanup := setupTestStore(t)
			defer cleanup()

			id := tt.setup(s)
			mem, err := s.Get(context.Background(), id)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errIs != nil {
					assert.ErrorIs(t, err, tt.errIs)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, id, mem.ID)
			assert.Equal(t, 0, mem.AccessCount) // Get 为纯读，不递增访问计数
		})
	}
}

func TestSQLiteMemoryStore_Update(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(s store.MemoryStore) *model.Memory
		mutate  func(mem *model.Memory)
		wantErr bool
		errIs   error
	}{
		{
			name: "update content",
			setup: func(s store.MemoryStore) *model.Memory {
				mem := &model.Memory{Content: "original"}
				s.Create(context.Background(), mem)
				return mem
			},
			mutate: func(mem *model.Memory) {
				mem.Content = "updated"
			},
			wantErr: false,
		},
		{
			name: "not found",
			setup: func(s store.MemoryStore) *model.Memory {
				return &model.Memory{ID: "nonexistent", Content: "x"}
			},
			mutate:  func(mem *model.Memory) {},
			wantErr: true,
			errIs:   model.ErrMemoryNotFound,
		},
		{
			name: "empty id",
			setup: func(s store.MemoryStore) *model.Memory {
				return &model.Memory{ID: "", Content: "x"}
			},
			mutate:  func(mem *model.Memory) {},
			wantErr: true,
			errIs:   model.ErrInvalidInput,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, cleanup := setupTestStore(t)
			defer cleanup()

			mem := tt.setup(s)
			tt.mutate(mem)
			err := s.Update(context.Background(), mem)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errIs != nil {
					assert.ErrorIs(t, err, tt.errIs)
				}
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestSQLiteMemoryStore_Delete(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(s store.MemoryStore) string
		wantErr bool
		errIs   error
	}{
		{
			name: "delete existing",
			setup: func(s store.MemoryStore) string {
				mem := &model.Memory{Content: "to delete"}
				s.Create(context.Background(), mem)
				return mem.ID
			},
			wantErr: false,
		},
		{
			name:    "delete nonexistent",
			setup:   func(s store.MemoryStore) string { return "no-such-id" },
			wantErr: true,
			errIs:   model.ErrMemoryNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, cleanup := setupTestStore(t)
			defer cleanup()

			id := tt.setup(s)
			err := s.Delete(context.Background(), id)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errIs != nil {
					assert.ErrorIs(t, err, tt.errIs)
				}
				return
			}
			require.NoError(t, err)

			// 确认已删除
			_, err = s.Get(context.Background(), id)
			assert.ErrorIs(t, err, model.ErrMemoryNotFound)
		})
	}
}

func TestSQLiteMemoryStore_List(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(s store.MemoryStore)
		identity *model.Identity
		offset   int
		limit    int
		want     int
	}{
		{
			name: "list all",
			setup: func(s store.MemoryStore) {
				for i := 0; i < 5; i++ {
					s.Create(context.Background(), &model.Memory{Content: "item"})
				}
			},
			identity: &model.Identity{TeamID: "", OwnerID: model.SystemOwnerID},
			limit:    20,
			want:     5,
		},
		{
			name: "filter by team",
			setup: func(s store.MemoryStore) {
				s.Create(context.Background(), &model.Memory{Content: "a", TeamID: "t1"})
				s.Create(context.Background(), &model.Memory{Content: "b", TeamID: "t2"})
				s.Create(context.Background(), &model.Memory{Content: "c", TeamID: "t1"})
			},
			identity: &model.Identity{TeamID: "t1", OwnerID: model.SystemOwnerID},
			limit:    20,
			want:     2,
		},
		{
			name:     "empty store",
			setup:    func(s store.MemoryStore) {},
			identity: &model.Identity{TeamID: "", OwnerID: model.SystemOwnerID},
			limit:    20,
			want:     0,
		},
		{
			name: "pagination",
			setup: func(s store.MemoryStore) {
				for i := 0; i < 10; i++ {
					s.Create(context.Background(), &model.Memory{Content: "item"})
				}
			},
			identity: &model.Identity{TeamID: "", OwnerID: model.SystemOwnerID},
			offset:   3,
			limit:    5,
			want:     5,
		},
		{
			name: "default limit when zero",
			setup: func(s store.MemoryStore) {
				s.Create(context.Background(), &model.Memory{Content: "item"})
			},
			identity: &model.Identity{TeamID: "", OwnerID: model.SystemOwnerID},
			limit:    0,
			want:     1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, cleanup := setupTestStore(t)
			defer cleanup()

			tt.setup(s)
			memories, err := s.List(context.Background(), tt.identity, tt.offset, tt.limit)
			require.NoError(t, err)
			assert.Len(t, memories, tt.want)
		})
	}
}

func TestCreate_WithRetentionTier(t *testing.T) {
	// 创建含 RetentionTier 的记忆并验证正确存储和检索 / Create memory with RetentionTier and verify storage
	s, cleanup := setupTestStore(t)
	defer cleanup()

	mem := &model.Memory{
		Content:       "permanent knowledge",
		RetentionTier: "permanent",
	}
	err := s.Create(context.Background(), mem)
	require.NoError(t, err)
	assert.NotEmpty(t, mem.ID)

	got, err := s.Get(context.Background(), mem.ID)
	require.NoError(t, err)
	assert.Equal(t, "permanent", got.RetentionTier)
}

func TestCreate_DefaultRetentionTier(t *testing.T) {
	// 创建不含 RetentionTier 的记忆，验证默认值为 standard / Memory without RetentionTier defaults to "standard"
	s, cleanup := setupTestStore(t)
	defer cleanup()

	mem := &model.Memory{Content: "default tier memory"}
	err := s.Create(context.Background(), mem)
	require.NoError(t, err)

	got, err := s.Get(context.Background(), mem.ID)
	require.NoError(t, err)
	assert.Equal(t, "standard", got.RetentionTier)
}

func TestCleanupExpired(t *testing.T) {
	// 创建过期记忆，调用 CleanupExpired，验证被软删除 / Create expired memory, call CleanupExpired, verify soft-deleted
	s, cleanup := setupTestStore(t)
	defer cleanup()

	past := time.Now().UTC().Add(-1 * time.Hour)
	mem := &model.Memory{
		Content:   "ephemeral content",
		ExpiresAt: &past,
	}
	err := s.Create(context.Background(), mem)
	require.NoError(t, err)

	count, err := s.CleanupExpired(context.Background())
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 1)

	// 记忆应已被软删除，Get 返回 ErrMemoryNotFound
	_, err = s.Get(context.Background(), mem.ID)
	assert.ErrorIs(t, err, model.ErrMemoryNotFound)
}

func TestPurgeDeleted(t *testing.T) {
	// 软删除记忆后，以 0 duration 调用 PurgeDeleted，验证记忆被硬删除 / Soft delete then PurgeDeleted(0), verify hard delete
	s, cleanup := setupTestStore(t)
	defer cleanup()

	mem := &model.Memory{Content: "to be purged"}
	err := s.Create(context.Background(), mem)
	require.NoError(t, err)

	// 先软删除
	err = s.SoftDelete(context.Background(), mem.ID)
	require.NoError(t, err)

	// 以 0 持续时间清除（所有软删除记录均在截止时间之前）
	count, err := s.PurgeDeleted(context.Background(), 0)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, count, 1)

	// 硬删除后，Delete 应返回 ErrMemoryNotFound
	err = s.Delete(context.Background(), mem.ID)
	assert.ErrorIs(t, err, model.ErrMemoryNotFound)
}

func TestListByContextOrdered(t *testing.T) {
	// 在同一 context 下创建不同 turn_number 的记忆，验证按 turn_number ASC 排序 / Verify memories ordered by turn_number ASC
	s, cleanup := setupTestStore(t)
	defer cleanup()

	contextID := "ctx-order-test"

	// 按乱序插入 turn_number，期望结果是升序
	turns := []struct {
		turn    int
		content string
	}{
		{3, "third turn"},
		{1, "first turn"},
		{2, "second turn"},
	}

	for _, tc := range turns {
		mem := &model.Memory{
			Content:    tc.content,
			ContextID:  contextID,
			TurnNumber: tc.turn,
		}
		err := s.Create(context.Background(), mem)
		require.NoError(t, err)
	}

	memories, err := s.ListByContextOrdered(context.Background(), contextID, &model.Identity{TeamID: "", OwnerID: model.SystemOwnerID}, 0, 10)
	require.NoError(t, err)
	require.Len(t, memories, 3)

	// 验证按 turn_number 升序排列
	assert.Equal(t, 1, memories[0].TurnNumber)
	assert.Equal(t, 2, memories[1].TurnNumber)
	assert.Equal(t, 3, memories[2].TurnNumber)
}

func TestSQLiteMemoryStore_SearchText(t *testing.T) {
	tests := []struct {
		name    string
		setup   func(s store.MemoryStore)
		query    string
		identity *model.Identity
		limit    int
		wantMin  int
		wantErr  bool
		errIs    error
	}{
		{
			name: "find matching",
			setup: func(s store.MemoryStore) {
				s.Create(context.Background(), &model.Memory{Content: "Go programming language"})
				s.Create(context.Background(), &model.Memory{Content: "Python scripting language"})
				s.Create(context.Background(), &model.Memory{Content: "Rust systems programming"})
			},
			query:    "programming",
			identity: &model.Identity{TeamID: "", OwnerID: model.SystemOwnerID},
			limit:    10,
			wantMin:  2,
		},
		{
			name:     "empty query",
			setup:    func(s store.MemoryStore) {},
			query:    "",
			identity: &model.Identity{TeamID: "", OwnerID: model.SystemOwnerID},
			limit:    10,
			wantErr:  true,
			errIs:    model.ErrInvalidInput,
		},
		{
			name: "no matches",
			setup: func(s store.MemoryStore) {
				s.Create(context.Background(), &model.Memory{Content: "hello world"})
			},
			query:    "nonexistent_term_xyz",
			identity: &model.Identity{TeamID: "", OwnerID: model.SystemOwnerID},
			limit:    10,
			wantMin:  0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, cleanup := setupTestStore(t)
			defer cleanup()

			tt.setup(s)
			results, err := s.SearchText(context.Background(), tt.query, tt.identity, tt.limit)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errIs != nil {
					assert.ErrorIs(t, err, tt.errIs)
				}
				return
			}
			require.NoError(t, err)
			assert.GreaterOrEqual(t, len(results), tt.wantMin)
			for _, r := range results {
				assert.Equal(t, "sqlite", r.Source)
				assert.Greater(t, r.Score, float64(0))
			}
		})
	}
}

func TestListMissingAbstract(t *testing.T) {
	tests := []struct {
		name      string
		setup     func(s store.MemoryStore) []string // returns IDs of memories without abstract
		limit     int
		wantCount int
	}{
		{
			name: "returns memories with empty abstract",
			setup: func(s store.MemoryStore) []string {
				mem1 := &model.Memory{Content: "no abstract here"}
				mem2 := &model.Memory{Content: "also no abstract"}
				require.NoError(t, s.Create(context.Background(), mem1))
				require.NoError(t, s.Create(context.Background(), mem2))
				return []string{mem1.ID, mem2.ID}
			},
			limit:     10,
			wantCount: 2,
		},
		{
			name: "does not return memories with abstract set",
			setup: func(s store.MemoryStore) []string {
				memWithAbstract := &model.Memory{Content: "has abstract", Abstract: "this is an abstract"}
				memNoAbstract := &model.Memory{Content: "no abstract"}
				require.NoError(t, s.Create(context.Background(), memWithAbstract))
				require.NoError(t, s.Create(context.Background(), memNoAbstract))
				return []string{memNoAbstract.ID}
			},
			limit:     10,
			wantCount: 1,
		},
		{
			name: "does not return soft-deleted memories",
			setup: func(s store.MemoryStore) []string {
				memDeleted := &model.Memory{Content: "soft deleted no abstract"}
				memActive := &model.Memory{Content: "active no abstract"}
				require.NoError(t, s.Create(context.Background(), memDeleted))
				require.NoError(t, s.Create(context.Background(), memActive))
				require.NoError(t, s.SoftDelete(context.Background(), memDeleted.ID))
				return []string{memActive.ID}
			},
			limit:     10,
			wantCount: 1,
		},
		{
			name: "respects limit parameter",
			setup: func(s store.MemoryStore) []string {
				ids := make([]string, 5)
				for i := 0; i < 5; i++ {
					mem := &model.Memory{Content: "no abstract item"}
					require.NoError(t, s.Create(context.Background(), mem))
					ids[i] = mem.ID
				}
				return ids
			},
			limit:     3,
			wantCount: 3,
		},
		{
			name: "zero limit defaults to 20",
			setup: func(s store.MemoryStore) []string {
				ids := make([]string, 5)
				for i := 0; i < 5; i++ {
					mem := &model.Memory{Content: "no abstract default limit"}
					require.NoError(t, s.Create(context.Background(), mem))
					ids[i] = mem.ID
				}
				return ids
			},
			limit:     0,
			wantCount: 5,
		},
		{
			name:      "empty store returns empty slice",
			setup:     func(s store.MemoryStore) []string { return nil },
			limit:     10,
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, cleanup := setupTestStore(t)
			defer cleanup()

			tt.setup(s)
			results, err := s.ListMissingAbstract(context.Background(), tt.limit)
			require.NoError(t, err)
			assert.Len(t, results, tt.wantCount)
			for _, mem := range results {
				assert.Empty(t, mem.Abstract, "all returned memories should have empty abstract")
				assert.Nil(t, mem.DeletedAt, "all returned memories should not be soft-deleted")
			}
		})
	}
}

func TestSearchText_BM25ColumnWeights(t *testing.T) {
	// abstract 权重高于 summary，命中 abstract 的结果应排名更高 / Abstract hit should rank higher than summary hit
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "bm25_weights.db")

	// content=10, abstract=5, summary=3
	s, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, nil)
	require.NoError(t, err)
	defer s.Close()

	err = s.Init(context.Background())
	require.NoError(t, err)

	// 记忆 A: 关键词仅在 summary 中
	memA := &model.Memory{Content: "unrelated content A", Summary: "quantum computing breakthrough"}
	err = s.Create(context.Background(), memA)
	require.NoError(t, err)

	// 记忆 B: 关键词仅在 abstract 中
	memB := &model.Memory{Content: "unrelated content B", Abstract: "quantum computing breakthrough"}
	err = s.Create(context.Background(), memB)
	require.NoError(t, err)

	results, err := s.SearchText(context.Background(), "quantum", &model.Identity{TeamID: "", OwnerID: model.SystemOwnerID}, 10)
	require.NoError(t, err)
	require.Len(t, results, 2)

	// abstract 命中 (memB) 的分数应高于 summary 命中 (memA)
	assert.Equal(t, memB.ID, results[0].Memory.ID, "abstract hit should rank first")
	assert.Equal(t, memA.ID, results[1].Memory.ID, "summary hit should rank second")
	assert.Greater(t, results[0].Score, results[1].Score, "abstract hit should have higher score")
}
