// Package eval_test 会话模拟端到端测试 / Conversation simulation end-to-end tests
package eval_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	eval "iclude/testing/eval"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConvSim_MockRetainer_SkipsShortTurns verifies the MockAIRetainer rule-based decision logic.
// No store needed — pure unit test.
func TestConvSim_MockRetainer_SkipsShortTurns(t *testing.T) {
	retainer := eval.NewMockAIRetainer()
	cases := []struct {
		text   string
		retain bool
	}{
		{"OK", false},
		{"Got it.", false},
		{"I just got promoted to senior engineer at Google!", true},
		{"We are planning to move to Seattle next month for the new role.", true},
		{"Sure!", false},
		{"thanks", false},
	}
	for _, tc := range cases {
		turn := eval.ConvTurn{Speaker: "User", Text: tc.text}
		d := retainer.Decide(turn)
		assert.Equal(t, tc.retain, d.ShouldRetain, "text: %q", tc.text)
	}
}

// TestConvSim_StorageQuality verifies storage quality after a simulated conversation:
// retained turns have summaries, short turns are skipped, later turns link to prior ones.
func TestConvSim_StorageQuality(t *testing.T) {
	if testing.Short() {
		t.Skip("skip: conv-sim requires store initialization")
	}
	eval.LoadTestConfig()

	dbPath := filepath.Join(t.TempDir(), "convsim.db")
	mgr, retriever := eval.SetupTestMgrAndRetriever(t, dbPath)

	session := eval.ConvSession{
		ID: "test-session-001",
		Turns: []eval.ConvTurn{
			{Speaker: "User", Text: "I just got promoted to senior engineer at Google!", Timestamp: time.Now()},
			{Speaker: "Assistant", Text: "Congratulations! That is a big achievement.", Timestamp: time.Now().Add(5 * time.Second)},
			{Speaker: "User", Text: "Thanks.", Timestamp: time.Now().Add(10 * time.Second)},
			{Speaker: "User", Text: "We are moving to Seattle next month for the new role at Google.", Timestamp: time.Now().Add(20 * time.Second)},
			{Speaker: "User", Text: "My Google relocation package to Seattle also covers temporary housing.", Timestamp: time.Now().Add(30 * time.Second)},
		},
	}

	result, err := eval.RunConvSim(context.Background(), session, mgr, retriever)
	require.NoError(t, err)

	t.Logf("ConvSim result: total=%d retained=%d skipped=%d withSummary=%d withDerived=%d",
		result.TotalTurns, result.RetainedTurns, result.SkippedTurns, result.WithSummary, result.WithDerived)

	assert.Equal(t, 5, result.TotalTurns, "total turns")
	assert.GreaterOrEqual(t, result.RetainedTurns, 3, "at least 3 meaningful turns retained")
	assert.GreaterOrEqual(t, result.SkippedTurns, 1, "short confirmation turn skipped")

	if result.RetainedTurns > 0 {
		summaryRate := float64(result.WithSummary) / float64(result.RetainedTurns)
		assert.GreaterOrEqual(t, summaryRate, 0.9, "≥90%% of retained memories should have summaries")
	}

	if result.RetainedTurns >= 2 {
		assert.GreaterOrEqual(t, result.WithDerived, 1, "at least 1 memory should link to prior context via recall")
	}
}
