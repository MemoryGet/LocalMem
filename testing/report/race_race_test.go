//go:build race

package report_test

// isRaceEnabled 返回竞态检测器是否激活 / Reports whether the race detector is active.
// race 构建中始终返回 true / Always returns true in race builds.
func isRaceEnabled() bool { return true }
