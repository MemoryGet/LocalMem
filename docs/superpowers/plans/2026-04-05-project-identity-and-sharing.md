# 项目标识稳定化 + 团队记忆共享 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让同一 git 仓库在不同路径/机器上产生相同的 project_id，项目级记忆自动团队共享，scope 写入受权限管控。

**Architecture:** ResolveProjectID 改为优先级链（.iclude.yaml → git remote normalized hash → path hash）；Manager.Create 按 scope+kind 自动填充 visibility；新增 scope_policies 表 + CRUD API + 写入校验；MCP session 注入 projectScope 供 retain fallback。

**Tech Stack:** Go 1.25+, SQLite, Gin, os/exec (git commands)

---

### Task 1: ResolveProjectID 优先级链 + URL Normalize

**Files:**
- Modify: `pkg/identity/project_id.go`
- Test: `testing/compliance/identity_test.go`

- [ ] **Step 1: 写测试（git remote normalize）**

```go
// testing/compliance/identity_test.go — 新增

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

func TestResolveProjectID_GitRemoteStable(t *testing.T) {
	// 两个不同路径但指向同一个 git remote 的目录应产生相同 project_id
	// 此测试需要 mock git — 用 temp dir + git init + git remote add
	dir1 := t.TempDir()
	dir2 := t.TempDir()
	remote := "https://github.com/MemoryGet/LocalMem.git"

	for _, d := range []string{dir1, dir2} {
		run(t, d, "git", "init")
		run(t, d, "git", "remote", "add", "origin", remote)
	}

	id1 := identity.ResolveProjectID(dir1)
	id2 := identity.ResolveProjectID(dir2)
	assert.Equal(t, id1, id2, "same git remote should produce same project_id")
	assert.True(t, strings.HasPrefix(id1, "p_"))
}

func TestResolveProjectID_UpstreamOverOrigin(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "git", "init")
	run(t, dir, "git", "remote", "add", "origin", "https://github.com/Bob/LocalMem.git")
	run(t, dir, "git", "remote", "add", "upstream", "https://github.com/MemoryGet/LocalMem.git")

	// upstream 目录的 project_id 应匹配 upstream URL 而不是 origin
	upstreamDir := t.TempDir()
	run(t, upstreamDir, "git", "init")
	run(t, upstreamDir, "git", "remote", "add", "origin", "https://github.com/MemoryGet/LocalMem.git")

	assert.Equal(t, identity.ResolveProjectID(dir), identity.ResolveProjectID(upstreamDir))
}

func TestResolveProjectID_IcludeYamlOverride(t *testing.T) {
	dir := t.TempDir()
	run(t, dir, "git", "init")
	run(t, dir, "git", "remote", "add", "origin", "https://github.com/MemoryGet/LocalMem.git")
	os.WriteFile(filepath.Join(dir, ".iclude.yaml"), []byte("project_id: my-custom-id\n"), 0644)

	assert.Equal(t, "my-custom-id", identity.ResolveProjectID(dir))
}

func TestResolveProjectID_NonGitFallback(t *testing.T) {
	dir := t.TempDir()
	id := identity.ResolveProjectID(dir)
	assert.True(t, strings.HasPrefix(id, "p_"), "non-git dir should still get p_ prefix")
}

// run 辅助函数
func run(t *testing.T, dir string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, "cmd %s %v failed: %s", name, args, out)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./testing/compliance/ -run "TestNormalizeGitRemoteURL|TestResolveProjectID_Git|TestResolveProjectID_Iclude|TestResolveProjectID_NonGit|TestResolveProjectID_Upstream" -v -count=1`
Expected: FAIL — `NormalizeGitRemoteURL` 未定义

- [ ] **Step 3: 实现 NormalizeGitRemoteURL + 优先级链**

```go
// pkg/identity/project_id.go — 完整替换

package identity

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// icludeYaml .iclude.yaml 文件结构 / .iclude.yaml file structure
type icludeYaml struct {
	ProjectID string `yaml:"project_id"`
}

// ResolveProjectID 按优先级链生成稳定 project_id / Generate stable project_id via priority chain
// 优先级: .iclude.yaml > git remote (upstream > origin) > path hash > 空
func ResolveProjectID(projectDir string) string {
	if projectDir == "" {
		return ""
	}

	// 1. .iclude.yaml override
	if id := readIcludeYaml(projectDir); id != "" {
		return id
	}

	// 2. git remote (upstream > origin)
	if normalized := resolveGitRemote(projectDir); normalized != "" {
		hash := sha256.Sum256([]byte(normalized))
		return fmt.Sprintf("p_%x", hash[:6])
	}

	// 3. 非 git 且无 .iclude.yaml → 检查是否是项目目录
	if !isProjectDir(projectDir) {
		return "" // 日常会话模式
	}

	// 4. path hash fallback
	hash := sha256.Sum256([]byte(projectDir))
	return fmt.Sprintf("p_%x", hash[:6])
}

// IsProjectDir 判断是否在项目目录中 / Check if CWD is a project directory
func IsProjectDir(dir string) bool {
	return isProjectDir(dir)
}

func isProjectDir(dir string) bool {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(dir, ".iclude.yaml")); err == nil {
		return true
	}
	return false
}

// readIcludeYaml 读取 .iclude.yaml 中的 project_id / Read project_id from .iclude.yaml
func readIcludeYaml(dir string) string {
	data, err := os.ReadFile(filepath.Join(dir, ".iclude.yaml"))
	if err != nil {
		return ""
	}
	var cfg icludeYaml
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return ""
	}
	return cfg.ProjectID
}

// resolveGitRemote 读取 git remote URL（upstream 优先于 origin）/ Read git remote URL (upstream > origin)
func resolveGitRemote(dir string) string {
	for _, remote := range []string{"upstream", "origin"} {
		url := gitRemoteURL(dir, remote)
		if url != "" {
			return NormalizeGitRemoteURL(url)
		}
	}
	return ""
}

// gitRemoteURL 读取指定 remote 的 URL / Get URL for a specific git remote
func gitRemoteURL(dir, remote string) string {
	cmd := exec.Command("git", "remote", "get-url", remote)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// sshRemoteRe 匹配 git@host:owner/repo 格式 / Match git@host:owner/repo SSH format
var sshRemoteRe = regexp.MustCompile(`^[\w.-]+@([\w.-]+):(.+)$`)

// NormalizeGitRemoteURL 将不同格式的 git URL 统一为 host/owner/repo（全小写）
// Normalize various git URL formats to host/owner/repo (lowercase)
func NormalizeGitRemoteURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}

	url := strings.TrimSpace(rawURL)

	// git@host:owner/repo.git → host/owner/repo
	if m := sshRemoteRe.FindStringSubmatch(url); len(m) == 3 {
		path := strings.TrimSuffix(m[2], ".git")
		return strings.ToLower(m[1] + "/" + path)
	}

	// ssh://git@host(:port)/owner/repo.git 或 https://host/owner/repo.git
	// 去掉协议前缀
	for _, prefix := range []string{"ssh://", "https://", "http://", "git://"} {
		if strings.HasPrefix(url, prefix) {
			url = url[len(prefix):]
			break
		}
	}
	// 去掉 user@ 前缀
	if at := strings.Index(url, "@"); at >= 0 {
		url = url[at+1:]
	}
	// 去掉端口号（host:port/path → host/path）
	if colon := strings.Index(url, ":"); colon >= 0 {
		rest := url[colon+1:]
		if slash := strings.Index(rest, "/"); slash >= 0 {
			// 冒号后有 /，说明是 host:port/path 格式
			url = url[:colon] + rest[slash:]
		}
	}
	// 去掉 .git 后缀
	url = strings.TrimSuffix(url, ".git")

	return strings.ToLower(url)
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./testing/compliance/ -run "TestNormalizeGitRemoteURL|TestResolveProjectID_Git|TestResolveProjectID_Iclude|TestResolveProjectID_NonGit|TestResolveProjectID_Upstream" -v -count=1`
Expected: PASS

- [ ] **Step 5: go build 确认编译通过**

Run: `go build ./...`
Expected: 无错误

- [ ] **Step 6: 提交**

```bash
git add pkg/identity/project_id.go testing/compliance/identity_test.go
git commit -m "feat(identity): stable project_id via git remote URL normalize + .iclude.yaml override"
```

---

### Task 2: ScopePolicy Model + Store 接口 + SQLite 实现

**Files:**
- Create: `internal/model/scope_policy.go`
- Modify: `internal/store/interfaces.go`
- Create: `internal/store/sqlite_scope_policy.go`
- Modify: `internal/store/sqlite_schema.go`
- Modify: `internal/store/sqlite_migration_v21_v25.go`
- Modify: `internal/store/sqlite_migration.go`
- Test: `testing/store/scope_policy_test.go`

- [ ] **Step 1: 创建 ScopePolicy model**

```go
// internal/model/scope_policy.go

package model

import "time"

// ScopePolicy scope 写入权限策略 / Scope write permission policy
type ScopePolicy struct {
	ID             string    `json:"id"`
	Scope          string    `json:"scope"`           // e.g. "project/p_a1b2c3"
	DisplayName    string    `json:"display_name"`
	TeamID         string    `json:"team_id"`
	AllowedWriters []string  `json:"allowed_writers"` // owner_id 列表
	CreatedBy      string    `json:"created_by"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

// CanWrite 检查 owner_id 是否有写入权限 / Check if owner_id has write permission
// 无策略或空白名单 = 不限制 / No policy or empty writers = unrestricted
func (p *ScopePolicy) CanWrite(ownerID string) bool {
	if p == nil || len(p.AllowedWriters) == 0 {
		return true
	}
	for _, w := range p.AllowedWriters {
		if w == ownerID {
			return true
		}
	}
	return false
}
```

- [ ] **Step 2: 添加 ScopePolicyStore 接口**

在 `internal/store/interfaces.go` 末尾 `RawDBProvider` 之前添加：

```go
// ScopePolicyStore scope 权限策略存储接口 / Scope policy storage interface
type ScopePolicyStore interface {
	Create(ctx context.Context, p *model.ScopePolicy) error
	GetByScope(ctx context.Context, scope string) (*model.ScopePolicy, error)
	List(ctx context.Context, teamID string) ([]*model.ScopePolicy, error)
	Update(ctx context.Context, p *model.ScopePolicy) error
	Delete(ctx context.Context, scope string) error
}
```

- [ ] **Step 3: 写 store 测试**

```go
// testing/store/scope_policy_test.go

package store_test

import (
	"context"
	"testing"

	"iclude/internal/model"
	"iclude/internal/store"
	"iclude/pkg/tokenizer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupScopePolicyStore(t *testing.T) store.ScopePolicyStore {
	t.Helper()
	db := setupTestDB(t)
	return store.NewSQLiteScopePolicyStore(db)
}

func TestScopePolicy_CreateAndGet(t *testing.T) {
	s := setupScopePolicyStore(t)
	p := &model.ScopePolicy{
		Scope:          "project/p_abc123",
		DisplayName:    "my-backend",
		TeamID:         "team1",
		AllowedWriters: []string{"alice", "bob"},
		CreatedBy:      "alice",
	}
	require.NoError(t, s.Create(context.Background(), p))
	assert.NotEmpty(t, p.ID)

	got, err := s.GetByScope(context.Background(), "project/p_abc123")
	require.NoError(t, err)
	assert.Equal(t, "my-backend", got.DisplayName)
	assert.Equal(t, []string{"alice", "bob"}, got.AllowedWriters)
	assert.Equal(t, "alice", got.CreatedBy)
}

func TestScopePolicy_GetNotFound(t *testing.T) {
	s := setupScopePolicyStore(t)
	_, err := s.GetByScope(context.Background(), "project/nonexistent")
	assert.ErrorIs(t, err, model.ErrScopePolicyNotFound)
}

func TestScopePolicy_Update(t *testing.T) {
	s := setupScopePolicyStore(t)
	p := &model.ScopePolicy{
		Scope:          "project/p_update",
		DisplayName:    "old-name",
		TeamID:         "team1",
		AllowedWriters: []string{"alice"},
		CreatedBy:      "alice",
	}
	require.NoError(t, s.Create(context.Background(), p))

	p.DisplayName = "new-name"
	p.AllowedWriters = []string{"alice", "charlie"}
	require.NoError(t, s.Update(context.Background(), p))

	got, _ := s.GetByScope(context.Background(), "project/p_update")
	assert.Equal(t, "new-name", got.DisplayName)
	assert.Equal(t, []string{"alice", "charlie"}, got.AllowedWriters)
}

func TestScopePolicy_Delete(t *testing.T) {
	s := setupScopePolicyStore(t)
	p := &model.ScopePolicy{
		Scope: "project/p_del", TeamID: "team1", CreatedBy: "alice",
	}
	require.NoError(t, s.Create(context.Background(), p))
	require.NoError(t, s.Delete(context.Background(), "project/p_del"))

	_, err := s.GetByScope(context.Background(), "project/p_del")
	assert.ErrorIs(t, err, model.ErrScopePolicyNotFound)
}

func TestScopePolicy_ListByTeam(t *testing.T) {
	s := setupScopePolicyStore(t)
	require.NoError(t, s.Create(context.Background(), &model.ScopePolicy{
		Scope: "project/p_1", TeamID: "team1", CreatedBy: "alice",
	}))
	require.NoError(t, s.Create(context.Background(), &model.ScopePolicy{
		Scope: "project/p_2", TeamID: "team1", CreatedBy: "bob",
	}))
	require.NoError(t, s.Create(context.Background(), &model.ScopePolicy{
		Scope: "project/p_3", TeamID: "team2", CreatedBy: "charlie",
	}))

	list, err := s.List(context.Background(), "team1")
	require.NoError(t, err)
	assert.Len(t, list, 2)
}

func TestScopePolicy_CanWrite(t *testing.T) {
	p := &model.ScopePolicy{AllowedWriters: []string{"alice", "bob"}}
	assert.True(t, p.CanWrite("alice"))
	assert.True(t, p.CanWrite("bob"))
	assert.False(t, p.CanWrite("charlie"))

	// 空白名单 = 不限制
	empty := &model.ScopePolicy{AllowedWriters: nil}
	assert.True(t, empty.CanWrite("anyone"))

	// nil policy = 不限制
	var nilP *model.ScopePolicy
	assert.True(t, nilP.CanWrite("anyone"))
}
```

- [ ] **Step 4: 运行测试确认失败**

Run: `go test ./testing/store/ -run "TestScopePolicy" -v -count=1`
Expected: FAIL — 编译错误

- [ ] **Step 5: 添加 sentinel error**

在 `internal/model/errors.go` 中添加：

```go
// ErrScopePolicyNotFound scope 策略不存在 / Scope policy not found
ErrScopePolicyNotFound = errors.New("scope policy not found")
```

- [ ] **Step 6: 实现 SQLite store**

```go
// internal/store/sqlite_scope_policy.go

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"iclude/internal/model"

	"github.com/google/uuid"
)

// SQLiteScopePolicyStore scope_policies 表的 SQLite 实现 / SQLite impl for scope_policies
type SQLiteScopePolicyStore struct {
	db *sql.DB
}

// NewSQLiteScopePolicyStore 创建 scope policy store / Create scope policy store
func NewSQLiteScopePolicyStore(db *sql.DB) *SQLiteScopePolicyStore {
	return &SQLiteScopePolicyStore{db: db}
}

// Create 创建策略 / Create a scope policy
func (s *SQLiteScopePolicyStore) Create(ctx context.Context, p *model.ScopePolicy) error {
	if p.ID == "" {
		p.ID = uuid.New().String()
	}
	now := time.Now()
	p.CreatedAt = now
	p.UpdatedAt = now

	writersJSON, err := json.Marshal(p.AllowedWriters)
	if err != nil {
		return fmt.Errorf("marshal allowed_writers: %w", err)
	}

	_, err = s.db.ExecContext(ctx, `
		INSERT INTO scope_policies (id, scope, display_name, team_id, allowed_writers, created_by, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		p.ID, p.Scope, p.DisplayName, p.TeamID, string(writersJSON), p.CreatedBy, p.CreatedAt, p.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create scope policy: %w", err)
	}
	return nil
}

// GetByScope 按 scope 获取策略 / Get policy by scope
func (s *SQLiteScopePolicyStore) GetByScope(ctx context.Context, scope string) (*model.ScopePolicy, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, scope, display_name, team_id, allowed_writers, created_by, created_at, updated_at
		FROM scope_policies WHERE scope = ?`, scope)

	p := &model.ScopePolicy{}
	var writersJSON string
	err := row.Scan(&p.ID, &p.Scope, &p.DisplayName, &p.TeamID, &writersJSON, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, model.ErrScopePolicyNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get scope policy: %w", err)
	}
	if writersJSON != "" {
		_ = json.Unmarshal([]byte(writersJSON), &p.AllowedWriters)
	}
	return p, nil
}

// List 列出团队的所有策略 / List all policies for a team
func (s *SQLiteScopePolicyStore) List(ctx context.Context, teamID string) ([]*model.ScopePolicy, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, scope, display_name, team_id, allowed_writers, created_by, created_at, updated_at
		FROM scope_policies WHERE team_id = ? ORDER BY scope`, teamID)
	if err != nil {
		return nil, fmt.Errorf("list scope policies: %w", err)
	}
	defer rows.Close()

	var result []*model.ScopePolicy
	for rows.Next() {
		p := &model.ScopePolicy{}
		var writersJSON string
		if err := rows.Scan(&p.ID, &p.Scope, &p.DisplayName, &p.TeamID, &writersJSON, &p.CreatedBy, &p.CreatedAt, &p.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan scope policy: %w", err)
		}
		if writersJSON != "" {
			_ = json.Unmarshal([]byte(writersJSON), &p.AllowedWriters)
		}
		result = append(result, p)
	}
	return result, rows.Err()
}

// Update 更新策略 / Update a scope policy
func (s *SQLiteScopePolicyStore) Update(ctx context.Context, p *model.ScopePolicy) error {
	p.UpdatedAt = time.Now()
	writersJSON, err := json.Marshal(p.AllowedWriters)
	if err != nil {
		return fmt.Errorf("marshal allowed_writers: %w", err)
	}

	result, err := s.db.ExecContext(ctx, `
		UPDATE scope_policies SET display_name = ?, allowed_writers = ?, updated_at = ?
		WHERE scope = ?`,
		p.DisplayName, string(writersJSON), p.UpdatedAt, p.Scope,
	)
	if err != nil {
		return fmt.Errorf("update scope policy: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return model.ErrScopePolicyNotFound
	}
	return nil
}

// Delete 删除策略 / Delete a scope policy
func (s *SQLiteScopePolicyStore) Delete(ctx context.Context, scope string) error {
	result, err := s.db.ExecContext(ctx, `DELETE FROM scope_policies WHERE scope = ?`, scope)
	if err != nil {
		return fmt.Errorf("delete scope policy: %w", err)
	}
	n, _ := result.RowsAffected()
	if n == 0 {
		return model.ErrScopePolicyNotFound
	}
	return nil
}
```

- [ ] **Step 7: 添加 schema + 迁移**

在 `internal/store/sqlite_schema.go` 的 `createFreshSchema` 中，`idempotency_keys` 之后添加：

```sql
CREATE TABLE scope_policies (
    id               TEXT PRIMARY KEY,
    scope            TEXT NOT NULL UNIQUE,
    display_name     TEXT NOT NULL DEFAULT '',
    team_id          TEXT NOT NULL DEFAULT '',
    allowed_writers  TEXT NOT NULL DEFAULT '[]',
    created_by       TEXT NOT NULL DEFAULT '',
    created_at       DATETIME NOT NULL DEFAULT (datetime('now')),
    updated_at       DATETIME NOT NULL DEFAULT (datetime('now'))
)
```

在 `internal/store/sqlite_migration_v21_v25.go` 中添加 `migrateV23ToV24`：

```go
func migrateV23ToV24(db *sql.DB) error {
	logger.Info("executing migration V23→V24: add scope_policies table")
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS scope_policies (
		id               TEXT PRIMARY KEY,
		scope            TEXT NOT NULL UNIQUE,
		display_name     TEXT NOT NULL DEFAULT '',
		team_id          TEXT NOT NULL DEFAULT '',
		allowed_writers  TEXT NOT NULL DEFAULT '[]',
		created_by       TEXT NOT NULL DEFAULT '',
		created_at       DATETIME NOT NULL DEFAULT (datetime('now')),
		updated_at       DATETIME NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return err
	}

	if _, err := tx.Exec(`INSERT OR REPLACE INTO schema_version (version, applied_at) VALUES (24, datetime('now'))`); err != nil {
		return err
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	logger.Info("migration V23→V24 completed: scope_policies table added")
	return nil
}
```

在 `internal/store/sqlite_migration.go` 中：
- `latestVersion` 改为 `24`
- 添加 V23→V24 调用

- [ ] **Step 8: 运行测试确认通过**

Run: `go test ./testing/store/ -run "TestScopePolicy" -v -count=1`
Expected: PASS

- [ ] **Step 9: 提交**

```bash
git add internal/model/scope_policy.go internal/store/sqlite_scope_policy.go internal/store/interfaces.go \
  internal/store/sqlite_schema.go internal/store/sqlite_migration.go internal/store/sqlite_migration_v21_v25.go \
  internal/model/errors.go testing/store/scope_policy_test.go
git commit -m "feat(store): add scope_policies table + ScopePolicyStore interface and SQLite implementation"
```

---

### Task 3: Manager.Create 自动填充 visibility + scope 降级

**Files:**
- Modify: `internal/memory/manager.go`
- Modify: `internal/store/sqlite_memory_write.go`
- Test: `testing/memory/manager_test.go`

- [ ] **Step 1: 写测试**

在 `testing/memory/manager_test.go` 中新增：

```go
func TestManager_Create_AutoVisibility_ProjectObservation(t *testing.T) {
	mgr := setupManager(t)
	mem, err := mgr.Create(context.Background(), &model.CreateMemoryRequest{
		Content: "grep result", Kind: "observation", Scope: "project/p_abc",
	})
	require.NoError(t, err)
	assert.Equal(t, model.VisibilityPrivate, mem.Visibility)
}

func TestManager_Create_AutoVisibility_ProjectFact(t *testing.T) {
	mgr := setupManager(t)
	mem, err := mgr.Create(context.Background(), &model.CreateMemoryRequest{
		Content: "Go 1.25 is required", Kind: "fact", Scope: "project/p_abc",
	})
	require.NoError(t, err)
	assert.Equal(t, model.VisibilityTeam, mem.Visibility)
}

func TestManager_Create_AutoVisibility_UserScope(t *testing.T) {
	mgr := setupManager(t)
	mem, err := mgr.Create(context.Background(), &model.CreateMemoryRequest{
		Content: "I prefer dark mode", Kind: "fact", Scope: "user/alice",
	})
	require.NoError(t, err)
	assert.Equal(t, model.VisibilityPrivate, mem.Visibility)
}

func TestManager_Create_ExplicitVisibility_NotOverridden(t *testing.T) {
	mgr := setupManager(t)
	mem, err := mgr.Create(context.Background(), &model.CreateMemoryRequest{
		Content: "shared note", Kind: "note", Scope: "user/alice", Visibility: strPtr(model.VisibilityTeam),
	})
	require.NoError(t, err)
	assert.Equal(t, model.VisibilityTeam, mem.Visibility)
}

func strPtr(s string) *string { return &s }
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./testing/memory/ -run "TestManager_Create_AutoVisibility|TestManager_Create_ExplicitVisibility" -v -count=1`
Expected: FAIL — AutoVisibility 测试失败（当前全部返回 private）

- [ ] **Step 3: 修改 Manager.Create 添加自动 visibility 逻辑**

在 `internal/memory/manager.go` 的 `Create` 方法中，在 `ResolveTierDefaults(mem)` 之后添加：

```go
// 自动填充 visibility（仅当未显式指定时）/ Auto-fill visibility when not explicitly set
if mem.Visibility == "" {
	mem.Visibility = resolveDefaultVisibility(mem.Scope, mem.Kind)
}
```

在 manager.go 中新增函数：

```go
// resolveDefaultVisibility 按 scope+kind 决定默认可见性 / Determine default visibility by scope and kind
func resolveDefaultVisibility(scope, kind string) string {
	if strings.HasPrefix(scope, "project/") && kind != "observation" {
		return model.VisibilityTeam
	}
	return model.VisibilityPrivate
}
```

同时修改 `internal/store/sqlite_memory_write.go`，将 visibility 默认值从 store 层移到 Manager 层：
- 将 `if mem.Visibility == "" { mem.Visibility = model.VisibilityPrivate }` 改为 `if mem.Visibility == "" { mem.Visibility = "private" }` — 保持 store 层兜底但 Manager 层优先

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./testing/memory/ -run "TestManager_Create_AutoVisibility|TestManager_Create_ExplicitVisibility" -v -count=1`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/memory/manager.go internal/store/sqlite_memory_write.go testing/memory/manager_test.go
git commit -m "feat(memory): auto-fill visibility by scope+kind — project non-observation defaults to team"
```

---

### Task 4: MCP Session 注入 projectScope

**Files:**
- Modify: `internal/mcp/session.go`
- Modify: `internal/mcp/tools/create_session.go`
- Modify: `internal/mcp/tools/retain.go`

- [ ] **Step 1: Session 新增 projectScope 字段 + context 方法**

在 `internal/mcp/session.go` 的 `Session` struct 中新增：

```go
projectScope string // 当前会话关联的项目 scope / Project scope for this session
```

新增 setter：

```go
// SetProjectScope 设置项目 scope / Set project scope for this session
func (s *Session) SetProjectScope(scope string) {
	s.projectScope = scope
}

// ProjectScope 获取项目 scope / Get project scope
func (s *Session) ProjectScope() string {
	return s.projectScope
}
```

新增 context 注入/读取：

```go
type projectScopeCtxKey struct{}

// WithProjectScope 将 project scope 注入 context / Inject project scope into context
func WithProjectScope(ctx context.Context, scope string) context.Context {
	return context.WithValue(ctx, projectScopeCtxKey{}, scope)
}

// ProjectScopeFromContext 从 context 读取 project scope / Read project scope from context
func ProjectScopeFromContext(ctx context.Context) string {
	v, _ := ctx.Value(projectScopeCtxKey{}).(string)
	return v
}
```

在 `HandleRequest` 方法中，给所有工具调用注入 projectScope：

找到 `ctx = WithIdentity(ctx, s.identity)` 那一行，在其后添加：

```go
if s.projectScope != "" {
	ctx = WithProjectScope(ctx, s.projectScope)
}
```

- [ ] **Step 2: create_session 写入 projectScope 到 session**

在 `internal/mcp/tools/create_session.go` 的 `Execute` 方法中，`t.creator.Create` 成功后，添加 session projectScope 写入。

需要让 CreateSessionTool 持有 session 引用。修改方案：让 Execute 方法从 context 中获取当前 session 并写入。

在 `create_session.go` Execute 方法末尾、`return` 之前添加：

```go
// 将 project scope 写入当前 MCP session / Write project scope to current MCP session
if args.Scope != "" {
	if sess := mcp.SessionFromContext(ctx); sess != nil {
		sess.SetProjectScope(args.Scope)
	}
}
```

需要在 session.go 添加 `SessionFromContext`：

```go
type sessionCtxKey struct{}

func WithSession(ctx context.Context, s *Session) context.Context {
	return context.WithValue(ctx, sessionCtxKey{}, s)
}

func SessionFromContext(ctx context.Context) *Session {
	s, _ := ctx.Value(sessionCtxKey{}).(*Session)
	return s
}
```

在 HandleRequest 中注入 session 到 context：

```go
ctx = WithSession(ctx, s)
```

- [ ] **Step 3: retain 从 context 读取 projectScope 作为 fallback**

在 `internal/mcp/tools/retain.go` 的 Execute 方法中，修改 scope 自动推导逻辑：

```go
// scope 自动推导 / Auto-derive scope when empty
scope := args.Scope
if scope == "" {
	// 优先从 session 获取项目 scope / Prefer project scope from session
	if ps := mcp.ProjectScopeFromContext(ctx); ps != "" {
		scope = ps
	} else if id != nil && id.OwnerID != "" {
		scope = "user/" + id.OwnerID
	}
}
```

- [ ] **Step 4: go build 确认编译通过**

Run: `go build ./...`
Expected: 无错误

- [ ] **Step 5: 提交**

```bash
git add internal/mcp/session.go internal/mcp/tools/create_session.go internal/mcp/tools/retain.go
git commit -m "feat(mcp): inject projectScope into session context — retain auto-derives project scope"
```

---

### Task 5: Scope 降级逻辑 + 降级响应

**Files:**
- Modify: `internal/memory/manager.go`
- Modify: `internal/mcp/tools/retain.go`

- [ ] **Step 1: Manager 添加 scope 降级方法**

在 `internal/memory/manager.go` 中新增：

```go
// ScopePolicyChecker scope 策略检查接口 / Scope policy checker interface
type ScopePolicyChecker interface {
	GetByScope(ctx context.Context, scope string) (*model.ScopePolicy, error)
}

// CheckAndDowngradeScope 检查写入权限，不通过时降级 scope / Check write permission, downgrade scope if denied
// 返回 (实际 scope, 是否降级, 原始 scope)
func CheckAndDowngradeScope(ctx context.Context, checker ScopePolicyChecker, scope, ownerID string) (actualScope string, downgraded bool, reason string) {
	if checker == nil || !strings.HasPrefix(scope, "project/") {
		return scope, false, ""
	}

	policy, err := checker.GetByScope(ctx, scope)
	if err != nil {
		// 无策略 = 不限制 / No policy = unrestricted
		return scope, false, ""
	}

	if policy.CanWrite(ownerID) {
		return scope, false, ""
	}

	// 降级到 user/ scope / Downgrade to user/ scope
	downgradedScope := "user/" + ownerID
	return downgradedScope, true, fmt.Sprintf("not in allowed_writers for %s", scope)
}
```

- [ ] **Step 2: retain 响应增加降级信息**

在 `internal/mcp/tools/retain.go` Execute 方法中，scope 确定后、创建 mem 之前，添加降级检查：

```go
// scope 降级检查 / Scope downgrade check
var downgraded bool
var requestedScope, downgradeReason string
if t.policyChecker != nil {
	requestedScope = scope
	scope, downgraded, downgradeReason = memory.CheckAndDowngradeScope(ctx, t.policyChecker, scope, id.OwnerID)
	if downgraded {
		// 降级时 visibility 强制 private / Force private on downgrade
		mem.Visibility = model.VisibilityPrivate
	}
}
```

修改 RetainTool struct 增加 policyChecker：

```go
type RetainTool struct {
	manager       MemoryCreator
	policyChecker memory.ScopePolicyChecker // 可为 nil / may be nil
}

func NewRetainTool(manager MemoryCreator, policyChecker memory.ScopePolicyChecker) *RetainTool {
	return &RetainTool{manager: manager, policyChecker: policyChecker}
}
```

修改响应，降级时增加提示：

```go
resp := map[string]any{"id": created.ID, "content": created.Content}
if downgraded {
	resp["scope_downgraded"] = true
	resp["requested_scope"] = requestedScope
	resp["actual_scope"] = scope
	resp["reason"] = downgradeReason
}
out, _ := json.Marshal(resp)
```

- [ ] **Step 3: go build 确认编译通过**

Run: `go build ./...`
Expected: 无错误（需要同步更新所有 NewRetainTool 调用点传入 policyChecker 或 nil）

- [ ] **Step 4: 提交**

```bash
git add internal/memory/manager.go internal/mcp/tools/retain.go
git commit -m "feat(policy): scope downgrade on write permission denied — retain response includes downgrade info"
```

---

### Task 6: Scope Policy API Handler + 路由

**Files:**
- Create: `internal/api/scope_policy_handler.go`
- Modify: `internal/api/router.go`
- Test: `testing/api/scope_policy_test.go`

- [ ] **Step 1: 实现 handler**

```go
// internal/api/scope_policy_handler.go

package api

import (
	"net/http"

	"iclude/internal/model"
	"iclude/internal/store"

	"github.com/gin-gonic/gin"
)

// ScopePolicyHandler scope 策略管理 handler / Scope policy management handler
type ScopePolicyHandler struct {
	store store.ScopePolicyStore
}

// NewScopePolicyHandler 创建 handler / Create handler
func NewScopePolicyHandler(s store.ScopePolicyStore) *ScopePolicyHandler {
	return &ScopePolicyHandler{store: s}
}

// List GET /v1/scope-policies
func (h *ScopePolicyHandler) List(c *gin.Context, identity *model.Identity) {
	policies, err := h.store.List(c.Request.Context(), identity.TeamID)
	if err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	SuccessResponse(c, http.StatusOK, policies)
}

// Create POST /v1/scope-policies
func (h *ScopePolicyHandler) Create(c *gin.Context, identity *model.Identity) {
	var req model.ScopePolicy
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorResponse(c, http.StatusBadRequest, err)
		return
	}
	req.TeamID = identity.TeamID
	req.CreatedBy = identity.OwnerID

	if err := h.store.Create(c.Request.Context(), &req); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	SuccessResponse(c, http.StatusCreated, req)
}

// Get GET /v1/scope-policies/:scope
func (h *ScopePolicyHandler) Get(c *gin.Context, identity *model.Identity) {
	scope := c.Param("scope")
	policy, err := h.store.GetByScope(c.Request.Context(), scope)
	if err != nil {
		if err == model.ErrScopePolicyNotFound {
			ErrorResponse(c, http.StatusNotFound, err)
			return
		}
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	SuccessResponse(c, http.StatusOK, policy)
}

// Update PUT /v1/scope-policies/:scope
func (h *ScopePolicyHandler) Update(c *gin.Context, identity *model.Identity) {
	scope := c.Param("scope")
	existing, err := h.store.GetByScope(c.Request.Context(), scope)
	if err != nil {
		if err == model.ErrScopePolicyNotFound {
			ErrorResponse(c, http.StatusNotFound, err)
			return
		}
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	// 鉴权：仅 created_by 可修改 / Auth: only created_by can update
	if existing.CreatedBy != identity.OwnerID {
		ErrorResponse(c, http.StatusForbidden, fmt.Errorf("only the policy creator can update"))
		return
	}

	var req model.ScopePolicy
	if err := c.ShouldBindJSON(&req); err != nil {
		ErrorResponse(c, http.StatusBadRequest, err)
		return
	}
	req.Scope = scope
	if err := h.store.Update(c.Request.Context(), &req); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	SuccessResponse(c, http.StatusOK, req)
}

// Delete DELETE /v1/scope-policies/:scope
func (h *ScopePolicyHandler) Delete(c *gin.Context, identity *model.Identity) {
	scope := c.Param("scope")
	existing, err := h.store.GetByScope(c.Request.Context(), scope)
	if err != nil {
		if err == model.ErrScopePolicyNotFound {
			ErrorResponse(c, http.StatusNotFound, err)
			return
		}
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}

	// 鉴权 / Auth
	if existing.CreatedBy != identity.OwnerID {
		ErrorResponse(c, http.StatusForbidden, fmt.Errorf("only the policy creator can delete"))
		return
	}

	if err := h.store.Delete(c.Request.Context(), scope); err != nil {
		ErrorResponse(c, http.StatusInternalServerError, err)
		return
	}
	SuccessResponse(c, http.StatusOK, map[string]string{"deleted": scope})
}
```

- [ ] **Step 2: 注册路由**

在 `internal/api/router.go` 中，添加 scope-policies 路由组：

```go
if stores.ScopePolicyStore != nil {
	sph := NewScopePolicyHandler(stores.ScopePolicyStore)
	sp := v1.Group("/scope-policies")
	{
		sp.GET("", withIdentity(sph.List))
		sp.POST("", withIdentity(sph.Create))
		sp.GET("/:scope", withIdentity(sph.Get))
		sp.PUT("/:scope", withIdentity(sph.Update))
		sp.DELETE("/:scope", withIdentity(sph.Delete))
	}
}
```

- [ ] **Step 3: go build 确认编译通过**

Run: `go build ./...`
Expected: 无错误

- [ ] **Step 4: 提交**

```bash
git add internal/api/scope_policy_handler.go internal/api/router.go
git commit -m "feat(api): add scope-policies CRUD endpoints with created_by authorization"
```

---

### Task 7: Hook session-start 输出 scope 信息

**Files:**
- Modify: `cmd/cli/hook_session_start.go`

- [ ] **Step 1: 输出中增加 project scope 和 user scope**

在 `hook_session_start.go` 的输出 session header 部分，`fmt.Println("---")` 之前添加：

```go
if projectScope != "" {
	fmt.Printf("Project scope: %s\n", projectScope)
}
fmt.Printf("User scope: user/%s\n", cfg.MCP.DefaultOwnerID)
```

- [ ] **Step 2: go build 确认编译通过**

Run: `go build ./...`
Expected: 无错误

- [ ] **Step 3: 提交**

```bash
git add cmd/cli/hook_session_start.go
git commit -m "feat(hooks): session-start outputs project scope and user scope for AI-guided classification"
```

---

### Task 8: Retain 工具描述更新 + Wiring

**Files:**
- Modify: `internal/mcp/tools/retain.go`
- Modify: `internal/bootstrap/wiring.go`
- Modify: `internal/store/factory.go` (if Stores struct needs ScopePolicyStore)
- Modify: `cmd/mcp/main.go`

- [ ] **Step 1: 更新 retain 工具描述**

在 `retain.go` 的 `Definition()` 中，更新 scope 字段描述：

```json
"scope": {
  "type": "string",
  "description": "Namespace scope. Rules: user preferences/habits → 'user/{owner_id}'; project knowledge/decisions → use project scope from session context; uncertain → omit (system auto-derives from session)"
}
```

- [ ] **Step 2: Wiring 注入 ScopePolicyStore**

在 `internal/store/factory.go` 的 `Stores` struct 中添加 `ScopePolicyStore ScopePolicyStore`。

在 `InitStores` 中，SQLite 启用时创建：`stores.ScopePolicyStore = NewSQLiteScopePolicyStore(db)`。

在 `internal/bootstrap/wiring.go` 中，将 `ScopePolicyStore` 传入需要它的组件（RetainTool、Router）。

在 `cmd/mcp/main.go` 中，更新 `NewRetainTool` 调用传入 `stores.ScopePolicyStore`。

- [ ] **Step 3: go build + 全量测试**

Run: `go build ./... && go test ./testing/runtime/ ./testing/memory/ ./testing/search/ ./testing/compliance/ -count=1 -timeout 120s`
Expected: 编译通过 + 测试全绿

- [ ] **Step 4: 提交**

```bash
git add internal/mcp/tools/retain.go internal/bootstrap/wiring.go internal/store/factory.go cmd/mcp/main.go
git commit -m "feat(wiring): integrate ScopePolicyStore into retain tool and bootstrap pipeline"
```
