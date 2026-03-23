package store_test

import (
	"context"
	"testing"

	"iclude/internal/model"
	"iclude/internal/store"
	"iclude/pkg/tokenizer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// setupVisibilityStore 创建带可见性测试数据的存储 / Create store with visibility test data
func setupVisibilityStore(t *testing.T) store.MemoryStore {
	t.Helper()
	s, err := store.NewSQLiteMemoryStore(":memory:", [3]float64{10, 5, 3}, tokenizer.NewNoopTokenizer())
	require.NoError(t, err)
	require.NoError(t, s.Init(context.Background()))

	ctx := context.Background()
	memories := []*model.Memory{
		{Content: "alice private memo", TeamID: "team-a", OwnerID: "alice", Visibility: model.VisibilityPrivate},
		{Content: "alice team memo", TeamID: "team-a", OwnerID: "alice", Visibility: model.VisibilityTeam},
		{Content: "bob private memo", TeamID: "team-a", OwnerID: "bob", Visibility: model.VisibilityPrivate},
		{Content: "public knowledge", TeamID: "team-a", OwnerID: "alice", Visibility: model.VisibilityPublic},
		{Content: "other team memo", TeamID: "team-b", OwnerID: "carol", Visibility: model.VisibilityTeam},
	}
	for _, m := range memories {
		require.NoError(t, s.Create(ctx, m))
	}
	return s
}

func TestList_VisibilityFiltering(t *testing.T) {
	s := setupVisibilityStore(t)
	ctx := context.Background()

	tests := []struct {
		name     string
		identity *model.Identity
		want     int
	}{
		{"alice sees own private + team + public", &model.Identity{TeamID: "team-a", OwnerID: "alice"}, 3},
		{"bob sees own private + team + public", &model.Identity{TeamID: "team-a", OwnerID: "bob"}, 3},
		{"team-b sees public + own team", &model.Identity{TeamID: "team-b", OwnerID: "carol"}, 2},
		{"system sees team + public only", &model.Identity{TeamID: "team-a", OwnerID: model.SystemOwnerID}, 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := s.List(ctx, tt.identity, 0, 100)
			require.NoError(t, err)
			if len(results) != tt.want {
				t.Errorf("got %d results, want %d", len(results), tt.want)
				for _, r := range results {
					t.Logf("  - %s (owner=%s, vis=%s, team=%s)", r.Content, r.OwnerID, r.Visibility, r.TeamID)
				}
			}
		})
	}
}

func TestGetVisible_VisibilityCheck(t *testing.T) {
	s := setupVisibilityStore(t)
	ctx := context.Background()

	// 先获取所有记忆 ID / Get all memory IDs first
	allMems, err := s.List(ctx, &model.Identity{TeamID: "team-a", OwnerID: "alice"}, 0, 100)
	require.NoError(t, err)

	// 找 alice 的 private memo
	var alicePrivateID string
	for _, m := range allMems {
		if m.Content == "alice private memo" {
			alicePrivateID = m.ID
			break
		}
	}
	require.NotEmpty(t, alicePrivateID, "alice private memo should exist")

	// alice 自己可以看到
	mem, err := s.GetVisible(ctx, alicePrivateID, &model.Identity{TeamID: "team-a", OwnerID: "alice"})
	require.NoError(t, err)
	assert.Equal(t, "alice private memo", mem.Content)

	// bob 看不到 alice 的 private memo
	_, err = s.GetVisible(ctx, alicePrivateID, &model.Identity{TeamID: "team-a", OwnerID: "bob"})
	assert.ErrorIs(t, err, model.ErrMemoryNotFound)

	// system 看不到 private memo
	_, err = s.GetVisible(ctx, alicePrivateID, &model.Identity{TeamID: "team-a", OwnerID: model.SystemOwnerID})
	assert.ErrorIs(t, err, model.ErrMemoryNotFound)
}

func TestList_LegacyEmptyOwner(t *testing.T) {
	// 旧数据 owner_id 为空，同 team 应可见 / Legacy data with empty owner_id should be visible to same team
	s, err := store.NewSQLiteMemoryStore(":memory:", [3]float64{10, 5, 3}, tokenizer.NewNoopTokenizer())
	require.NoError(t, err)
	require.NoError(t, s.Init(context.Background()))

	ctx := context.Background()
	require.NoError(t, s.Create(ctx, &model.Memory{
		Content:    "legacy memo",
		TeamID:     "team-x",
		OwnerID:    "",
		Visibility: model.VisibilityPrivate,
	}))

	// 同 team 的任何人都能看到无主 private 记忆
	results, err := s.List(ctx, &model.Identity{TeamID: "team-x", OwnerID: "anyone"}, 0, 100)
	require.NoError(t, err)
	assert.Len(t, results, 1, "legacy empty owner_id private memo should be visible to same team")
}
