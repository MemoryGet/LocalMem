package main

import (
	"database/sql"
	"fmt"
	_ "modernc.org/sqlite"
)

func main() {
	db, _ := sql.Open("sqlite", "data/eval_longmemeval.db")
	defer db.Close()
	var e, r, me int
	db.QueryRow("SELECT COUNT(*) FROM entities").Scan(&e)
	db.QueryRow("SELECT COUNT(*) FROM entity_relations").Scan(&r)
	db.QueryRow("SELECT COUNT(*) FROM memory_entities").Scan(&me)
	fmt.Printf("entities=%d  relations=%d  memory_entities=%d\n", e, r, me)

	var withEntity, withoutEntity int
	db.QueryRow(`SELECT COUNT(*) FROM memories m WHERE deleted_at IS NULL AND EXISTS (SELECT 1 FROM memory_entities me WHERE me.memory_id = m.id)`).Scan(&withEntity)
	db.QueryRow(`SELECT COUNT(*) FROM memories m WHERE deleted_at IS NULL AND NOT EXISTS (SELECT 1 FROM memory_entities me WHERE me.memory_id = m.id)`).Scan(&withoutEntity)
	fmt.Printf("memories with entities=%d  without=%d\n", withEntity, withoutEntity)

	// 前 2000 条（对应约前 100 道测试题的 seed 记忆）中，有多少已有实体
	var earlyWith, earlyWithout int
	db.QueryRow(`
		WITH early AS (SELECT id FROM memories WHERE deleted_at IS NULL ORDER BY created_at LIMIT 2000)
		SELECT COUNT(*) FROM early e
		WHERE EXISTS (SELECT 1 FROM memory_entities me WHERE me.memory_id = e.id)`).Scan(&earlyWith)
	db.QueryRow(`
		WITH early AS (SELECT id FROM memories WHERE deleted_at IS NULL ORDER BY created_at LIMIT 2000)
		SELECT COUNT(*) FROM early e
		WHERE NOT EXISTS (SELECT 1 FROM memory_entities me WHERE me.memory_id = e.id)`).Scan(&earlyWithout)
	fmt.Printf("first-2000 memories: with entities=%d  without=%d\n", earlyWith, earlyWithout)
}
