package report_test

import (
	"fmt"
	"testing"

	"iclude/pkg/sqlbuilder"
	"iclude/pkg/testreport"

	"github.com/stretchr/testify/assert"
)

const (
	suiteSQLBuilder     = "SQL Builder 查询构建"
	suiteSQLBuilderIcon = "\U0001F3D7"
	suiteSQLBuilderDesc = "优化点 #6: 轻量 SQL 查询构建器替代字符串拼接，提升可读性与安全性"
)

func TestSQLBuilder_WhereEmpty(t *testing.T) {
	tc := testreport.NewCase(t, suiteSQLBuilder, suiteSQLBuilderIcon, suiteSQLBuilderDesc,
		"WhereBuilder 空条件")
	defer tc.Done()

	tc.Input("条件数", "0")

	wb := sqlbuilder.NewWhere()
	tc.Step("创建 WhereBuilder")

	clause, args := wb.Build()
	tc.Step("调用 Build()")

	assert.Equal(t, "1=1", clause)
	assert.Len(t, args, 0)
	tc.Step("验证: 空条件返回 '1=1'", "确保 SQL 合法性")

	tc.OutputCode("WHERE", clause)
	tc.Output("args", fmt.Sprintf("%v (len=%d)", args, len(args)))
}

func TestSQLBuilder_WhereSingleCondition(t *testing.T) {
	tc := testreport.NewCase(t, suiteSQLBuilder, suiteSQLBuilderIcon, suiteSQLBuilderDesc,
		"WhereBuilder 单条件")
	defer tc.Done()

	tc.InputSQL("条件", "scope = ?")
	tc.Input("参数", "tech")

	wb := sqlbuilder.NewWhere()
	wb.And("scope = ?", "tech")
	tc.Step("添加条件: scope = ?", "args: tech")

	clause, args := wb.Build()
	tc.Step("构建 WHERE 子句")

	assert.Equal(t, "scope = ?", clause)
	assert.Len(t, args, 1)
	assert.Equal(t, "tech", args[0])
	tc.Step("验证: 单条件直接输出，参数正确绑定")

	tc.OutputCode("WHERE", clause)
	tc.Output("args", fmt.Sprintf("%v", args))
}

func TestSQLBuilder_WhereMultipleConditions(t *testing.T) {
	tc := testreport.NewCase(t, suiteSQLBuilder, suiteSQLBuilderIcon, suiteSQLBuilderDesc,
		"WhereBuilder 多条件 AND 组合")
	defer tc.Done()

	tc.InputSQL("条件1", "scope = ?")
	tc.InputSQL("条件2", "kind = ?")
	tc.InputSQL("条件3", "deleted_at IS NULL")

	wb := sqlbuilder.NewWhere()
	wb.And("scope = ?", "tech")
	tc.Step("添加: scope = 'tech'")

	wb.And("kind = ?", "fact")
	tc.Step("添加: kind = 'fact'")

	wb.And("deleted_at IS NULL")
	tc.Step("添加: deleted_at IS NULL (无参数)")

	clause, args := wb.Build()
	tc.Step("构建 WHERE 子句")

	assert.Equal(t, "scope = ? AND kind = ? AND deleted_at IS NULL", clause)
	assert.Len(t, args, 2)
	tc.Step("验证: 三个条件用 AND 连接，参数按序绑定")

	tc.OutputCode("WHERE", clause)
	tc.Output("args", fmt.Sprintf("%v (len=%d)", args, len(args)))
}

func TestSQLBuilder_WhereAndIf(t *testing.T) {
	tc := testreport.NewCase(t, suiteSQLBuilder, suiteSQLBuilderIcon, suiteSQLBuilderDesc,
		"WhereBuilder AndIf 条件过滤")
	defer tc.Done()

	scope := "tech"
	kind := ""
	tc.Input("scope", scope+" (非空，应添加)")
	tc.Input("kind", "(空字符串，应跳过)")

	wb := sqlbuilder.NewWhere()
	wb.And("deleted_at IS NULL")
	tc.Step("添加基础条件: deleted_at IS NULL")

	wb.AndIf(scope != "", "scope = ?", scope)
	tc.Step("AndIf(scope!='', ...) -> 添加", fmt.Sprintf("predicate=true, scope=%q", scope))

	wb.AndIf(kind != "", "kind = ?", kind)
	tc.Step("AndIf(kind!='', ...) -> 跳过", "predicate=false")

	clause, args := wb.Build()
	tc.Step("构建 WHERE 子句")

	assert.Equal(t, "deleted_at IS NULL AND scope = ?", clause)
	assert.Len(t, args, 1)
	tc.Step("验证: 仅 scope 条件被添加，kind 条件被跳过")

	tc.OutputCode("WHERE", clause)
	tc.Output("args", fmt.Sprintf("%v", args))
	tc.Output("规则", "AndIf(false, ...) 不添加条件，不添加参数")
}

func TestSQLBuilder_SelectComplete(t *testing.T) {
	tc := testreport.NewCase(t, suiteSQLBuilder, suiteSQLBuilderIcon, suiteSQLBuilderDesc,
		"SelectBuilder 完整查询构建")
	defer tc.Done()

	tc.InputSQL("列", "id, content, scope")
	tc.InputSQL("表", "memories")
	tc.InputSQL("排序", "created_at DESC")
	tc.Input("分页", "LIMIT 10")

	qb := sqlbuilder.Select("id, content, scope").
		From("memories").
		OrderBy("created_at DESC").
		Limit(10)
	tc.Step("创建 SelectBuilder: SELECT ... FROM memories")

	qb.Where().And("deleted_at IS NULL")
	tc.Step("添加 WHERE: deleted_at IS NULL")

	qb.Where().AndIf(true, "scope = ?", "tech")
	tc.Step("添加 WHERE: scope = 'tech' (AndIf=true)")

	qb.Where().AndIf(false, "kind = ?", "ignored")
	tc.Step("跳过 WHERE: kind (AndIf=false)")

	sql, args := qb.Build()
	tc.Step("构建完整 SQL")

	assert.Contains(t, sql, "SELECT id, content, scope FROM memories")
	assert.Contains(t, sql, "WHERE deleted_at IS NULL AND scope = ?")
	assert.Contains(t, sql, "ORDER BY created_at DESC")
	assert.Contains(t, sql, "LIMIT ?")
	assert.Len(t, args, 2)
	assert.Equal(t, "tech", args[0])
	assert.Equal(t, 10, args[1])
	tc.Step("验证: SQL 结构完整，参数按序绑定")

	tc.OutputCode("SQL", sql)
	tc.Output("args", fmt.Sprintf("%v", args))
}

func TestSQLBuilder_SelectWithJoin(t *testing.T) {
	tc := testreport.NewCase(t, suiteSQLBuilder, suiteSQLBuilderIcon, suiteSQLBuilderDesc,
		"SelectBuilder JOIN 查询")
	defer tc.Done()

	tc.InputSQL("主表", "memories m")
	tc.InputSQL("JOIN", "memories_fts f ON m.rowid = f.rowid")

	qb := sqlbuilder.Select("m.id, m.content").
		From("memories m").
		Join("JOIN memories_fts f ON m.rowid = f.rowid").
		OrderBy("bm25(memories_fts, 10, 5, 3)").
		Limit(10)
	tc.Step("创建 SelectBuilder with JOIN")

	qb.Where().And("memories_fts MATCH ?", "检 索")
	tc.Step("添加 FTS5 MATCH 条件")

	qb.Where().And("m.deleted_at IS NULL")
	tc.Step("添加软删除过滤")

	sql, args := qb.Build()
	tc.Step("构建完整 SQL")

	assert.Contains(t, sql, "JOIN memories_fts f ON m.rowid = f.rowid")
	assert.Contains(t, sql, "memories_fts MATCH ?")
	assert.Len(t, args, 2) // "检 索" + limit 10
	tc.Step("验证: JOIN 和 MATCH 子句正确拼接")

	tc.OutputCode("SQL", sql)
	tc.Output("args", fmt.Sprintf("%v", args))
}
