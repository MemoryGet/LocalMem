// Package hooks Claude Code hook 工具函数 / Claude Code hook utilities
package hooks

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ShouldSkipTool 检查工具是否在黑名单中 / Check if tool is in skip list
func ShouldSkipTool(toolName string, skipTools []string) bool {
	for _, skip := range skipTools {
		if strings.EqualFold(toolName, skip) {
			return true
		}
	}
	return false
}

// FormatObservation 格式化工具调用为可读文本 / Format tool call as readable text
func FormatObservation(toolName, toolInput, toolOutput string, maxInput, maxOutput int) string {
	input := Truncate(toolInput, maxInput)
	output := Truncate(toolOutput, maxOutput)

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[%s] ", toolName))

	switch toolName {
	case "Write", "Edit", "Read":
		var parsed struct {
			FilePath string `json:"file_path"`
		}
		if json.Unmarshal([]byte(toolInput), &parsed) == nil && parsed.FilePath != "" {
			sb.WriteString(parsed.FilePath)
		} else {
			sb.WriteString(input)
		}
	case "Bash":
		var parsed struct {
			Command string `json:"command"`
		}
		if json.Unmarshal([]byte(toolInput), &parsed) == nil && parsed.Command != "" {
			sb.WriteString(fmt.Sprintf("$ %s", Truncate(parsed.Command, 200)))
			if output != "" {
				sb.WriteString(fmt.Sprintf(" -> %s", Truncate(output, 200)))
			}
		} else {
			sb.WriteString(input)
		}
	default:
		sb.WriteString(input)
	}

	return sb.String()
}

// Truncate 截断字符串到指定 rune 数 / Truncate string to max rune count
func Truncate(s string, maxChars int) string {
	if maxChars <= 0 {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxChars {
		return s
	}
	return string(runes[:maxChars]) + "..."
}
