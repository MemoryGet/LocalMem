// Package memory 记忆管理业务逻辑 / Memory management business logic
package memory

import (
	"context"

	"iclude/internal/model"
	"iclude/internal/store"
)

// TagManager 标签管理器（封装 TagStore + 记忆归属校验）/ Tag manager wrapping TagStore with memory ownership checks
type TagManager struct {
	tagStore  store.TagStore
	memReader store.MemoryReader
}

// NewTagManager 创建标签管理器 / Create a new tag manager
func NewTagManager(tagStore store.TagStore, memReader store.MemoryReader) *TagManager {
	return &TagManager{tagStore: tagStore, memReader: memReader}
}

// CreateTag 创建标签 / Create a tag
func (m *TagManager) CreateTag(ctx context.Context, tag *model.Tag) error {
	return m.tagStore.CreateTag(ctx, tag)
}

// GetTag 获取标签 / Get tag by ID
func (m *TagManager) GetTag(ctx context.Context, id string) (*model.Tag, error) {
	return m.tagStore.GetTag(ctx, id)
}

// ListTags 列出标签 / List all tags with optional scope filter
func (m *TagManager) ListTags(ctx context.Context, scope string) ([]*model.Tag, error) {
	return m.tagStore.ListTags(ctx, scope)
}

// DeleteTag 删除标签 / Delete a tag
func (m *TagManager) DeleteTag(ctx context.Context, id string) error {
	return m.tagStore.DeleteTag(ctx, id)
}

// TagMemory 给记忆打标签 / Associate a tag with a memory
func (m *TagManager) TagMemory(ctx context.Context, memoryID, tagID string) error {
	return m.tagStore.TagMemory(ctx, memoryID, tagID)
}

// UntagMemory 移除记忆标签 / Remove tag from memory
func (m *TagManager) UntagMemory(ctx context.Context, memoryID, tagID string) error {
	return m.tagStore.UntagMemory(ctx, memoryID, tagID)
}

// GetMemoryTags 获取记忆的所有标签 / Get all tags for a memory
func (m *TagManager) GetMemoryTags(ctx context.Context, memoryID string) ([]*model.Tag, error) {
	return m.tagStore.GetMemoryTags(ctx, memoryID)
}

// GetVisible 带可见性校验获取记忆（代理 MemoryReader）/ Get memory with visibility check (proxy to MemoryReader)
func (m *TagManager) GetVisible(ctx context.Context, id string, identity *model.Identity) (*model.Memory, error) {
	return m.memReader.GetVisible(ctx, id, identity)
}
