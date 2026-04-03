// lineage.go 记忆溯源查询 / Memory lineage traversal (B7)
package memory

import (
	"context"
	"fmt"

	"iclude/internal/model"
	"iclude/internal/store"
)

// LineageNode 溯源节点 / Lineage tree node
type LineageNode struct {
	Memory   *model.Memory  `json:"memory"`             // 当前记忆 / Current memory
	Children []*LineageNode `json:"children,omitempty"` // 衍生记忆 / Derived memories (downstream)
	Sources  []*LineageNode `json:"sources,omitempty"`  // 来源记忆 / Source memories (upstream via derived_from)
}

// LineageResponse 溯源响应 / Lineage query response
type LineageResponse struct {
	Root       *LineageNode `json:"root"`        // 起始节点 / Starting node
	TotalNodes int          `json:"total_nodes"` // 总节点数 / Total nodes in lineage
}

// maxLineageDepth 最大遍历深度防无限递归 / Max traversal depth to prevent infinite loops
const maxLineageDepth = 10

// LineageTracer 溯源查询器 / Lineage tracer
type LineageTracer struct {
	memStore store.MemoryStore
}

// NewLineageTracer 创建溯源查询器 / Create lineage tracer
func NewLineageTracer(memStore store.MemoryStore) *LineageTracer {
	return &LineageTracer{memStore: memStore}
}

// Trace 从指定记忆出发，构建完整演化链 / Build complete lineage from a given memory
// 向上追溯 derived_from 来源，向下追溯被衍生和被归纳的记忆
func (t *LineageTracer) Trace(ctx context.Context, id string, identity *model.Identity) (*LineageResponse, error) {
	if id == "" {
		return nil, fmt.Errorf("id is required: %w", model.ErrInvalidInput)
	}

	visited := make(map[string]bool)
	totalNodes := 0

	root, err := t.buildNode(ctx, id, identity, visited, &totalNodes, 0)
	if err != nil {
		return nil, err
	}

	return &LineageResponse{
		Root:       root,
		TotalNodes: totalNodes,
	}, nil
}

// buildNode 递归构建节点 / Recursively build lineage node
func (t *LineageTracer) buildNode(ctx context.Context, id string, identity *model.Identity, visited map[string]bool, total *int, depth int) (*LineageNode, error) {
	if depth > maxLineageDepth || visited[id] {
		return nil, nil
	}
	visited[id] = true

	mem, err := t.memStore.GetVisible(ctx, id, identity)
	if err != nil {
		return nil, fmt.Errorf("failed to get memory %s: %w", id, err)
	}

	*total++
	node := &LineageNode{Memory: mem}

	// 向上：通过 junction 表追溯 derived_from 来源 / Upstream: trace sources via junction table
	sourceIDs, err := t.memStore.GetDerivedFrom(ctx, id)
	if err == nil {
		for _, srcID := range sourceIDs {
			srcNode, err := t.buildNode(ctx, srcID, identity, visited, total, depth+1)
			if err != nil {
				continue // 来源不可见或已删除，跳过 / Source invisible or deleted, skip
			}
			if srcNode != nil {
				node.Sources = append(node.Sources, srcNode)
			}
		}
	}

	// 向下：查询衍生记忆 / Downstream: find derived memories
	derived, err := t.memStore.ListDerivedFrom(ctx, id, identity)
	if err == nil {
		for _, d := range derived {
			if visited[d.ID] {
				continue
			}
			childNode, err := t.buildNode(ctx, d.ID, identity, visited, total, depth+1)
			if err != nil {
				continue
			}
			if childNode != nil {
				node.Children = append(node.Children, childNode)
			}
		}
	}

	// 向下：查询归纳到当前记忆的源记忆 / Downstream: find memories consolidated into this one
	consolidated, err := t.memStore.ListConsolidatedInto(ctx, id, identity)
	if err == nil {
		for _, c := range consolidated {
			if visited[c.ID] {
				continue
			}
			childNode, err := t.buildNode(ctx, c.ID, identity, visited, total, depth+1)
			if err != nil {
				continue
			}
			if childNode != nil {
				node.Children = append(node.Children, childNode)
			}
		}
	}

	return node, nil
}
