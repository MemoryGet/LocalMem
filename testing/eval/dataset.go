package eval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
)

// SeedMemory 种子记忆 / Seed memory for evaluation
type SeedMemory struct {
	Content     string `json:"content"`
	Kind        string `json:"kind"`
	SubKind     string `json:"sub_kind"`
	MemoryClass string `json:"memory_class,omitempty"` // episodic(default) / semantic / procedural
}

// EvalCase 评测用例 / Single evaluation case
type EvalCase struct {
	Query      string   `json:"query"`
	Expected   []string `json:"expected"`
	Category   string   `json:"category"`
	Difficulty string   `json:"difficulty"`
}

// EvalDataset 评测数据集 / Evaluation dataset
type EvalDataset struct {
	Name         string       `json:"name"`
	Description  string       `json:"description"`
	SeedMemories []SeedMemory `json:"seed_memories"`
	Cases        []EvalCase   `json:"cases"`
}

// rawDataset Python 脚本 --dump-dataset 输出的原始格式 / Raw format from Python --dump-dataset output
type rawDataset struct {
	SeedMemories []SeedMemory `json:"seed_memories"`
	TestQueries  [][]any      `json:"test_queries"`
}

// LoadDatasetFromJSON 从 JSON 文件读取并解析评测数据集 / Reads and parses an EvalDataset from a JSON file.
func LoadDatasetFromJSON(path string) (*EvalDataset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("LoadDatasetFromJSON: read file: %w", err)
	}
	var ds EvalDataset
	if err := json.Unmarshal(data, &ds); err != nil {
		return nil, fmt.Errorf("LoadDatasetFromJSON: unmarshal JSON: %w", err)
	}
	return &ds, nil
}

// ExportBuiltinDataset 调用 Python 脚本导出内置数据集并写入目标 JSON 文件 /
// Calls `python3 <scriptPath> --dump-dataset`, converts raw output to EvalDataset,
// and writes the result to outputPath as JSON.
func ExportBuiltinDataset(scriptPath string, outputPath string) error {
	cmd := exec.Command("python3", scriptPath, "--dump-dataset")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ExportBuiltinDataset: python3 failed: %w (stderr: %s)", err, stderr.String())
	}

	var raw rawDataset
	if err := json.Unmarshal(stdout.Bytes(), &raw); err != nil {
		return fmt.Errorf("ExportBuiltinDataset: unmarshal raw output: %w", err)
	}

	cases := make([]EvalCase, 0, len(raw.TestQueries))
	for i, row := range raw.TestQueries {
		if len(row) != 4 {
			return fmt.Errorf("ExportBuiltinDataset: test_queries[%d]: expected 4 elements, got %d", i, len(row))
		}
		query, ok1 := row[0].(string)
		expected, ok2 := row[1].(string)
		category, ok3 := row[2].(string)
		difficulty, ok4 := row[3].(string)
		if !ok1 || !ok2 || !ok3 || !ok4 {
			return fmt.Errorf("ExportBuiltinDataset: test_queries[%d]: unexpected element types", i)
		}
		cases = append(cases, EvalCase{
			Query:      query,
			Expected:   []string{expected},
			Category:   category,
			Difficulty: difficulty,
		})
	}

	ds := EvalDataset{
		Name:         "retrieval-500",
		Description:  "500-query retrieval evaluation dataset exported from tools/retrieval_test_500.py",
		SeedMemories: raw.SeedMemories,
		Cases:        cases,
	}

	out, err := json.MarshalIndent(ds, "", "  ")
	if err != nil {
		return fmt.Errorf("ExportBuiltinDataset: marshal output: %w", err)
	}

	if err := os.WriteFile(outputPath, out, 0o644); err != nil {
		return fmt.Errorf("ExportBuiltinDataset: write output: %w", err)
	}

	return nil
}
