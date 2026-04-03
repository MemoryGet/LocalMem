package memory

import (
	"context"
	"fmt"

	"iclude/internal/model"
	"iclude/internal/store"
)

// ContextManager 上下文管理器 / Context management logic
type ContextManager struct {
	contextStore store.ContextStore
}

// NewContextManager 创建上下文管理器 / Create context manager
func NewContextManager(contextStore store.ContextStore) *ContextManager {
	return &ContextManager{contextStore: contextStore}
}

// Create 创建上下文 / Create a new context
func (m *ContextManager) Create(ctx context.Context, req *model.CreateContextRequest) (*model.Context, error) {
	if req.Name == "" {
		return nil, fmt.Errorf("name is required: %w", model.ErrInvalidInput)
	}
	c := &model.Context{
		Name:        req.Name,
		ParentID:    req.ParentID,
		Scope:       req.Scope,
		ContextType: req.ContextType,
		Description: req.Description,
		Mission:     req.Mission,
		Directives:  req.Directives,
		Disposition: req.Disposition,
		Metadata:    req.Metadata,
		SortOrder:   req.SortOrder,
	}
	if err := m.contextStore.Create(ctx, c); err != nil {
		return nil, fmt.Errorf("failed to create context: %w", err)
	}
	return c, nil
}

// Get 获取上下文 / Get a context by ID
func (m *ContextManager) Get(ctx context.Context, id string) (*model.Context, error) {
	if id == "" {
		return nil, fmt.Errorf("id is required: %w", model.ErrInvalidInput)
	}
	return m.contextStore.Get(ctx, id)
}

// GetByPath 通过路径获取上下文 / Get context by path
func (m *ContextManager) GetByPath(ctx context.Context, path string) (*model.Context, error) {
	if path == "" {
		return nil, fmt.Errorf("path is required: %w", model.ErrInvalidInput)
	}
	return m.contextStore.GetByPath(ctx, path)
}

// Update 更新上下文 / Update a context
func (m *ContextManager) Update(ctx context.Context, id string, req *model.UpdateContextRequest) (*model.Context, error) {
	if id == "" {
		return nil, fmt.Errorf("id is required: %w", model.ErrInvalidInput)
	}
	c, err := m.contextStore.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	if req.Name != nil {
		c.Name = *req.Name
	}
	if req.Description != nil {
		c.Description = *req.Description
	}
	if req.ContextType != nil {
		c.ContextType = *req.ContextType
	}
	if req.Metadata != nil {
		c.Metadata = req.Metadata
	}
	if req.Mission != nil {
		c.Mission = *req.Mission
	}
	if req.Directives != nil {
		c.Directives = *req.Directives
	}
	if req.Disposition != nil {
		c.Disposition = *req.Disposition
	}
	if req.SortOrder != nil {
		c.SortOrder = *req.SortOrder
	}
	if err := m.contextStore.Update(ctx, c); err != nil {
		return nil, fmt.Errorf("failed to update context: %w", err)
	}
	return c, nil
}

// Delete 删除上下文 / Delete a context
func (m *ContextManager) Delete(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("id is required: %w", model.ErrInvalidInput)
	}
	return m.contextStore.Delete(ctx, id)
}

// ListChildren 列出子上下文 / List child contexts
func (m *ContextManager) ListChildren(ctx context.Context, parentID string) ([]*model.Context, error) {
	return m.contextStore.ListChildren(ctx, parentID)
}

// ListSubtree 列出子树 / List subtree
func (m *ContextManager) ListSubtree(ctx context.Context, id string) ([]*model.Context, error) {
	c, err := m.contextStore.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	return m.contextStore.ListSubtree(ctx, c.Path)
}

// Move 移动上下文 / Move context to new parent
func (m *ContextManager) Move(ctx context.Context, id string, newParentID string) error {
	if id == "" {
		return fmt.Errorf("id is required: %w", model.ErrInvalidInput)
	}
	// 验证无环引用：如果 newParentID 非空，确保它不在 id 的子树中
	if newParentID != "" {
		c, err := m.contextStore.Get(ctx, id)
		if err != nil {
			return err
		}
		parent, err := m.contextStore.Get(ctx, newParentID)
		if err != nil {
			return err
		}
		// 检查 parent 的路径不在当前节点子树内
		if len(parent.Path) > len(c.Path) && parent.Path[:len(c.Path)+1] == c.Path+"/" {
			return model.ErrCircularReference
		}
	}
	return m.contextStore.Move(ctx, id, newParentID)
}
