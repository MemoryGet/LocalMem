package store_test

import (
	"context"
	"testing"

	"iclude/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// seedSessionMemories 创建一组带 source_ref 的测试记忆 / Create test memories with source_ref
func seedSessionMemories(t *testing.T, s interface {
	Create(ctx context.Context, mem *model.Memory) error
}, sourceRef string, count int) []*model.Memory {
	t.Helper()
	ctx := context.Background()
	mems := make([]*model.Memory, count)
	for i := 0; i < count; i++ {
		m := &model.Memory{
			Content:    "session memory " + sourceRef,
			SourceRef:  sourceRef,
			SourceType: "conversation",
			TeamID:     "team1",
			OwnerID:    "owner1",
			Visibility: "private",
		}
		require.NoError(t, s.Create(ctx, m))
		mems[i] = m
	}
	return mems
}

func TestListBySourceRef(t *testing.T) {
	tests := []struct {
		name      string
		seedRef   string
		queryRef  string
		wantCount int
	}{
		{name: "match 3 memories", seedRef: "session-abc", queryRef: "session-abc", wantCount: 3},
		{name: "no match", seedRef: "session-abc", queryRef: "session-xyz", wantCount: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, cleanup := setupTestStore(t)
			defer cleanup()
			ctx := context.Background()

			seedSessionMemories(t, s, tt.seedRef, 3)
			identity := &model.Identity{TeamID: "team1", OwnerID: "owner1"}

			results, err := s.ListBySourceRef(ctx, tt.queryRef, identity, 0, 20)
			require.NoError(t, err)
			assert.Len(t, results, tt.wantCount)
		})
	}
}

func TestSoftDeleteBySourceRef(t *testing.T) {
	tests := []struct {
		name        string
		seedRef     string
		deleteRef   string
		wantDeleted int
	}{
		{name: "delete all 3", seedRef: "session-del", deleteRef: "session-del", wantDeleted: 3},
		{name: "delete none", seedRef: "session-del", deleteRef: "session-none", wantDeleted: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, cleanup := setupTestStore(t)
			defer cleanup()
			ctx := context.Background()

			seedSessionMemories(t, s, tt.seedRef, 3)
			identity := &model.Identity{TeamID: "team1", OwnerID: "owner1"}

			count, err := s.SoftDeleteBySourceRef(ctx, tt.deleteRef, identity)
			require.NoError(t, err)
			assert.Equal(t, tt.wantDeleted, count)

			// 验证软删除后不可见 / Verify invisible after soft delete
			if tt.wantDeleted > 0 {
				identity := &model.Identity{TeamID: "team1", OwnerID: "owner1"}
				results, err := s.ListBySourceRef(ctx, tt.seedRef, identity, 0, 20)
				require.NoError(t, err)
				assert.Empty(t, results)
			}
		})
	}
}

func TestRestoreBySourceRef(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()
	identity := &model.Identity{TeamID: "team1", OwnerID: "owner1"}

	seedSessionMemories(t, s, "session-restore", 3)

	// 先软删除 / Soft delete first
	deleted, err := s.SoftDeleteBySourceRef(ctx, "session-restore", identity)
	require.NoError(t, err)
	assert.Equal(t, 3, deleted)

	// 恢复 / Restore
	restored, err := s.RestoreBySourceRef(ctx, "session-restore", identity)
	require.NoError(t, err)
	assert.Equal(t, 3, restored)

	// 验证恢复后可见 / Verify visible after restore
	results, err := s.ListBySourceRef(ctx, "session-restore", identity, 0, 20)
	require.NoError(t, err)
	assert.Len(t, results, 3)
}

func TestListDerivedFrom(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()
	identity := &model.Identity{TeamID: "team1", OwnerID: "owner1"}

	// 创建源记忆 / Create source memory
	source := &model.Memory{Content: "source episodic", TeamID: "team1", OwnerID: "owner1", Visibility: "private"}
	require.NoError(t, s.Create(ctx, source))

	// 创建衍生记忆 / Create derived memory
	derived := &model.Memory{
		Content:     "derived semantic",
		TeamID:      "team1",
		OwnerID:     "owner1",
		Visibility:  "private",
		MemoryClass: "semantic",
	}
	require.NoError(t, s.Create(ctx, derived))
	require.NoError(t, s.AddDerivations(ctx, []string{source.ID}, derived.ID))

	// 创建无关记忆 / Create unrelated memory
	unrelated := &model.Memory{Content: "unrelated", TeamID: "team1", OwnerID: "owner1", Visibility: "private"}
	require.NoError(t, s.Create(ctx, unrelated))
	_ = unrelated

	results, err := s.ListDerivedFrom(ctx, source.ID, identity)
	require.NoError(t, err)
	assert.Len(t, results, 1)
	assert.Equal(t, derived.ID, results[0].ID)
}

func TestListConsolidatedInto(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()
	identity := &model.Identity{TeamID: "team1", OwnerID: "owner1"}

	// 创建目标记忆 / Create target memory
	target := &model.Memory{Content: "consolidated target", TeamID: "team1", OwnerID: "owner1", Visibility: "private"}
	require.NoError(t, s.Create(ctx, target))

	// 创建被归纳的源记忆 / Create source memories consolidated into target
	src1 := &model.Memory{Content: "src 1", TeamID: "team1", OwnerID: "owner1", Visibility: "private", ConsolidatedInto: target.ID}
	src2 := &model.Memory{Content: "src 2", TeamID: "team1", OwnerID: "owner1", Visibility: "private", ConsolidatedInto: target.ID}
	require.NoError(t, s.Create(ctx, src1))
	require.NoError(t, s.Create(ctx, src2))

	results, err := s.ListConsolidatedInto(ctx, target.ID, identity)
	require.NoError(t, err)
	assert.Len(t, results, 2)
}

func TestTimelineSourceRefFilter(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()

	seedSessionMemories(t, s, "session-timeline-a", 2)
	seedSessionMemories(t, s, "session-timeline-b", 3)

	req := &model.TimelineRequest{
		SourceRef: "session-timeline-a",
		Limit:     20,
		TeamID:    "team1",
		OwnerID:   "owner1",
	}

	results, err := s.ListTimeline(ctx, req)
	require.NoError(t, err)
	assert.Len(t, results, 2)
}
