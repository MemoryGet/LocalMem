package store_test

import (
	"context"
	"testing"

	"iclude/internal/memory"
	"iclude/internal/model"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLineageTracer_SimpleChain(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()
	identity := &model.Identity{TeamID: "team1", OwnerID: "owner1"}

	// episodic → semantic → procedural 三级链 / 3-level chain
	ep := &model.Memory{Content: "raw observation", TeamID: "team1", OwnerID: "owner1", Visibility: "private", MemoryClass: "episodic"}
	require.NoError(t, s.Create(ctx, ep))

	sem := &model.Memory{Content: "summarized insight", TeamID: "team1", OwnerID: "owner1", Visibility: "private", MemoryClass: "semantic"}
	require.NoError(t, s.Create(ctx, sem))
	require.NoError(t, s.AddDerivations(ctx, []string{ep.ID}, sem.ID))

	proc := &model.Memory{Content: "reusable pattern", TeamID: "team1", OwnerID: "owner1", Visibility: "private", MemoryClass: "procedural"}
	require.NoError(t, s.Create(ctx, proc))
	require.NoError(t, s.AddDerivations(ctx, []string{sem.ID}, proc.ID))

	tracer := memory.NewLineageTracer(s)

	// 从 procedural 出发追溯 / Trace from procedural
	resp, err := tracer.Trace(ctx, proc.ID, identity)
	require.NoError(t, err)
	assert.Equal(t, proc.ID, resp.Root.Memory.ID)
	assert.Equal(t, 3, resp.TotalNodes) // proc + sem + ep

	// Sources 应包含 semantic / Sources should contain semantic
	require.Len(t, resp.Root.Sources, 1)
	assert.Equal(t, sem.ID, resp.Root.Sources[0].Memory.ID)

	// semantic 的 Sources 应包含 episodic / Semantic's sources should contain episodic
	require.Len(t, resp.Root.Sources[0].Sources, 1)
	assert.Equal(t, ep.ID, resp.Root.Sources[0].Sources[0].Memory.ID)
}

func TestLineageTracer_FromMiddle(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()
	identity := &model.Identity{TeamID: "team1", OwnerID: "owner1"}

	// 从中间节点出发应同时向上向下展开 / Starting from middle node should expand both directions
	ep := &model.Memory{Content: "episode", TeamID: "team1", OwnerID: "owner1", Visibility: "private", MemoryClass: "episodic"}
	require.NoError(t, s.Create(ctx, ep))

	sem := &model.Memory{Content: "semantic", TeamID: "team1", OwnerID: "owner1", Visibility: "private", MemoryClass: "semantic"}
	require.NoError(t, s.Create(ctx, sem))
	require.NoError(t, s.AddDerivations(ctx, []string{ep.ID}, sem.ID))

	proc := &model.Memory{Content: "procedural", TeamID: "team1", OwnerID: "owner1", Visibility: "private", MemoryClass: "procedural"}
	require.NoError(t, s.Create(ctx, proc))
	require.NoError(t, s.AddDerivations(ctx, []string{sem.ID}, proc.ID))

	tracer := memory.NewLineageTracer(s)

	resp, err := tracer.Trace(ctx, sem.ID, identity)
	require.NoError(t, err)
	assert.Equal(t, sem.ID, resp.Root.Memory.ID)
	assert.Equal(t, 3, resp.TotalNodes)

	// 向上有 episodic / Upstream has episodic
	require.Len(t, resp.Root.Sources, 1)
	assert.Equal(t, ep.ID, resp.Root.Sources[0].Memory.ID)

	// 向下有 procedural / Downstream has procedural
	require.Len(t, resp.Root.Children, 1)
	assert.Equal(t, proc.ID, resp.Root.Children[0].Memory.ID)
}

func TestLineageTracer_ConsolidatedInto(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()
	identity := &model.Identity{TeamID: "team1", OwnerID: "owner1"}

	// 归纳链：src1, src2 → target / Consolidation chain
	target := &model.Memory{Content: "consolidated", TeamID: "team1", OwnerID: "owner1", Visibility: "private", MemoryClass: "semantic"}
	require.NoError(t, s.Create(ctx, target))

	src1 := &model.Memory{Content: "src1", TeamID: "team1", OwnerID: "owner1", Visibility: "private", ConsolidatedInto: target.ID}
	src2 := &model.Memory{Content: "src2", TeamID: "team1", OwnerID: "owner1", Visibility: "private", ConsolidatedInto: target.ID}
	require.NoError(t, s.Create(ctx, src1))
	require.NoError(t, s.Create(ctx, src2))

	tracer := memory.NewLineageTracer(s)

	resp, err := tracer.Trace(ctx, target.ID, identity)
	require.NoError(t, err)
	assert.Equal(t, 3, resp.TotalNodes) // target + src1 + src2
	assert.Len(t, resp.Root.Children, 2)
}

func TestLineageTracer_SingleNode(t *testing.T) {
	s, cleanup := setupTestStore(t)
	defer cleanup()
	ctx := context.Background()
	identity := &model.Identity{TeamID: "team1", OwnerID: "owner1"}

	mem := &model.Memory{Content: "isolated", TeamID: "team1", OwnerID: "owner1", Visibility: "private"}
	require.NoError(t, s.Create(ctx, mem))

	tracer := memory.NewLineageTracer(s)

	resp, err := tracer.Trace(ctx, mem.ID, identity)
	require.NoError(t, err)
	assert.Equal(t, 1, resp.TotalNodes)
	assert.Empty(t, resp.Root.Sources)
	assert.Empty(t, resp.Root.Children)
}
