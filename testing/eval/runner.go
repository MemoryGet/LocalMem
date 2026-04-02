// Package eval 提供评测运行器 / provides the evaluation test runner.
package eval

import (
	"testing"
)

// NewTestRunner 创建用于测试的运行器和清理函数 /
// Creates a runner and cleanup function for tests.
// 此为占位实现，待 Task 3 完成后替换 / Placeholder until Task 3 is implemented.
func NewTestRunner(t *testing.T) (*Runner, func()) {
	t.Helper()
	t.Skip("runner not yet implemented (Task 3 pending)")
	return nil, func() {}
}

// Runner 评测运行器 / Evaluation runner.
// 此为占位实现，待 Task 3 完成后替换 / Placeholder until Task 3 is implemented.
type Runner struct{}

// Run 执行评测 / Executes the evaluation.
func (r *Runner) Run(_ interface{}, _ *EvalDataset, _ string) (*EvalReport, error) {
	return nil, nil
}
