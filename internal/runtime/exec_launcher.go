package runtime

import (
	"context"
	"fmt"
	"os"
	"os/exec"

	"iclude/internal/logger"
	"iclude/internal/model"

	"go.uber.org/zap"
)

// sentinel error aliases for launcher / launcher 哨兵错误别名
var (
	ErrUnsupportedTool = model.ErrUnsupportedTool
	ErrToolNotFound    = model.ErrToolNotFound
)

// toolBinary 宿主工具对应的可执行文件名 / Executable binary name per host tool
var toolBinary = map[string]string{
	"claude-code": "claude",
	"codex":       "codex",
	"cursor":      "cursor",
	"cline":       "cline",
}

// ExecLauncher 基于 os/exec 的宿主工具启动器 / os/exec-based host tool launcher
type ExecLauncher struct{}

// NewExecLauncher 创建 ExecLauncher / Create ExecLauncher
func NewExecLauncher() *ExecLauncher {
	return &ExecLauncher{}
}

// Launch 启动宿主工具进程 / Launch host tool process
func (l *ExecLauncher) Launch(ctx context.Context, req LaunchRequest) error {
	bin, ok := toolBinary[req.ToolName]
	if !ok {
		return fmt.Errorf("unknown tool %q: %w", req.ToolName, ErrUnsupportedTool)
	}

	// 查找可执行文件 / Resolve executable path
	binPath, err := exec.LookPath(bin)
	if err != nil {
		return fmt.Errorf("tool %q not found in PATH: %w", bin, ErrToolNotFound)
	}

	// 构建参数 / Build arguments
	args := make([]string, 0, len(req.Args)+2)
	if req.ProjectDir != "" {
		args = append(args, "--cwd", req.ProjectDir)
	}
	args = append(args, req.Args...)

	cmd := exec.CommandContext(ctx, binPath, args...)
	cmd.Dir = req.ProjectDir

	// 合并环境变量 / Merge environment variables
	cmd.Env = os.Environ()
	if req.SessionID != "" {
		cmd.Env = append(cmd.Env, "ICLUDE_SESSION_ID="+req.SessionID)
	}
	for k, v := range req.Environment {
		cmd.Env = append(cmd.Env, k+"="+v)
	}

	logger.Info("runtime.launcher_exec",
		zap.String("tool", req.ToolName),
		zap.String("bin", binPath),
		zap.String("project_dir", req.ProjectDir),
		zap.String("session_id", req.SessionID),
	)

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start %s: %w", bin, err)
	}

	// 非阻塞：不等待进程完成 / Non-blocking: don't wait for process to finish
	go func() {
		if waitErr := cmd.Wait(); waitErr != nil {
			logger.Debug("launcher process exited",
				zap.String("tool", req.ToolName),
				zap.Error(waitErr),
			)
		}
	}()

	return nil
}
