package compliance_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"iclude/pkg/identity"
)

// --- URL Normalize ---

func TestNormalizeGitRemoteURL(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want string
	}{
		{"ssh", "git@github.com:MemoryGet/LocalMem.git", "github.com/memoryget/localmem"},
		{"https", "https://github.com/MemoryGet/LocalMem.git", "github.com/memoryget/localmem"},
		{"https no .git", "https://github.com/MemoryGet/LocalMem", "github.com/memoryget/localmem"},
		{"ssh://", "ssh://git@github.com/MemoryGet/LocalMem.git", "github.com/memoryget/localmem"},
		{"with port", "ssh://git@github.com:2222/MemoryGet/LocalMem", "github.com/memoryget/localmem"},
		{"empty", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, identity.NormalizeGitRemoteURL(tt.url))
		})
	}
}

// --- Existing path-based tests ---

func TestResolveProjectID_SamePathSameID(t *testing.T) {
	// 使用带 .iclude.yaml 的目录确保稳定 / Use dir with .iclude.yaml for stability
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".iclude.yaml"), []byte("project_id: stable-test\n"), 0644)
	id1 := identity.ResolveProjectID(dir)
	id2 := identity.ResolveProjectID(dir)
	assert.Equal(t, id1, id2)
}

func TestResolveProjectID_DifferentPathDifferentID(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	// 两个目录都有 .iclude.yaml 但 project_id 不同 / Both have .iclude.yaml with different ids
	os.WriteFile(filepath.Join(dir1, ".iclude.yaml"), []byte("project_id: proj-a\n"), 0644)
	os.WriteFile(filepath.Join(dir2, ".iclude.yaml"), []byte("project_id: proj-b\n"), 0644)
	assert.NotEqual(t, identity.ResolveProjectID(dir1), identity.ResolveProjectID(dir2))
}

func TestResolveProjectID_EmptyPath(t *testing.T) {
	assert.Equal(t, "", identity.ResolveProjectID(""))
}

// --- Git remote tests ---

func TestResolveProjectID_GitRemoteStable(t *testing.T) {
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	remote := "https://github.com/MemoryGet/LocalMem.git"

	for _, d := range []string{dir1, dir2} {
		gitRun(t, d, "init")
		gitRun(t, d, "remote", "add", "origin", remote)
	}

	id1 := identity.ResolveProjectID(dir1)
	id2 := identity.ResolveProjectID(dir2)
	assert.Equal(t, id1, id2, "same git remote should produce same project_id")
	assert.True(t, strings.HasPrefix(id1, "p_"))
}

func TestResolveProjectID_UpstreamOverOrigin(t *testing.T) {
	// Bob fork 了仓库，配了 upstream 指向上游 / Bob forked, configured upstream to upstream repo
	forkDir := t.TempDir()
	gitRun(t, forkDir, "init")
	gitRun(t, forkDir, "remote", "add", "origin", "https://github.com/Bob/LocalMem.git")
	gitRun(t, forkDir, "remote", "add", "upstream", "https://github.com/MemoryGet/LocalMem.git")

	// Alice 直接 clone 上游 / Alice cloned upstream directly
	upstreamDir := t.TempDir()
	gitRun(t, upstreamDir, "init")
	gitRun(t, upstreamDir, "remote", "add", "origin", "https://github.com/MemoryGet/LocalMem.git")

	assert.Equal(t, identity.ResolveProjectID(forkDir), identity.ResolveProjectID(upstreamDir))
}

func TestResolveProjectID_SSHAndHTTPSSameResult(t *testing.T) {
	sshDir := t.TempDir()
	gitRun(t, sshDir, "init")
	gitRun(t, sshDir, "remote", "add", "origin", "git@github.com:MemoryGet/LocalMem.git")

	httpsDir := t.TempDir()
	gitRun(t, httpsDir, "init")
	gitRun(t, httpsDir, "remote", "add", "origin", "https://github.com/MemoryGet/LocalMem.git")

	assert.Equal(t, identity.ResolveProjectID(sshDir), identity.ResolveProjectID(httpsDir))
}

// --- .iclude.yaml override ---

func TestResolveProjectID_IcludeYamlOverride(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init")
	gitRun(t, dir, "remote", "add", "origin", "https://github.com/MemoryGet/LocalMem.git")
	os.WriteFile(filepath.Join(dir, ".iclude.yaml"), []byte("project_id: my-custom-id\n"), 0644)

	assert.Equal(t, "my-custom-id", identity.ResolveProjectID(dir))
}

// --- Non-git fallback ---

func TestResolveProjectID_NonGitWithIcludeYaml(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".iclude.yaml"), []byte("project_id: manual-proj\n"), 0644)
	assert.Equal(t, "manual-proj", identity.ResolveProjectID(dir))
}

func TestResolveProjectID_NonProjectReturnsEmpty(t *testing.T) {
	dir := t.TempDir() // 无 .git 无 .iclude.yaml / No .git, no .iclude.yaml
	assert.Equal(t, "", identity.ResolveProjectID(dir))
}

// --- IsProjectDir ---

func TestIsProjectDir_GitDir(t *testing.T) {
	dir := t.TempDir()
	gitRun(t, dir, "init")
	assert.True(t, identity.IsProjectDir(dir))
}

func TestIsProjectDir_IcludeYaml(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, ".iclude.yaml"), []byte("project_id: x\n"), 0644)
	assert.True(t, identity.IsProjectDir(dir))
}

func TestIsProjectDir_TempDir(t *testing.T) {
	dir := t.TempDir()
	assert.False(t, identity.IsProjectDir(dir))
}

// gitRun 辅助函数 / helper to run git commands
func gitRun(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "git %v failed: %s", args, out)
}
