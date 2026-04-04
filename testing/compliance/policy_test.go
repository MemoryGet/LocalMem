package compliance_test

import (
	"testing"

	"iclude/internal/mcp/tools"

	"github.com/stretchr/testify/assert"
)

func TestWritePolicy_BlocksCoreFromExternal(t *testing.T) {
	assert.Error(t, tools.ValidateWritePolicy("core", "hook"))
}
func TestWritePolicy_BlocksProceduralFromExternal(t *testing.T) {
	assert.Error(t, tools.ValidateWritePolicy("procedural", "hook"))
}
func TestWritePolicy_AllowsEpisodicFromExternal(t *testing.T) {
	assert.NoError(t, tools.ValidateWritePolicy("episodic", "hook"))
}
func TestWritePolicy_AllowsEmptyClassFromExternal(t *testing.T) {
	assert.NoError(t, tools.ValidateWritePolicy("", "hook"))
}
func TestWritePolicy_AllowsCoreFromInternal(t *testing.T) {
	assert.NoError(t, tools.ValidateWritePolicy("core", "reflect"))
}
func TestWritePolicy_AllowsProceduralFromInternal(t *testing.T) {
	assert.NoError(t, tools.ValidateWritePolicy("procedural", "session_summary"))
}
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
