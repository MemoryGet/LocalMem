package memory_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"iclude/internal/memory"
	"iclude/internal/model"
	"iclude/internal/store"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupManager(t *testing.T) (*memory.Manager, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	s, err := store.NewSQLiteMemoryStore(dbPath, [3]float64{10, 5, 3}, nil)
	require.NoError(t, err)

	err = s.Init(context.Background())
	require.NoError(t, err)

	mgr := memory.NewManager(memory.ManagerDeps{MemStore: s})
	return mgr, func() {
		s.Close()
		os.RemoveAll(dir)
	}
}

func TestManager_Create(t *testing.T) {
	tests := []struct {
		name    string
		req     *model.CreateMemoryRequest
		wantErr bool
		errIs   error
	}{
		{
			name:    "happy path",
			req:     &model.CreateMemoryRequest{Content: "test memory"},
			wantErr: false,
		},
		{
			name:    "with team and metadata",
			req:     &model.CreateMemoryRequest{Content: "test", TeamID: "team1", Metadata: map[string]any{"k": "v"}},
			wantErr: false,
		},
		{
			name:    "empty content",
			req:     &model.CreateMemoryRequest{Content: ""},
			wantErr: true,
			errIs:   model.ErrInvalidInput,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr, cleanup := setupManager(t)
			defer cleanup()

			mem, err := mgr.Create(context.Background(), tt.req)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errIs != nil {
					assert.ErrorIs(t, err, tt.errIs)
				}
				return
			}
			require.NoError(t, err)
			assert.NotEmpty(t, mem.ID)
			assert.Equal(t, tt.req.Content, mem.Content)
		})
	}
}

func TestManager_Get(t *testing.T) {
	tests := []struct {
		name    string
		id      string
		setup   bool
		wantErr bool
		errIs   error
	}{
		{
			name:    "existing",
			setup:   true,
			wantErr: false,
		},
		{
			name:    "not found",
			id:      "nonexistent",
			wantErr: true,
			errIs:   model.ErrMemoryNotFound,
		},
		{
			name:    "empty id",
			id:      "",
			wantErr: true,
			errIs:   model.ErrInvalidInput,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mgr, cleanup := setupManager(t)
			defer cleanup()

			id := tt.id
			if tt.setup {
				mem, err := mgr.Create(context.Background(), &model.CreateMemoryRequest{Content: "test"})
				require.NoError(t, err)
				id = mem.ID
			}

			mem, err := mgr.Get(context.Background(), id)
			if tt.wantErr {
				require.Error(t, err)
				if tt.errIs != nil {
					assert.ErrorIs(t, err, tt.errIs)
				}
				return
			}
			require.NoError(t, err)
			assert.Equal(t, id, mem.ID)
		})
	}
}

func TestManager_Update(t *testing.T) {
	mgr, cleanup := setupManager(t)
	defer cleanup()

	ctx := context.Background()
	mem, err := mgr.Create(ctx, &model.CreateMemoryRequest{Content: "original"})
	require.NoError(t, err)

	newContent := "updated content"
	updated, err := mgr.Update(ctx, mem.ID, &model.UpdateMemoryRequest{Content: &newContent})
	require.NoError(t, err)
	assert.Equal(t, newContent, updated.Content)

	// 验证持久化
	fetched, err := mgr.Get(ctx, mem.ID)
	require.NoError(t, err)
	assert.Equal(t, newContent, fetched.Content)
}

func TestManager_Delete(t *testing.T) {
	mgr, cleanup := setupManager(t)
	defer cleanup()

	ctx := context.Background()
	mem, err := mgr.Create(ctx, &model.CreateMemoryRequest{Content: "to delete"})
	require.NoError(t, err)

	err = mgr.Delete(ctx, mem.ID)
	require.NoError(t, err)

	_, err = mgr.Get(ctx, mem.ID)
	assert.ErrorIs(t, err, model.ErrMemoryNotFound)
}

func TestManager_List(t *testing.T) {
	mgr, cleanup := setupManager(t)
	defer cleanup()

	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_, err := mgr.Create(ctx, &model.CreateMemoryRequest{Content: fmt.Sprintf("item %d", i)})
		require.NoError(t, err)
	}

	memories, err := mgr.List(ctx, &model.Identity{TeamID: "", OwnerID: model.SystemOwnerID}, 0, 20)
	require.NoError(t, err)
	assert.Len(t, memories, 5)

	// 测试 limit 上限
	memories, err = mgr.List(ctx, &model.Identity{TeamID: "", OwnerID: model.SystemOwnerID}, 0, 200)
	require.NoError(t, err)
	assert.Len(t, memories, 5) // 仅 5 条，但 limit 被裁至 100
}
