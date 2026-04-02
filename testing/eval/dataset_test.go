package eval

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestLoadDatasetFromJSON 测试从 JSON 文件加载数据集 / tests loading a dataset from a JSON file
func TestLoadDatasetFromJSON(t *testing.T) {
	// 准备临时 JSON 文件 / Prepare a temp JSON file
	dataset := EvalDataset{
		Name:        "test-dataset",
		Description: "unit test fixture",
		SeedMemories: []SeedMemory{
			{Content: "用户偏好暗色主题", Kind: "profile", SubKind: "preference"},
			{Content: "Go module is iclude", Kind: "fact", SubKind: "entity"},
		},
		Cases: []EvalCase{
			{Query: "暗色主题", Expected: []string{"暗色主题"}, Category: "exact", Difficulty: "easy"},
			{Query: "Go module", Expected: []string{"iclude"}, Category: "exact", Difficulty: "easy"},
		},
	}

	data, err := json.Marshal(dataset)
	require.NoError(t, err)

	tmpFile := filepath.Join(t.TempDir(), "test-dataset.json")
	require.NoError(t, os.WriteFile(tmpFile, data, 0o644))

	// 加载并验证 / Load and verify
	got, err := LoadDatasetFromJSON(tmpFile)
	require.NoError(t, err)
	require.NotNil(t, got)

	assert.Equal(t, "test-dataset", got.Name)
	assert.Equal(t, "unit test fixture", got.Description)
	assert.Len(t, got.SeedMemories, 2)
	assert.Equal(t, "用户偏好暗色主题", got.SeedMemories[0].Content)
	assert.Equal(t, "profile", got.SeedMemories[0].Kind)
	assert.Equal(t, "preference", got.SeedMemories[0].SubKind)
	assert.Len(t, got.Cases, 2)
	assert.Equal(t, "暗色主题", got.Cases[0].Query)
	assert.Equal(t, []string{"暗色主题"}, got.Cases[0].Expected)
	assert.Equal(t, "exact", got.Cases[0].Category)
	assert.Equal(t, "easy", got.Cases[0].Difficulty)
}

// TestLoadDatasetFromJSON_FileNotFound 测试文件不存在时返回错误 / tests error when file is missing
func TestLoadDatasetFromJSON_FileNotFound(t *testing.T) {
	_, err := LoadDatasetFromJSON("/nonexistent/path/no-such-file.json")
	assert.Error(t, err)
}

// TestLoadDatasetFromJSON_InvalidJSON 测试 JSON 格式错误时返回错误 / tests error on malformed JSON
func TestLoadDatasetFromJSON_InvalidJSON(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "bad.json")
	require.NoError(t, os.WriteFile(tmpFile, []byte("{not valid json"), 0o644))

	_, err := LoadDatasetFromJSON(tmpFile)
	assert.Error(t, err)
}

// TestExportBuiltinDataset 测试导出内置数据集 / tests exporting the builtin dataset via Python script
func TestExportBuiltinDataset(t *testing.T) {
	// 检查 python3 是否可用 / Check if python3 is available
	if _, err := os.Stat("/usr/bin/python3"); os.IsNotExist(err) {
		if _, err2 := os.Stat("/usr/local/bin/python3"); os.IsNotExist(err2) {
			t.Skip("python3 not available, skipping ExportBuiltinDataset test")
		}
	}

	scriptPath := "../../tools/retrieval_test_500.py"
	if _, err := os.Stat(scriptPath); os.IsNotExist(err) {
		t.Skip("retrieval_test_500.py not found, skipping ExportBuiltinDataset test")
	}

	outputPath := filepath.Join(t.TempDir(), "exported-500.json")
	err := ExportBuiltinDataset(scriptPath, outputPath)
	require.NoError(t, err)

	// 验证输出文件内容 / Verify output file content
	got, err := LoadDatasetFromJSON(outputPath)
	require.NoError(t, err)

	assert.Equal(t, "retrieval-500", got.Name)
	assert.NotEmpty(t, got.SeedMemories)
	assert.NotEmpty(t, got.Cases)
	// Python script has exactly 500 queries
	assert.Len(t, got.Cases, 500)

	// 验证每个 case 的结构 / Verify structure of each case
	for i, c := range got.Cases {
		assert.NotEmpty(t, c.Query, "case %d: query should not be empty", i)
		assert.NotEmpty(t, c.Expected, "case %d: expected should not be empty", i)
		assert.NotEmpty(t, c.Category, "case %d: category should not be empty", i)
		assert.NotEmpty(t, c.Difficulty, "case %d: difficulty should not be empty", i)
	}
}
