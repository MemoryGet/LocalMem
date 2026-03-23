// Package sqlbuilder 轻量 SQL 查询构建器 / Lightweight SQL query builder
// 仅处理 WHERE 子句拼接，可拔插，不依赖任何 ORM
package sqlbuilder

import (
	"fmt"
	"strings"
)

// WhereBuilder WHERE 子句构建器 / WHERE clause builder
type WhereBuilder struct {
	conditions []string
	args       []interface{}
}

// NewWhere 创建 WHERE 构建器 / Create a new WHERE builder
func NewWhere() *WhereBuilder {
	return &WhereBuilder{}
}

// And 添加 AND 条件 / Add an AND condition
func (w *WhereBuilder) And(condition string, args ...interface{}) *WhereBuilder {
	w.conditions = append(w.conditions, condition)
	w.args = append(w.args, args...)
	return w
}

// AndIf 条件满足时添加 AND / Add AND condition only if predicate is true
func (w *WhereBuilder) AndIf(predicate bool, condition string, args ...interface{}) *WhereBuilder {
	if predicate {
		w.And(condition, args...)
	}
	return w
}

// Build 构建 WHERE 子句和参数 / Build WHERE clause and args
// 返回 "cond1 AND cond2 AND ..." 和对应参数
func (w *WhereBuilder) Build() (string, []interface{}) {
	if len(w.conditions) == 0 {
		return "1=1", nil
	}
	return strings.Join(w.conditions, " AND "), w.args
}

// SelectBuilder 简单 SELECT 构建器 / Simple SELECT builder
type SelectBuilder struct {
	columns  string
	from     string
	joins    []string
	where    *WhereBuilder
	orderBy  string
	limitVal int
}

// Select 创建 SELECT 构建器 / Create a SELECT builder
func Select(columns string) *SelectBuilder {
	return &SelectBuilder{
		columns: columns,
		where:   NewWhere(),
	}
}

// From 设置表名 / Set table name
func (s *SelectBuilder) From(table string) *SelectBuilder {
	s.from = table
	return s
}

// Join 添加 JOIN 子句 / Add JOIN clause
func (s *SelectBuilder) Join(join string) *SelectBuilder {
	s.joins = append(s.joins, join)
	return s
}

// Where 获取 WHERE 构建器 / Get WHERE builder
func (s *SelectBuilder) Where() *WhereBuilder {
	return s.where
}

// OrderBy 设置排序 / Set ORDER BY
func (s *SelectBuilder) OrderBy(orderBy string) *SelectBuilder {
	s.orderBy = orderBy
	return s
}

// Limit 设置限制 / Set LIMIT
func (s *SelectBuilder) Limit(limit int) *SelectBuilder {
	s.limitVal = limit
	return s
}

// Build 构建完整 SQL 和参数 / Build complete SQL and args
func (s *SelectBuilder) Build() (string, []interface{}) {
	var sb strings.Builder
	var allArgs []interface{}

	sb.WriteString(fmt.Sprintf("SELECT %s FROM %s", s.columns, s.from))

	for _, j := range s.joins {
		sb.WriteString(" ")
		sb.WriteString(j)
	}

	whereClause, whereArgs := s.where.Build()
	sb.WriteString(" WHERE ")
	sb.WriteString(whereClause)
	allArgs = append(allArgs, whereArgs...)

	if s.orderBy != "" {
		sb.WriteString(" ORDER BY ")
		sb.WriteString(s.orderBy)
	}

	if s.limitVal > 0 {
		sb.WriteString(" LIMIT ?")
		allArgs = append(allArgs, s.limitVal)
	}

	return sb.String(), allArgs
}
