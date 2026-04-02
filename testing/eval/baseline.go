// Package eval 提供基线快照管理与回归检测 / provides baseline snapshot management and regression detection.
package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// EvalReport 评测报告 / Evaluation report.
// 如果 runner.go 已定义该类型，此处注释掉 / If runner.go already defines this type, remove this definition.
type EvalReport struct {
	Mode         string                      `json:"mode"`
	Dataset      string                      `json:"dataset"`
	Timestamp    time.Time                   `json:"timestamp"`
	Metrics      AggregateMetrics            `json:"metrics"`
	ByCategory   map[string]AggregateMetrics `json:"by_category"`
	ByDifficulty map[string]AggregateMetrics `json:"by_difficulty"`
	Cases        []CaseResult                `json:"cases"`
	Duration     time.Duration               `json:"duration"`
	GitCommit    string                      `json:"git_commit,omitempty"`
}

// Regression 回归检测结果 / Regression detection result.
type Regression struct {
	Metric   string  `json:"metric"`
	Baseline float64 `json:"baseline"`
	Current  float64 `json:"current"`
	Delta    float64 `json:"delta"`
}

// RegressionThresholds 回归阈值 / Regression detection thresholds.
// 所有阈值均为正数，表示允许的最大下降量 / All values are positive and represent the maximum allowed drop.
type RegressionThresholds struct {
	HitRateDrop float64 // percentage points (e.g. 2.0 = 2 pp)
	MRRDrop     float64 // absolute (e.g. 0.02)
	NDCGDrop    float64 // absolute (e.g. 0.02)
}

// DefaultThresholds 默认回归阈值 / Default regression thresholds.
var DefaultThresholds = RegressionThresholds{
	HitRateDrop: 2.0,
	MRRDrop:     0.02,
	NDCGDrop:    0.02,
}

// SaveBaseline 将评测报告序列化为 JSON 并写入 baseDir/name.json /
// Serializes the evaluation report as indented JSON and writes to baseDir/name.json.
func SaveBaseline(report *EvalReport, name string, baseDir string) error {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return fmt.Errorf("SaveBaseline: mkdir %s: %w", baseDir, err)
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("SaveBaseline: marshal report: %w", err)
	}
	path := filepath.Join(baseDir, name+".json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("SaveBaseline: write file %s: %w", path, err)
	}
	return nil
}

// LoadBaseline 从 baseDir/name.json 读取并反序列化评测报告 /
// Reads and unmarshals an evaluation report from baseDir/name.json.
func LoadBaseline(name string, baseDir string) (*EvalReport, error) {
	path := filepath.Join(baseDir, name+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("LoadBaseline: read file %s: %w", path, err)
	}
	var report EvalReport
	if err := json.Unmarshal(data, &report); err != nil {
		return nil, fmt.Errorf("LoadBaseline: unmarshal JSON: %w", err)
	}
	return &report, nil
}

// ListBaselines 列举 baseDir 下所有 .json 文件并返回不含扩展名的名称列表 /
// Lists all .json files in baseDir and returns their names without the .json suffix.
func ListBaselines(baseDir string) ([]string, error) {
	entries, err := os.ReadDir(baseDir)
	if err != nil {
		return nil, fmt.Errorf("ListBaselines: read dir %s: %w", baseDir, err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".json") {
			names = append(names, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	return names, nil
}

// CompareBaseline 检测当前报告相对于基线是否出现指标回归 /
// Detects metric regressions by comparing current report against the baseline using thresholds.
// 返回所有超过阈值的指标回归项 / Returns all metrics that dropped beyond the configured threshold.
func CompareBaseline(current, baseline *EvalReport, thresholds RegressionThresholds) []Regression {
	var regressions []Regression

	// HitRate: percentage points drop
	if delta := current.Metrics.HitRate - baseline.Metrics.HitRate; delta < -thresholds.HitRateDrop {
		regressions = append(regressions, Regression{
			Metric:   "HitRate",
			Baseline: baseline.Metrics.HitRate,
			Current:  current.Metrics.HitRate,
			Delta:    delta,
		})
	}

	// MRR: absolute drop
	if delta := current.Metrics.MRR - baseline.Metrics.MRR; delta < -thresholds.MRRDrop {
		regressions = append(regressions, Regression{
			Metric:   "MRR",
			Baseline: baseline.Metrics.MRR,
			Current:  current.Metrics.MRR,
			Delta:    delta,
		})
	}

	// NDCG@10: absolute drop
	if delta := current.Metrics.NDCG10 - baseline.Metrics.NDCG10; delta < -thresholds.NDCGDrop {
		regressions = append(regressions, Regression{
			Metric:   "NDCG@10",
			Baseline: baseline.Metrics.NDCG10,
			Current:  current.Metrics.NDCG10,
			Delta:    delta,
		})
	}

	return regressions
}
