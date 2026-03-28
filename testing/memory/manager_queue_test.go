package memory_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"

	"iclude/internal/queue"

	_ "modernc.org/sqlite"
)

func TestManager_Create_EnqueuesExtraction(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := queue.CreateTable(db); err != nil {
		t.Fatal(err)
	}

	q := queue.New(db)
	payload, _ := json.Marshal(map[string]string{"memory_id": "test-id", "content": "hello"})
	id, err := q.Enqueue(context.Background(), "entity_extract", payload)
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	task, err := q.Poll(context.Background())
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if task == nil {
		t.Fatal("expected task")
	}
	if task.ID != id {
		t.Errorf("expected id %s, got %s", id, task.ID)
	}
}
