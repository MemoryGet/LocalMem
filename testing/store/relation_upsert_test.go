package store_test

import (
	"context"
	"sync"
	"testing"

	"iclude/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mkUpsertEntities 构造两个实体并返回其 ID / Create two entities, return their IDs
func mkUpsertEntities(t *testing.T, gs interface {
	CreateEntity(context.Context, *model.Entity) error
}) (string, string) {
	t.Helper()
	ctx := context.Background()
	a := &model.Entity{Name: "UpA", EntityType: "concept", Scope: "default"}
	b := &model.Entity{Name: "UpB", EntityType: "concept", Scope: "default"}
	require.NoError(t, gs.CreateEntity(ctx, a))
	require.NoError(t, gs.CreateEntity(ctx, b))
	return a.ID, b.ID
}

func TestUpsertRelation_InsertNew(t *testing.T) {
	gs, _, cleanup := setupGraphStore(t)
	defer cleanup()
	ctx := context.Background()
	aID, bID := mkUpsertEntities(t, gs)

	got, err := gs.UpsertRelation(ctx, &model.EntityRelation{
		SourceID: aID, TargetID: bID, RelationType: "uses", SourceMemoryID: "mem-1",
	})
	require.NoError(t, err)
	assert.Equal(t, 1, got.MentionCount)
	assert.Equal(t, "mem-1", got.SourceMemoryID)
	assert.NotEmpty(t, got.ID)
}

func TestUpsertRelation_Accumulate(t *testing.T) {
	gs, _, cleanup := setupGraphStore(t)
	defer cleanup()
	ctx := context.Background()
	aID, bID := mkUpsertEntities(t, gs)

	first, err := gs.UpsertRelation(ctx, &model.EntityRelation{
		SourceID: aID, TargetID: bID, RelationType: "uses", SourceMemoryID: "mem-1",
	})
	require.NoError(t, err)

	for i := 0; i < 2; i++ {
		_, err := gs.UpsertRelation(ctx, &model.EntityRelation{
			SourceID: aID, TargetID: bID, RelationType: "uses", SourceMemoryID: "mem-LATER",
		})
		require.NoError(t, err)
	}

	final, err := gs.UpsertRelation(ctx, &model.EntityRelation{
		SourceID: aID, TargetID: bID, RelationType: "uses", SourceMemoryID: "mem-LATER",
	})
	require.NoError(t, err)
	assert.Equal(t, 4, final.MentionCount, "mention_count should accumulate across upserts")
	assert.Equal(t, first.ID, final.ID, "id preserved from first insert")
	assert.Equal(t, "mem-1", final.SourceMemoryID, "source_memory_id preserved from first insert")
	assert.Equal(t, first.CreatedAt, final.CreatedAt, "created_at preserved from first insert")
}

func TestUpsertRelation_DifferentTypeSeparate(t *testing.T) {
	gs, _, cleanup := setupGraphStore(t)
	defer cleanup()
	ctx := context.Background()
	aID, bID := mkUpsertEntities(t, gs)

	_, err := gs.UpsertRelation(ctx, &model.EntityRelation{SourceID: aID, TargetID: bID, RelationType: "uses"})
	require.NoError(t, err)
	_, err = gs.UpsertRelation(ctx, &model.EntityRelation{SourceID: aID, TargetID: bID, RelationType: "likes"})
	require.NoError(t, err)

	rels, err := gs.GetEntityRelations(ctx, aID)
	require.NoError(t, err)
	assert.Len(t, rels, 2, "different relation_type → separate rows")
	for _, r := range rels {
		assert.Equal(t, 1, r.MentionCount)
	}
}

func TestUpsertRelation_SelfLoopErrors(t *testing.T) {
	gs, _, cleanup := setupGraphStore(t)
	defer cleanup()
	ctx := context.Background()
	aID, _ := mkUpsertEntities(t, gs)

	_, err := gs.UpsertRelation(ctx, &model.EntityRelation{SourceID: aID, TargetID: aID, RelationType: "uses"})
	assert.Error(t, err, "self-loop violates CHECK(source_id != target_id)")
}

func TestUpsertRelation_Concurrent(t *testing.T) {
	gs, _, cleanup := setupGraphStore(t)
	defer cleanup()
	ctx := context.Background()
	aID, bID := mkUpsertEntities(t, gs)

	const n = 20
	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			if _, err := gs.UpsertRelation(ctx, &model.EntityRelation{SourceID: aID, TargetID: bID, RelationType: "uses"}); err != nil {
				t.Logf("goroutine upsert error (may be busy-timeout): %v", err)
			}
		}()
	}
	wg.Wait()

	rels, err := gs.GetEntityRelations(ctx, aID)
	require.NoError(t, err)
	require.Len(t, rels, 1)
	assert.Equal(t, n, rels[0].MentionCount, "concurrent upserts must each increment exactly once (no TOCTOU)")
}
