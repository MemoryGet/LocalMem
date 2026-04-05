package memory

import (
	"context"
	"fmt"

	"iclude/internal/logger"
	"iclude/internal/model"
	"iclude/internal/store"

	"go.uber.org/zap"
)

// CoreBlockKind 核心记忆块类型 / Core memory block kinds
const (
	CoreUserProfile         = "user_profile"
	CoreUserPreferences     = "user_preferences"
	CoreActiveGoals         = "active_goals"
	CoreCurrentProjectState = "current_project_state"
	CoreOperatingRules      = "operating_rules"
)

// AllCoreBlocks 所有核心记忆块类型 / All core block kinds
var AllCoreBlocks = []string{
	CoreUserProfile,
	CoreUserPreferences,
	CoreActiveGoals,
	CoreCurrentProjectState,
	CoreOperatingRules,
}

// CoreBlockLimits 核心记忆块约束 / Core memory block constraints
const (
	MaxCoreBlocksPerScope = 5    // 每个 scope 最多 5 个 core block / Max core blocks per scope
	MaxCoreContentRunes   = 500  // 每个 core block 最大内容长度（rune）/ Max content length per block
)

// CoreManager 核心记忆管理器 / Core memory manager
type CoreManager struct {
	memStore store.MemoryStore
}

// NewCoreManager 创建核心记忆管理器 / Create core memory manager
func NewCoreManager(memStore store.MemoryStore) *CoreManager {
	return &CoreManager{memStore: memStore}
}

// GetCoreBlocks 获取指定 scope 下的所有 core blocks / Get all core blocks for a scope
func (c *CoreManager) GetCoreBlocks(ctx context.Context, scope string, identity *model.Identity) ([]*model.Memory, error) {
	if scope == "" {
		return nil, nil
	}

	memories, err := c.memStore.ListCoreByScope(ctx, scope, identity, MaxCoreBlocksPerScope)
	if err != nil {
		return nil, fmt.Errorf("get core blocks: %w", err)
	}

	return memories, nil
}

// GetCoreBlocksMultiScope 获取多个 scope 的 core blocks（按优先级合并）/ Get core blocks from multiple scopes
func (c *CoreManager) GetCoreBlocksMultiScope(ctx context.Context, scopes []string, identity *model.Identity) ([]*model.Memory, error) {
	var all []*model.Memory
	seen := make(map[string]bool)

	for _, scope := range scopes {
		blocks, err := c.GetCoreBlocks(ctx, scope, identity)
		if err != nil {
			logger.Debug("core blocks fetch failed for scope, skipping",
				zap.String("scope", scope),
				zap.Error(err),
			)
			continue
		}
		for _, m := range blocks {
			if !seen[m.ID] {
				seen[m.ID] = true
				all = append(all, m)
			}
		}
	}

	return all, nil
}

// ValidateCoreWrite 验证 core block 写入约束 / Validate core block write constraints
func ValidateCoreWrite(mem *model.Memory) error {
	if mem.MemoryClass != "core" {
		return nil
	}

	// 检查 sub_kind 是否是有效的 core block 类型 / Validate sub_kind is a valid core block
	valid := false
	for _, kind := range AllCoreBlocks {
		if mem.SubKind == kind {
			valid = true
			break
		}
	}
	if mem.SubKind != "" && !valid {
		return fmt.Errorf("invalid core block sub_kind %q: expected one of %v", mem.SubKind, AllCoreBlocks)
	}

	// 检查内容长度 / Validate content length
	if len([]rune(mem.Content)) > MaxCoreContentRunes {
		return fmt.Errorf("core block content exceeds %d runes limit", MaxCoreContentRunes)
	}

	return nil
}
