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
	t.Run("create with explicit class and derived_from via junction table", func(t *testing.T) {
		s, cleanup := setupTestStore(t)
		defer cleanup()
		ctx := context.Background()

		// 先创建来源记忆 / Create source memories first (FK constraint)
		src1 := &model.Memory{Content: "source 1", TeamID: "team1", Scope: "test"}
		require.NoError(t, s.Create(ctx, src1))
		src2 := &model.Memory{Content: "source 2", TeamID: "team1", Scope: "test"}
		require.NoError(t, s.Create(ctx, src2))

		mem := &model.Memory{
			Content:     "consolidated insight about Go patterns",
			TeamID:      "team1",
			Scope:       "test",
			MemoryClass: "semantic",
		}
		err := s.Create(ctx, mem)
		require.NoError(t, err)
		require.NotEmpty(t, mem.ID)

		// 写入溯源到 junction 表 / Write derivations to junction table
		err = s.AddDerivations(ctx, []string{src1.ID, src2.ID}, mem.ID)
		require.NoError(t, err)

		got, err := s.Get(ctx, mem.ID)
		require.NoError(t, err)
		assert.Equal(t, "semantic", got.MemoryClass)

		// 通过 junction 表读取溯源 / Read derivations from junction table
		derivedFrom, err := s.GetDerivedFrom(ctx, mem.ID)
		require.NoError(t, err)
		assert.Len(t, derivedFrom, 2)
		assert.Contains(t, derivedFrom, src1.ID)
		assert.Contains(t, derivedFrom, src2.ID)
	})

	t.Run("create with default class", func(t *testing.T) {
		s, cleanup := setupTestStore(t)
		defer cleanup()
		ctx := context.Background()

		mem := &model.Memory{
			Content: "user said something interesting",
			TeamID:  "team1",
			Scope:   "test",
		}

		err := s.Create(ctx, mem)
		require.NoError(t, err)

		got, err := s.Get(ctx, mem.ID)
		require.NoError(t, err)
		assert.Equal(t, "episodic", got.MemoryClass)

		// 无溯源 / No derivations
		derivedFrom, err := s.GetDerivedFrom(ctx, mem.ID)
		require.NoError(t, err)
		assert.Nil(t, derivedFrom)
	})

	t.Run("update memory_class persists", func(t *testing.T) {
		s, cleanup := setupTestStore(t)
		defer cleanup()
		ctx := context.Background()

		// 创建来源 / Create source
		src := &model.Memory{Content: "reflect session data", TeamID: "team1", Scope: "test"}
		require.NoError(t, s.Create(ctx, src))

		mem := &model.Memory{
			Content:     "original episodic memory",
			TeamID:      "team1",
			Scope:       "test",
			MemoryClass: "episodic",
		}

		err := s.Create(ctx, mem)
		require.NoError(t, err)

		got, err := s.Get(ctx, mem.ID)
		require.NoError(t, err)
		assert.Equal(t, "episodic", got.MemoryClass)

		got.MemoryClass = "procedural"
		err = s.Update(ctx, got)
		require.NoError(t, err)

		// 添加溯源 / Add derivation
		err = s.AddDerivations(ctx, []string{src.ID}, got.ID)
		require.NoError(t, err)

		updated, err := s.Get(ctx, got.ID)
		require.NoError(t, err)
		assert.Equal(t, "procedural", updated.MemoryClass)

		derivedFrom, err := s.GetDerivedFrom(ctx, got.ID)
		require.NoError(t, err)
		assert.Equal(t, []string{src.ID}, derivedFrom)
	})

	t.Run("derived_from junction round-trip with multiple IDs", func(t *testing.T) {
		s, cleanup := setupTestStore(t)
		defer cleanup()
		ctx := context.Background()

		// 创建 4 个来源记忆 / Create 4 source memories
		var sourceIDs []string
		for i := 0; i < 4; i++ {
			src := &model.Memory{Content: "source", TeamID: "team1", Scope: "test"}
			require.NoError(t, s.Create(ctx, src))
			sourceIDs = append(sourceIDs, src.ID)
		}

		mem := &model.Memory{
			Content:     "consolidated from four source memories",
			TeamID:      "team1",
			Scope:       "test",
			MemoryClass: "semantic",
		}

		err := s.Create(ctx, mem)
		require.NoError(t, err)

		err = s.AddDerivations(ctx, sourceIDs, mem.ID)
		require.NoError(t, err)

		derivedFrom, err := s.GetDerivedFrom(ctx, mem.ID)
		require.NoError(t, err)
		assert.Len(t, derivedFrom, 4)
	})

	t.Run("GetDerivedTo returns targets", func(t *testing.T) {
		s, cleanup := setupTestStore(t)
		defer cleanup()
		ctx := context.Background()

		src := &model.Memory{Content: "source", TeamID: "team1", Scope: "test"}
		require.NoError(t, s.Create(ctx, src))

		t1 := &model.Memory{Content: "target1", TeamID: "team1", Scope: "test"}
		require.NoError(t, s.Create(ctx, t1))

		t2 := &model.Memory{Content: "target2", TeamID: "team1", Scope: "test"}
		require.NoError(t, s.Create(ctx, t2))

		require.NoError(t, s.AddDerivations(ctx, []string{src.ID}, t1.ID))
		require.NoError(t, s.AddDerivations(ctx, []string{src.ID}, t2.ID))

		targets, err := s.GetDerivedTo(ctx, src.ID)
		require.NoError(t, err)
		assert.Len(t, targets, 2)
	})

	t.Run("AddDerivations is idempotent (INSERT OR IGNORE)", func(t *testing.T) {
		s, cleanup := setupTestStore(t)
		defer cleanup()
		ctx := context.Background()

		src := &model.Memory{Content: "source", TeamID: "team1", Scope: "test"}
		require.NoError(t, s.Create(ctx, src))

		mem := &model.Memory{Content: "target", TeamID: "team1", Scope: "test"}
		require.NoError(t, s.Create(ctx, mem))

		// 重复添加同一溯源 / Add same derivation twice
		require.NoError(t, s.AddDerivations(ctx, []string{src.ID}, mem.ID))
		require.NoError(t, s.AddDerivations(ctx, []string{src.ID}, mem.ID))

		derivedFrom, err := s.GetDerivedFrom(ctx, mem.ID)
		require.NoError(t, err)
		assert.Len(t, derivedFrom, 1)
	})
}
