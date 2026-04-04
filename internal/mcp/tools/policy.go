package tools

import "fmt"

var internalSourceTypes = map[string]bool{
	"reflect": true, "session_summary": true, "consolidation": true, "system": true,
}

var protectedMemoryClasses = map[string]bool{
	"core": true, "procedural": true,
}

func ValidateWritePolicy(memoryClass, sourceType string) error {
	if memoryClass == "" {
		return nil
	}
	if !protectedMemoryClasses[memoryClass] {
		return nil
	}
	if internalSourceTypes[sourceType] {
		return nil
	}
	return fmt.Errorf("write policy: external source %q cannot write memory_class %q directly", sourceType, memoryClass)
}

var allowedToolNames = map[string]bool{
	"codex": true, "claude-code": true, "cursor": true, "cline": true,
}

func ValidateToolName(toolName string) error {
	if toolName == "" {
		return nil
	}
	if allowedToolNames[toolName] {
		return nil
	}
	return fmt.Errorf("unknown tool_name %q: expected one of codex, claude-code, cursor, cline", toolName)
}

var validScopePrefixes = []string{"user/", "project/", "session/", "agent/"}

func ValidateScope(scope string) error {
	if scope == "" {
		return nil
	}
	for _, prefix := range validScopePrefixes {
		if len(scope) >= len(prefix) && scope[:len(prefix)] == prefix {
			return nil
		}
	}
	return fmt.Errorf("invalid scope %q: must start with user/, project/, session/, or agent/", scope)
}
