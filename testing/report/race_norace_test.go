//go:build !race

package report_test

// isRaceEnabled 返回竞态检测器是否激活 / Reports whether the race detector is active.
// 非 race 构建中始终返回 false / Always returns false in non-race builds.
func isRaceEnabled() bool { return false }
