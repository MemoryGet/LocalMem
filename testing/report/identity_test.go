package report_test

import (
	"context"
	"fmt"
	"testing"

	"iclude/internal/model"
	"iclude/pkg/testreport"
	"iclude/pkg/tokenizer"
)

const (
	suiteIdentity     = "身份与可见性 (Identity & Visibility)"
	suiteIdentityIcon = "\U0001F512"
	suiteIdentityDesc = "验证记忆可见性隔离：private 仅创建者可见，team 团队内可见，public 全局可见"
)

func TestIdentity_VisibilityIsolation(t *testing.T) {
	tc := testreport.NewCase(t, suiteIdentity, suiteIdentityIcon, suiteIdentityDesc,
		"记忆可见性隔离 / Memory visibility isolation")
	defer tc.Done()

	tc.Input("scenario", "3 memories: alice-private, alice-team, bob-private in team-a")

	s := newTestStore(t, tokenizer.NewNoopTokenizer())
	defer s.Close()
	tc.Step("创建 SQLite store (NoopTokenizer)")

	ctx := context.Background()

	tc.Step("create memories with different visibility")
	for _, m := range []*model.Memory{
		{Content: "alice secret", TeamID: "team-a", OwnerID: "alice", Visibility: model.VisibilityPrivate},
		{Content: "team knowledge", TeamID: "team-a", OwnerID: "alice", Visibility: model.VisibilityTeam},
		{Content: "bob secret", TeamID: "team-a", OwnerID: "bob", Visibility: model.VisibilityPrivate},
	} {
		if err := s.Create(ctx, m); err != nil {
			t.Fatal(err)
		}
	}

	tc.Step("alice lists memories")
	aliceID := &model.Identity{TeamID: "team-a", OwnerID: "alice"}
	aliceResults, err := s.List(ctx, aliceID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	tc.Output("alice visible count", fmt.Sprintf("%d (expect 2: own private + team)", len(aliceResults)))
	if len(aliceResults) != 2 {
		t.Errorf("alice should see 2 memories, got %d", len(aliceResults))
	}

	tc.Step("bob lists memories")
	bobID := &model.Identity{TeamID: "team-a", OwnerID: "bob"}
	bobResults, err := s.List(ctx, bobID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	tc.Output("bob visible count", fmt.Sprintf("%d (expect 2: own private + team)", len(bobResults)))
	if len(bobResults) != 2 {
		t.Errorf("bob should see 2 memories, got %d", len(bobResults))
	}
}

func TestIdentity_PublicCrossTeam(t *testing.T) {
	tc := testreport.NewCase(t, suiteIdentity, suiteIdentityIcon, suiteIdentityDesc,
		"公开记忆跨团队可见 / Public memories visible across teams")
	defer tc.Done()

	tc.Input("scenario", "public memory in team-a, queried by team-b user")

	s := newTestStore(t, tokenizer.NewNoopTokenizer())
	defer s.Close()
	tc.Step("创建 SQLite store (NoopTokenizer)")

	ctx := context.Background()

	tc.Step("create public memory in team-a")
	if err := s.Create(ctx, &model.Memory{
		Content: "public knowledge", TeamID: "team-a", OwnerID: "alice", Visibility: model.VisibilityPublic,
	}); err != nil {
		t.Fatal(err)
	}

	tc.Step("team-b user queries")
	bobID := &model.Identity{TeamID: "team-b", OwnerID: "bob"}
	results, err := s.List(ctx, bobID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	tc.Output("cross-team visible", fmt.Sprintf("%d (expect 1)", len(results)))
	if len(results) != 1 {
		t.Errorf("team-b should see 1 public memory, got %d", len(results))
	}
}
