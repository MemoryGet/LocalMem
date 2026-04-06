# Protocol Compliance Gaps Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the remaining gaps between the Unified AI Tool Integration Protocol spec and the actual codebase, bringing Claude Code to full L4 compliance.

**Architecture:** Add write policy guard in retain/ingest tools, enrich capture metadata with `capture_mode` and `host_tool`, add scope validation helper, and stabilize project_id via directory hashing. All changes are additive — no existing interfaces change.

**Tech Stack:** Go 1.25+, SQLite, MCP protocol, existing store/runtime layers

---

## File Structure

| File | Action | Responsibility |
|------|--------|----------------|
| `pkg/identity/project_id.go` | Create | Stable project_id generation (git-remote hash or dir hash) |
| `internal/mcp/tools/policy.go` | Create | Write policy validation (memory_class guard + scope validation + tool_name enum) |
| `internal/mcp/tools/retain.go` | Modify | Add write policy check + metadata enrichment |
| `cmd/cli/hook_capture.go` | Modify | Add `capture_mode` + `host_tool` metadata |
| `cmd/cli/hook_session_start.go` | Modify | Use stable project_id hash |
| `cmd/cli/hook_session_stop.go` | Modify | Add `capture_mode` metadata |
| `testing/compliance/policy_test.go` | Create | Write policy + scope validation tests |
| `testing/compliance/identity_test.go` | Create | Project ID hashing + tool_name validation tests |

---

### Task 1: Write Policy Guard

**Files:**
- Create: `internal/mcp/tools/policy.go`
- Test: `testing/compliance/policy_test.go`

- [ ] **Step 1: Write the failing test for memory_class write guard**

```go
// testing/compliance/policy_test.go
package compliance_test

import (
    "testing"

    "iclude/internal/mcp/tools"

    "github.com/stretchr/testify/assert"
)

func TestWritePolicy_BlocksCoreFromExternal(t *testing.T) {
    err := tools.ValidateWritePolicy("core", "hook")
    assert.Error(t, err)
}

func TestWritePolicy_BlocksProceduralFromExternal(t *testing.T) {
    err := tools.ValidateWritePolicy("procedural", "hook")
    assert.Error(t, err)
}

func TestWritePolicy_AllowsEpisodicFromExternal(t *testing.T) {
    err := tools.ValidateWritePolicy("episodic", "hook")
    assert.NoError(t, err)
}

func TestWritePolicy_AllowsEmptyClassFromExternal(t *testing.T) {
    err := tools.ValidateWritePolicy("", "hook")
    assert.NoError(t, err)
}

func TestWritePolicy_AllowsCoreFromInternal(t *testing.T) {
    err := tools.ValidateWritePolicy("core", "reflect")
    assert.NoError(t, err)
}

func TestWritePolicy_AllowsProceduralFromInternal(t *testing.T) {
    err := tools.ValidateWritePolicy("procedural", "session_summary")
    assert.NoError(t, err)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -count=1 -run TestWritePolicy ./testing/compliance/...`
Expected: FAIL — `ValidateWritePolicy` not defined

- [ ] **Step 3: Implement write policy validation**

```go
// internal/mcp/tools/policy.go
package tools

import "fmt"

// internalSourceTypes 内部来源类型（允许写 core/procedural）/ Internal source types allowed to write core/procedural
var internalSourceTypes = map[string]bool{
    "reflect":         true,
    "session_summary": true,
    "consolidation":   true,
    "system":          true,
}

// protectedMemoryClasses 受保护的记忆类别 / Protected memory classes (external tools cannot write)
var protectedMemoryClasses = map[string]bool{
    "core":       true,
    "procedural": true,
}

// ValidateWritePolicy 校验写入策略 / Validate write policy for memory_class + source_type
// 外部工具不允许直接写 core/procedural / External tools cannot write core/procedural directly
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

// allowedToolNames 允许的宿主工具名 / Allowed host tool names
var allowedToolNames = map[string]bool{
    "codex":      true,
    "claude-code": true,
    "cursor":     true,
    "cline":      true,
}

// ValidateToolName 校验工具名是否合法 / Validate tool name against known enum
func ValidateToolName(toolName string) error {
    if toolName == "" {
        return nil // 空值不强制 / Empty is allowed
    }
    if allowedToolNames[toolName] {
        return nil
    }
    return fmt.Errorf("unknown tool_name %q: expected one of codex, claude-code, cursor, cline", toolName)
}

// validScopePrefixes 合法 scope 前缀 / Valid scope prefixes
var validScopePrefixes = []string{
    "user/",
    "project/",
    "session/",
    "agent/",
}

// ValidateScope 校验 scope 格式 / Validate scope format against allowed prefixes
func ValidateScope(scope string) error {
    if scope == "" {
        return nil // 空 scope 允许 / Empty scope is allowed
    }
    for _, prefix := range validScopePrefixes {
        if len(scope) >= len(prefix) && scope[:len(prefix)] == prefix {
            return nil
        }
    }
    return fmt.Errorf("invalid scope %q: must start with user/, project/, session/, or agent/", scope)
}
```

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test -count=1 -run TestWritePolicy ./testing/compliance/...`
Expected: PASS

- [ ] **Step 5: Add scope validation tests**

Append to `testing/compliance/policy_test.go`:

```go
func TestValidateScope_EmptyAllowed(t *testing.T) {
    assert.NoError(t, tools.ValidateScope(""))
}

func TestValidateScope_ValidPrefixes(t *testing.T) {
    for _, scope := range []string{"user/u1/prefs", "project/p1/status", "session/s1/thread", "agent/a1/scratch"} {
        assert.NoError(t, tools.ValidateScope(scope), "scope %s should be valid", scope)
    }
}

func TestValidateScope_InvalidPrefix(t *testing.T) {
    assert.Error(t, tools.ValidateScope("random/scope"))
    assert.Error(t, tools.ValidateScope("global"))
}

func TestValidateToolName_Known(t *testing.T) {
    for _, name := range []string{"codex", "claude-code", "cursor", "cline"} {
        assert.NoError(t, tools.ValidateToolName(name))
    }
}

func TestValidateToolName_Unknown(t *testing.T) {
    assert.Error(t, tools.ValidateToolName("unknown-tool"))
}

func TestValidateToolName_Empty(t *testing.T) {
    assert.NoError(t, tools.ValidateToolName(""))
}
```

- [ ] **Step 6: Run all policy tests**

Run: `go test -count=1 -v -run "TestWritePolicy|TestValidateScope|TestValidateToolName" ./testing/compliance/...`
Expected: All PASS

- [ ] **Step 7: Commit**

```bash
git add internal/mcp/tools/policy.go testing/compliance/policy_test.go
git commit -m "feat(policy): write policy guard + scope validation + tool_name enum"
```

---

### Task 2: Integrate Write Policy into Retain Tool

**Files:**
- Modify: `internal/mcp/tools/retain.go:58-87`

- [ ] **Step 1: Add memory_class field to retainArgs**

In `retain.go`, add `MemoryClass` to `retainArgs`:

```go
type retainArgs struct {
    Content     string            `json:"content"`
    Scope       string            `json:"scope,omitempty"`
    Kind        string            `json:"kind,omitempty"`
    MemoryClass string            `json:"memory_class,omitempty"`
    Tags        []string          `json:"tags,omitempty"`
    Metadata    map[string]string `json:"metadata,omitempty"`
    ContextID   string            `json:"context_id,omitempty"`
    SourceType  string            `json:"source_type,omitempty"`
    MessageRole string            `json:"message_role,omitempty"`
}
```

- [ ] **Step 2: Add write policy + scope validation in Execute**

In `retain.go` Execute method, after argument parsing and before creating Memory:

```go
// 写入策略校验 / Write policy check
if err := ValidateWritePolicy(args.MemoryClass, args.SourceType); err != nil {
    return mcp.ErrorResult(err.Error()), nil
}
// Scope 格式校验 / Scope format validation
if err := ValidateScope(args.Scope); err != nil {
    return mcp.ErrorResult(err.Error()), nil
}
```

And set `mem.MemoryClass = args.MemoryClass` when building the Memory struct.

- [ ] **Step 3: Build and verify**

Run: `go vet ./internal/mcp/tools/... && go build ./cmd/mcp/`
Expected: No errors

- [ ] **Step 4: Commit**

```bash
git add internal/mcp/tools/retain.go
git commit -m "feat(retain): integrate write policy guard + scope validation"
```

---

### Task 3: Enrich Capture Metadata

**Files:**
- Modify: `cmd/cli/hook_capture.go:53-63`

- [ ] **Step 1: Add `capture_mode` and `host_tool` to capture metadata**

In `hook_capture.go`, update the metadata block:

```go
metadata := map[string]string{
    "tool_name":    hookInput.ToolName,
    "tool_use_id":  hookInput.ToolUseID,
    "session_id":   hookInput.SessionID,
    "host_tool":    "claude-code",
    "capture_mode": "auto",
}
```

- [ ] **Step 2: Build CLI**

Run: `go build ./cmd/cli/...`
Expected: No errors

- [ ] **Step 3: Add host_tool to session_stop fallback metadata**

In `hook_session_stop.go` `fallbackRetainSummary`, update metadata:

```go
"metadata": map[string]string{
    "session_id":   hookInput.SessionID,
    "host_tool":    "claude-code",
    "capture_mode": "auto",
},
```

- [ ] **Step 4: Commit**

```bash
git add cmd/cli/hook_capture.go cmd/cli/hook_session_stop.go
git commit -m "fix(hooks): add capture_mode + host_tool metadata per protocol §10.3"
```

---

### Task 4: Stable Project ID

**Files:**
- Create: `pkg/identity/project_id.go`
- Test: `testing/compliance/identity_test.go`
- Modify: `cmd/cli/hook_session_start.go`

- [ ] **Step 1: Write failing test for project_id hashing**

```go
// testing/compliance/identity_test.go
package compliance_test

import (
    "testing"

    "iclude/pkg/identity"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestResolveProjectID_SamePathSameID(t *testing.T) {
    id1 := identity.ResolveProjectID("/home/user/project")
    id2 := identity.ResolveProjectID("/home/user/project")
    assert.Equal(t, id1, id2)
}

func TestResolveProjectID_DifferentPathDifferentID(t *testing.T) {
    id1 := identity.ResolveProjectID("/home/user/project-a")
    id2 := identity.ResolveProjectID("/home/user/project-b")
    assert.NotEqual(t, id1, id2)
}

func TestResolveProjectID_HasPrefix(t *testing.T) {
    id := identity.ResolveProjectID("/home/user/project")
    require.True(t, len(id) > 0)
    assert.Contains(t, id, "p_")
}

func TestResolveProjectID_EmptyPath(t *testing.T) {
    id := identity.ResolveProjectID("")
    assert.Equal(t, "", id)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -count=1 -run TestResolveProjectID ./testing/compliance/...`
Expected: FAIL — package `identity` not found

- [ ] **Step 3: Implement project_id resolver**

```go
// pkg/identity/project_id.go
package identity

import (
    "crypto/sha256"
    "fmt"
)

// ResolveProjectID 根据目录路径生成稳定 project_id / Generate stable project_id from directory path
// 格式: p_{sha256_prefix_12} / Format: p_{sha256_prefix_12}
func ResolveProjectID(projectDir string) string {
    if projectDir == "" {
        return ""
    }
    hash := sha256.Sum256([]byte(projectDir))
    return fmt.Sprintf("p_%x", hash[:6])
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -count=1 -run TestResolveProjectID ./testing/compliance/...`
Expected: PASS

- [ ] **Step 5: Integrate into hook_session_start.go**

In `hook_session_start.go`, import `identity` and use it when calling `create_session`:

Replace direct `hookInput.CWD` usage with `identity.ResolveProjectID(hookInput.CWD)` for the project_id field.

- [ ] **Step 6: Build and verify**

Run: `go build ./cmd/cli/...`
Expected: No errors

- [ ] **Step 7: Commit**

```bash
git add pkg/identity/project_id.go testing/compliance/identity_test.go cmd/cli/hook_session_start.go
git commit -m "feat(identity): stable project_id via directory hash + integrate into hooks"
```

---

### Task 5: Full Build + Regression Test

**Files:** None (verification only)

- [ ] **Step 1: Vet all packages**

Run: `go vet ./...`
Expected: No errors

- [ ] **Step 2: Build all binaries**

Run: `go build ./cmd/server/ && go build ./cmd/mcp/ && go build ./cmd/cli/...`
Expected: No errors

- [ ] **Step 3: Run all tests**

Run: `go test -count=1 ./testing/... -timeout 5m`
Expected: All PASS (except eval which needs LLM)

- [ ] **Step 4: Final commit if any fixes needed**

---

## Spec Coverage Check

| Spec Section | Requirement | Task |
|-------------|-------------|------|
| §4.4 | External tools cannot write core/procedural | Task 1 + Task 2 |
| §7.1 | project_id must be stable hash | Task 4 |
| §7.1 | tool_name must be fixed enum | Task 1 |
| §7.3 | Scope must follow prefix convention | Task 1 + Task 2 |
| §10.3 | capture_mode metadata required | Task 3 |
| §10.3 | host_tool metadata required | Task 3 |
| §15.1 | No direct external core/procedural writes | Task 1 + Task 2 |
| §15.2 | Scope whitelist validation | Task 1 + Task 2 |
