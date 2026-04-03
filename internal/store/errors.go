// Package store 提供存储层错误分类工具 / Storage layer error classification helpers
package store

import "strings"

// IsColumnExistsError 判断是否为列已存在错误（ALTER TABLE ADD COLUMN 幂等）
// Check if error indicates column already exists (idempotent ALTER TABLE)
func IsColumnExistsError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "duplicate column name") || strings.Contains(msg, "already exists")
}

// IsUniqueConstraintError 判断是否为唯一约束冲突 / Check if error is a UNIQUE constraint violation
func IsUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(err.Error(), "UNIQUE constraint failed")
}
