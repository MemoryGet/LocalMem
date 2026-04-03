package store_test

import (
	"context"
	"testing"

	"iclude/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMemoryClass_EndToEnd 端到端验证 memory_class 和 derived_from 在 SQLite 存储层的完整生命周期
// / End-to-end verification of memory_class and derived_from through the full SQLite store lifecycle
func TestMemoryClass_EndToEnd(t *testing.T) {
	t.Run("create with explicit class and derived_from", func(t *testing.T) {
		s, cleanup := setupTestStore(t)
		defer cleanup()

		mem := &model.Memory{
			Content:     "consolidated insight about Go patterns",
			TeamID:      "team1",
			Scope:       "test",
			MemoryClass: "semantic",
			DerivedFrom: []string{"src-001", "src-002"},
		}

		err := s.Create(context.Background(), mem)
		require.NoError(t, err)
		require.NotEmpty(t, mem.ID)

		got, err := s.Get(context.Background(), mem.ID)
		require.NoError(t, err)
		assert.Equal(t, "semantic", got.MemoryClass)
		assert.Equal(t, []string{"src-001", "src-002"}, got.DerivedFrom)
	})

	t.Run("create with default class", func(t *testing.T) {
		s, cleanup := setupTestStore(t)
		defer cleanup()

		mem := &model.Memory{
			Content: "user said something interesting",
			TeamID:  "team1",
			Scope:   "test",
		}

		err := s.Create(context.Background(), mem)
		require.NoError(t, err)

		got, err := s.Get(context.Background(), mem.ID)
		require.NoError(t, err)
		assert.Equal(t, "episodic", got.MemoryClass)
		assert.Nil(t, got.DerivedFrom)
	})

	t.Run("update memory_class persists", func(t *testing.T) {
		s, cleanup := setupTestStore(t)
		defer cleanup()

		mem := &model.Memory{
			Content:     "original episodic memory",
			TeamID:      "team1",
			Scope:       "test",
			MemoryClass: "episodic",
		}

		err := s.Create(context.Background(), mem)
		require.NoError(t, err)

		// 读取 → 修改 → 写回 / Read → modify → write back
		got, err := s.Get(context.Background(), mem.ID)
		require.NoError(t, err)
		assert.Equal(t, "episodic", got.MemoryClass)

		got.MemoryClass = "procedural"
		got.DerivedFrom = []string{"reflect-session-42"}
		err = s.Update(context.Background(), got)
		require.NoError(t, err)

		updated, err := s.Get(context.Background(), got.ID)
		require.NoError(t, err)
		assert.Equal(t, "procedural", updated.MemoryClass)
		assert.Equal(t, []string{"reflect-session-42"}, updated.DerivedFrom)
	})

	t.Run("derived_from JSON round-trip with multiple IDs", func(t *testing.T) {
		s, cleanup := setupTestStore(t)
		defer cleanup()

		sourceIDs := []string{
			"mem-aaa-111",
			"mem-bbb-222",
			"mem-ccc-333",
			"mem-ddd-444",
		}

		mem := &model.Memory{
			Content:     "consolidated from four source memories",
			TeamID:      "team1",
			Scope:       "test",
			MemoryClass: "semantic",
			DerivedFrom: sourceIDs,
		}

		err := s.Create(context.Background(), mem)
		require.NoError(t, err)

		got, err := s.Get(context.Background(), mem.ID)
		require.NoError(t, err)
		assert.Equal(t, sourceIDs, got.DerivedFrom)
		assert.Len(t, got.DerivedFrom, 4)
	})

	t.Run("clear derived_from via update", func(t *testing.T) {
		s, cleanup := setupTestStore(t)
		defer cleanup()

		mem := &model.Memory{
			Content:     "memory with lineage",
			TeamID:      "team1",
			Scope:       "test",
			MemoryClass: "semantic",
			DerivedFrom: []string{"src-x"},
		}

		err := s.Create(context.Background(), mem)
		require.NoError(t, err)

		got, err := s.Get(context.Background(), mem.ID)
		require.NoError(t, err)
		require.Equal(t, []string{"src-x"}, got.DerivedFrom)

		// 清空 derived_from / Clear derived_from
		got.DerivedFrom = nil
		err = s.Update(context.Background(), got)
		require.NoError(t, err)

		updated, err := s.Get(context.Background(), got.ID)
		require.NoError(t, err)
		assert.Nil(t, updated.DerivedFrom)
	})
}
