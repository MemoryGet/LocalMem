package tools

// HostProfile 宿主工具能力 profile / Host tool capability profile
type HostProfile struct {
	Name            string   // 工具名 / Tool name (matches allowedToolNames)
	SupportsHooks   bool     // 支持 hooks（session-start/capture/stop）/ Supports lifecycle hooks
	SupportsStdio   bool     // 支持 stdio MCP 传输 / Supports stdio MCP transport
	SupportsSSE     bool     // 支持 SSE MCP 传输 / Supports SSE MCP transport
	DefaultScope    string   // 默认 scope 前缀 / Default scope prefix
	AllowedScopes   []string // 允许的 scope 前缀 / Allowed scope prefixes
	CaptureMode     string   // 默认捕获模式 / Default capture mode (auto/manual/off)
	MaxToolTimeout  int      // 最大工具超时（秒）/ Max tool timeout (seconds)
}

// KnownProfiles 已知宿主工具 profile / Known host tool profiles
var KnownProfiles = map[string]HostProfile{
	"claude-code": {
		Name:           "claude-code",
		SupportsHooks:  true,
		SupportsStdio:  true,
		SupportsSSE:    true,
		DefaultScope:   "project/",
		AllowedScopes:  []string{"user/", "project/", "session/", "agent/"},
		CaptureMode:    "auto",
		MaxToolTimeout: 60,
	},
	"codex": {
		Name:           "codex",
		SupportsHooks:  false, // Codex 通过 AGENTS.md 集成，无原生 hook / Integrates via AGENTS.md, no native hooks
		SupportsStdio:  true,
		SupportsSSE:    false,
		DefaultScope:   "project/",
		AllowedScopes:  []string{"user/", "project/", "session/", "agent/"},
		CaptureMode:    "auto",
		MaxToolTimeout: 60,
	},
	"cursor": {
		Name:           "cursor",
		SupportsHooks:  false,
		SupportsStdio:  true,
		SupportsSSE:    true,
		DefaultScope:   "project/",
		AllowedScopes:  []string{"user/", "project/", "session/", "agent/"},
		CaptureMode:    "auto",
		MaxToolTimeout: 60,
	},
	"cline": {
		Name:           "cline",
		SupportsHooks:  false,
		SupportsStdio:  true,
		SupportsSSE:    true,
		DefaultScope:   "project/",
		AllowedScopes:  []string{"user/", "project/", "session/", "agent/"},
		CaptureMode:    "auto",
		MaxToolTimeout: 60,
	},
}

// GetProfile 获取宿主 profile，未知工具返回 nil / Get host profile, nil for unknown tools
func GetProfile(toolName string) *HostProfile {
	p, ok := KnownProfiles[toolName]
	if !ok {
		return nil
	}
	return &p
}
