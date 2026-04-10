package store_test

import (
	"context"
	"testing"
	"time"

	"iclude/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSoftDeleteEntity_Basic(t *testing.T) {
	gs, _, cleanup := setupGraphStore(t)
	defer cleanup()

	ctx := context.Background()

	// 创建实体 / Create entity
	entity := &model.Entity{Name: "SoftDel", EntityType: "concept", Scope: "default"}
	require.NoError(t, gs.CreateEntity(ctx, entity))
	require.NotEmpty(t, entity.ID)

	// 软删除 / Soft delete
	err := gs.SoftDeleteEntity(ctx, entity.ID)
	require.NoError(t, err)

	// GetEntity 应返回 ErrEntityNotFound / GetEntity should return ErrEntityNotFound
	_, err = gs.GetEntity(ctx, entity.ID)
	assert.ErrorIs(t, err, model.ErrEntityNotFound)

	// ListEntities 不应包含已删除的实体 / ListEntities should exclude soft-deleted
	list, err := gs.ListEntities(ctx, "", "", 100)
	require.NoError(t, err)
	for _, e := range list {
		assert.NotEqual(t, entity.ID, e.ID, "soft-deleted entity should not appear in ListEntities")
	}

	// 重复软删除应返回 ErrEntityNotFound / Double soft delete should return ErrEntityNotFound
	err = gs.SoftDeleteEntity(ctx, entity.ID)
	assert.ErrorIs(t, err, model.ErrEntityNotFound)
}

func TestRestoreEntity_Basic(t *testing.T) {
	gs, _, cleanup := setupGraphStore(t)
	defer cleanup()

	ctx := context.Background()

	// 创建并软删除 / Create and soft delete
	entity := &model.Entity{Name: "RestoreMe", EntityType: "concept", Scope: "default"}
	require.NoError(t, gs.CreateEntity(ctx, entity))
	require.NoError(t, gs.SoftDeleteEntity(ctx, entity.ID))

	// 确认不可见 / Confirm invisible
	_, err := gs.GetEntity(ctx, entity.ID)
	require.ErrorIs(t, err, model.ErrEntityNotFound)

	// 恢复 / Restore
	err = gs.RestoreEntity(ctx, entity.ID)
	require.NoError(t, err)

	// 确认可见 / Confirm visible again
	got, err := gs.GetEntity(ctx, entity.ID)
	require.NoError(t, err)
	assert.Equal(t, "RestoreMe", got.Name)
	assert.Nil(t, got.DeletedAt)

	// 恢复未删除的实体应返回 ErrEntityNotFound / Restore non-deleted should return ErrEntityNotFound
	err = gs.RestoreEntity(ctx, entity.ID)
	assert.ErrorIs(t, err, model.ErrEntityNotFound)
}

func TestUpdateRelationStats_CreateAndIncrement(t *testing.T) {
	gs, _, cleanup := setupGraphStore(t)
	defer cleanup()

	ctx := context.Background()

	// 创建两个实体 / Create two entities
	e1 := &model.Entity{Name: "Alice", EntityType: "person", Scope: "default"}
	require.NoError(t, gs.CreateEntity(ctx, e1))
	e2 := &model.Entity{Name: "Bob", EntityType: "person", Scope: "default"}
	require.NoError(t, gs.CreateEntity(ctx, e2))

	// 首次调用应创建关系 / First call should create relation
	rel, err := gs.UpdateRelationStats(ctx, e1.ID, e2.ID, "collaborates_with")
	require.NoError(t, err)
	assert.Equal(t, 1, rel.MentionCount)
	assert.Equal(t, e1.ID, rel.SourceID)
	assert.Equal(t, e2.ID, rel.TargetID)
	assert.Equal(t, "collaborates_with", rel.RelationType)
	assert.NotNil(t, rel.LastSeenAt)

	// 第二次调用应递增 / Second call should increment
	rel2, err := gs.UpdateRelationStats(ctx, e1.ID, e2.ID, "collaborates_with")
	require.NoError(t, err)
	assert.Equal(t, 2, rel2.MentionCount)
	assert.Equal(t, rel.ID, rel2.ID, "should be same relation")
}

func TestCleanupStaleRelations_Basic(t *testing.T) {
	gs, _, cleanup := setupGraphStore(t)
	defer cleanup()

	ctx := context.Background()

	// 创建两个实体和一个关系 / Create two entities and one relation
	e1 := &model.Entity{Name: "X", EntityType: "concept", Scope: "default"}
	require.NoError(t, gs.CreateEntity(ctx, e1))
	e2 := &model.Entity{Name: "Y", EntityType: "concept", Scope: "default"}
	require.NoError(t, gs.CreateEntity(ctx, e2))

	rel := &model.EntityRelation{
		SourceID:     e1.ID,
		TargetID:     e2.ID,
		RelationType: "related_to",
		Weight:       0.5,
	}
	require.NoError(t, gs.CreateRelation(ctx, rel))

	// 确认关系存在 / Confirm relation exists
	rels, err := gs.GetEntityRelations(ctx, e1.ID)
	require.NoError(t, err)
	require.Len(t, rels, 1)
	assert.Equal(t, 1, rels[0].MentionCount)

	// 用未来时间作为 cutoff 清理 mention_count < 2 的关系 / Cleanup with future cutoff
	cutoff := time.Now().UTC().Add(1 * time.Hour)
	deleted, err := gs.CleanupStaleRelations(ctx, 2, cutoff)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)

	// 确认关系已删除 / Confirm relation is gone
	rels, err = gs.GetEntityRelations(ctx, e1.ID)
	require.NoError(t, err)
	assert.Len(t, rels, 0)
}

func TestCleanupOrphanEntities_Basic(t *testing.T) {
	gs, _, cleanup := setupGraphStore(t)
	defer cleanup()

	ctx := context.Background()

	// 创建一个孤儿实体（无关系无关联） / Create an orphan entity (no relations, no memory associations)
	orphan := &model.Entity{Name: "Orphan", EntityType: "concept", Scope: "default"}
	require.NoError(t, gs.CreateEntity(ctx, orphan))

	// 创建一个有关系的实体 / Create entities with a relation
	e1 := &model.Entity{Name: "Connected1", EntityType: "person", Scope: "default"}
	require.NoError(t, gs.CreateEntity(ctx, e1))
	e2 := &model.Entity{Name: "Connected2", EntityType: "person", Scope: "default"}
	require.NoError(t, gs.CreateEntity(ctx, e2))
	require.NoError(t, gs.CreateRelation(ctx, &model.EntityRelation{
		SourceID: e1.ID, TargetID: e2.ID, RelationType: "knows",
	}))

	// 清理孤儿 / Cleanup orphans
	cleaned, err := gs.CleanupOrphanEntities(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(1), cleaned)

	// 孤儿应不可见 / Orphan should be invisible
	_, err = gs.GetEntity(ctx, orphan.ID)
	assert.ErrorIs(t, err, model.ErrEntityNotFound)

	// 有关系的实体应仍可见 / Connected entities should still be visible
	_, err = gs.GetEntity(ctx, e1.ID)
	require.NoError(t, err)
	_, err = gs.GetEntity(ctx, e2.ID)
	require.NoError(t, err)
}

func TestPurgeDeletedEntities_Basic(t *testing.T) {
	gs, _, cleanup := setupGraphStore(t)
	defer cleanup()

	ctx := context.Background()

	// 创建并软删除 / Create and soft delete
	entity := &model.Entity{Name: "PurgeMe", EntityType: "concept", Scope: "default"}
	require.NoError(t, gs.CreateEntity(ctx, entity))
	require.NoError(t, gs.SoftDeleteEntity(ctx, entity.ID))

	// 用过去时间作为 cutoff 不应清除 / Past cutoff should not purge
	pastCutoff := time.Now().UTC().Add(-1 * time.Hour)
	purged, err := gs.PurgeDeletedEntities(ctx, pastCutoff)
	require.NoError(t, err)
	assert.Equal(t, int64(0), purged)

	// 用未来时间作为 cutoff 应清除 / Future cutoff should purge
	futureCutoff := time.Now().UTC().Add(1 * time.Hour)
	purged, err = gs.PurgeDeletedEntities(ctx, futureCutoff)
	require.NoError(t, err)
	assert.Equal(t, int64(1), purged)

	// 恢复也应失败（硬删除后不存在）/ Restore should also fail (hard deleted)
	err = gs.RestoreEntity(ctx, entity.ID)
	assert.ErrorIs(t, err, model.ErrEntityNotFound)
}
