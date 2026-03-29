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
}
