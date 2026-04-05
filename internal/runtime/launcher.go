package runtime

import "context"

// LaunchRequest launcher 启动请求 / Launcher start request
type LaunchRequest struct {
	ToolName    string            // 宿主工具名 / Host tool name (codex/cursor/cline)
	ProjectDir  string            // 工作目录 / Working directory
	SessionID   string            // 会话 ID / Session ID
	Args        []string          // 传递给宿主的参数 / Arguments for host tool
	Environment map[string]string // 额外环境变量 / Extra environment variables
}

// Launcher 宿主工具启动器接口 / Host tool launcher interface
// 实现：ExecLauncher（exec_launcher.go）/ Implementation: ExecLauncher (exec_launcher.go)
type Launcher interface {
	Launch(ctx context.Context, req LaunchRequest) error
}
