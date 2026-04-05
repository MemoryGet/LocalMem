// Package config 配置管理 / Configuration management
package config

// HooksConfig Claude Code hooks 配置 / Claude Code hooks configuration
type HooksConfig struct {
	Enabled        bool     `mapstructure:"enabled"`
	MCPURL         string   `mapstructure:"mcp_url"`
	SkipTools      []string `mapstructure:"skip_tools"`
	MaxInputChars  int      `mapstructure:"max_input_chars"`
	MaxOutputChars int      `mapstructure:"max_output_chars"`
	InjectLimit    int      `mapstructure:"inject_limit"`
	SummaryLimit   int      `mapstructure:"summary_limit"`
	HostTool       string   `mapstructure:"host_tool"`    // 宿主工具标识 / Host tool identifier (claude-code, codex, cursor, cline)
	CaptureMode    string   `mapstructure:"capture_mode"` // 捕获模式 / Capture mode (auto, manual, off)
}

// ResolvedHostTool 返回 host_tool，未配置时默认 claude-code / Return host_tool with default
func (h HooksConfig) ResolvedHostTool() string {
	if h.HostTool != "" {
		return h.HostTool
	}
	return "claude-code"
}

// ResolvedCaptureMode 返回 capture_mode，未配置时默认 auto / Return capture_mode with default
func (h HooksConfig) ResolvedCaptureMode() string {
	if h.CaptureMode != "" {
		return h.CaptureMode
	}
	return "auto"
}
