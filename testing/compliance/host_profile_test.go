package compliance_test

import (
	"testing"

	"iclude/internal/mcp/tools"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestAllKnownToolsHaveProfiles 所有白名单工具必须有 profile / All whitelisted tools must have profiles
func TestAllKnownToolsHaveProfiles(t *testing.T) {
	for _, name := range []string{"claude-code", "codex", "cursor", "cline"} {
		t.Run(name, func(t *testing.T) {
			p := tools.GetProfile(name)
			require.NotNil(t, p, "profile for %s must exist", name)
			assert.Equal(t, name, p.Name)
			assert.NotEmpty(t, p.DefaultScope)
			assert.NotEmpty(t, p.AllowedScopes)
			assert.NotEmpty(t, p.CaptureMode)
			assert.Greater(t, p.MaxToolTimeout, 0)
		})
	}
}

// TestUnknownToolHasNoProfile 未知工具返回 nil / Unknown tool returns nil profile
func TestUnknownToolHasNoProfile(t *testing.T) {
	assert.Nil(t, tools.GetProfile("unknown-tool"))
}

// TestAllProfilesPassToolNameValidation profile 名必须通过白名单校验 / Profile names pass validation
func TestAllProfilesPassToolNameValidation(t *testing.T) {
	for name := range tools.KnownProfiles {
		assert.NoError(t, tools.ValidateToolName(name), "tool %s should pass validation", name)
	}
}

// TestAllProfileScopesAreValid profile 中的 scope 前缀必须通过 ValidateScope / Profile scopes pass validation
func TestAllProfileScopesAreValid(t *testing.T) {
	for name, p := range tools.KnownProfiles {
		for _, scope := range p.AllowedScopes {
			// scope 前缀以 / 结尾，加个测试值 / Scope prefix ends with /, append test value
			testScope := scope + "test"
			assert.NoError(t, tools.ValidateScope(testScope), "tool %s scope %s should be valid", name, testScope)
		}
	}
}

// TestProfileCapabilities_ClaudeCode Claude Code 特定能力 / Claude Code specific capabilities
func TestProfileCapabilities_ClaudeCode(t *testing.T) {
	p := tools.GetProfile("claude-code")
	require.NotNil(t, p)
	assert.True(t, p.SupportsHooks, "claude-code must support hooks")
	assert.True(t, p.SupportsStdio, "claude-code must support stdio")
	assert.True(t, p.SupportsSSE, "claude-code must support SSE")
}

// TestProfileCapabilities_Codex Codex 特定能力 / Codex specific capabilities
func TestProfileCapabilities_Codex(t *testing.T) {
	p := tools.GetProfile("codex")
	require.NotNil(t, p)
	assert.False(t, p.SupportsHooks, "codex does not support native hooks")
	assert.True(t, p.SupportsStdio, "codex must support stdio")
}

// TestProfileCapabilities_Cursor Cursor 特定能力 / Cursor specific capabilities
func TestProfileCapabilities_Cursor(t *testing.T) {
	p := tools.GetProfile("cursor")
	require.NotNil(t, p)
	assert.True(t, p.SupportsStdio, "cursor must support stdio")
	assert.True(t, p.SupportsSSE, "cursor must support SSE")
}

// TestProfileCapabilities_Cline Cline 特定能力 / Cline specific capabilities
func TestProfileCapabilities_Cline(t *testing.T) {
	p := tools.GetProfile("cline")
	require.NotNil(t, p)
	assert.True(t, p.SupportsStdio, "cline must support stdio")
}

// TestProfileConsistency_AllSupportStdio 所有宿主至少支持 stdio / All hosts must support stdio
func TestProfileConsistency_AllSupportStdio(t *testing.T) {
	for name, p := range tools.KnownProfiles {
		assert.True(t, p.SupportsStdio, "tool %s must support stdio transport", name)
	}
}
