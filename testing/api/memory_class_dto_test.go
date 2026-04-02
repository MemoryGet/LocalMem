package api_test

import (
	"encoding/json"
	"testing"

	"iclude/internal/model"
)

func TestCreateMemoryRequest_MemoryClassDefault(t *testing.T) {
	tests := []struct {
		name      string
		jsonInput string
		wantClass string
	}{
		{"omitted defaults to empty", `{"content":"hello"}`, ""},
		{"explicit semantic", `{"content":"hello","memory_class":"semantic"}`, "semantic"},
		{"explicit procedural", `{"content":"hello","memory_class":"procedural"}`, "procedural"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req model.CreateMemoryRequest
			if err := json.Unmarshal([]byte(tt.jsonInput), &req); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if req.MemoryClass != tt.wantClass {
				t.Errorf("got MemoryClass=%q, want %q", req.MemoryClass, tt.wantClass)
			}
		})
	}
}

func TestCreateMemoryRequest_DerivedFrom(t *testing.T) {
	tests := []struct {
		name      string
		jsonInput string
		wantLen   int
	}{
		{"omitted is nil", `{"content":"hello"}`, 0},
		{"single source", `{"content":"hello","derived_from":["mem_abc"]}`, 1},
		{"multiple sources", `{"content":"hello","derived_from":["a","b","c"]}`, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req model.CreateMemoryRequest
			if err := json.Unmarshal([]byte(tt.jsonInput), &req); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if len(req.DerivedFrom) != tt.wantLen {
				t.Errorf("got DerivedFrom len=%d, want %d", len(req.DerivedFrom), tt.wantLen)
			}
		})
	}
}

func TestRetrieveRequest_MemoryClassFilter(t *testing.T) {
	input := `{"query":"test","memory_class":"semantic"}`
	var req model.RetrieveRequest
	if err := json.Unmarshal([]byte(input), &req); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if req.MemoryClass != "semantic" {
		t.Errorf("got MemoryClass=%q, want semantic", req.MemoryClass)
	}
}
