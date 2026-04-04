package compliance_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"iclude/pkg/identity"
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
