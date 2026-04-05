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

	// 3. 非 git 且无 .iclude.yaml → 检查是否是项目目录 / Non-git without .iclude.yaml → check if project dir
	if !IsProjectDir(projectDir) {
		return "" // 日常会话模式 / Daily conversation mode
	}

	// 4. path hash fallback
	hash := sha256.Sum256([]byte(projectDir))
	return fmt.Sprintf("p_%x", hash[:6])
}

// IsProjectDir 判断是否在项目目录中 / Check if CWD is a project directory
func IsProjectDir(dir string) bool {
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
	// 去掉协议前缀 / Strip protocol prefix
	for _, prefix := range []string{"ssh://", "https://", "http://", "git://"} {
		if strings.HasPrefix(url, prefix) {
			url = url[len(prefix):]
			break
		}
	}
	// 去掉 user@ 前缀 / Strip user@ prefix
	if at := strings.Index(url, "@"); at >= 0 {
		url = url[at+1:]
	}
	// 去掉端口号（host:port/path → host/path）/ Strip port (host:port/path → host/path)
	if colon := strings.Index(url, ":"); colon >= 0 {
		rest := url[colon+1:]
		if slash := strings.Index(rest, "/"); slash >= 0 {
			url = url[:colon] + rest[slash:]
		}
	}
	// 去掉 .git 后缀 / Strip .git suffix
	url = strings.TrimSuffix(url, ".git")

	return strings.ToLower(url)
}
