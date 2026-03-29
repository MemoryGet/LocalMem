package cli_test

import (
	"testing"

	"iclude/internal/hooks"

	"github.com/stretchr/testify/assert"
)

func TestShouldSkipTool(t *testing.T) {
	skipList := []string{"Glob", "Grep", "ToolSearch"}

	tests := []struct {
		name     string
		tool     string
		expected bool
	}{
		{"skip Glob", "Glob", true},
		{"skip Grep", "Grep", true},
		{"skip case insensitive", "glob", true},
		{"allow Write", "Write", false},
		{"allow Edit", "Edit", false},
		{"allow Bash", "Bash", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.expected, hooks.ShouldSkipTool(tc.tool, skipList))
		})
	}
}

func TestFormatObservation(t *testing.T) {
	tests := []struct {
		name         string
		toolName     string
		toolInput    string
		toolOutput   string
		wantContains string
	}{
		{
			name:         "Write extracts file_path",
			toolName:     "Write",
			toolInput:    `{"file_path":"/root/LocalMem/cmd/cli/main.go","content":"package main..."}`,
			toolOutput:   `{"success":true}`,
			wantContains: "/root/LocalMem/cmd/cli/main.go",
		},
		{
			name:         "Bash extracts command",
			toolName:     "Bash",
			toolInput:    `{"command":"go test ./..."}`,
			toolOutput:   `ok iclude/testing/store 0.5s`,
			wantContains: "$ go test",
		},
		{
			name:         "Read extracts file_path",
			toolName:     "Read",
			toolInput:    `{"file_path":"/root/LocalMem/internal/config/config.go"}`,
			toolOutput:   `file contents...`,
			wantContains: "/root/LocalMem/internal/config/config.go",
		},
		{
			name:         "Unknown tool uses raw input",
			toolName:     "Agent",
			toolInput:    `{"prompt":"do something"}`,
			toolOutput:   `done`,
			wantContains: `{"prompt":"do something"}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := hooks.FormatObservation(tc.toolName, tc.toolInput, tc.toolOutput, 1000, 500)
			assert.Contains(t, result, "["+tc.toolName+"]")
			assert.Contains(t, result, tc.wantContains)
		})
	}
}

func TestTruncate(t *testing.T) {
	assert.Equal(t, "abc", hooks.Truncate("abc", 10))
	assert.Equal(t, "ab...", hooks.Truncate("abcde", 2))
	assert.Equal(t, "abcde", hooks.Truncate("abcde", 0)) // 0 means no limit
	assert.Equal(t, "你好...", hooks.Truncate("你好世界测试", 2))
}
