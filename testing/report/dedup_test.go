package report_test

import (
	"context"
	"fmt"
	"testing"

	"iclude/internal/memory"
	"iclude/internal/model"
	"iclude/internal/store"
	"iclude/pkg/hashutil"
	"iclude/pkg/testreport"
	"iclude/pkg/tokenizer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	suiteDedup     = "哈希去重 (Hash Dedup)"
	suiteDedupIcon = "\U0001F512"
	suiteDedupDesc = "P0: SHA-256 内容哈希去重，相同内容写入时自动 reinforce 而非重复创建"
)

func TestDedup_ExactDuplicate(t *testing.T) {
	tc := testreport.NewCase(t, suiteDedup, suiteDedupIcon, suiteDedupDesc,
		"精确重复内容去重")
	defer tc.Done()

	tc.Input("内容", "这是一条测试记忆")
	tc.Input("写入次数", "2")

	mgr, cleanup := setupReportManager(t)
	defer cleanup()
	tc.Step("创建 Manager 实例")

	ctx := context.Background()
	mem1, err := mgr.Create(ctx, &model.CreateMemoryRequest{Content: "这是一条测试记忆"})
	require.NoError(t, err)
	tc.Step("第一次写入成功", fmt.Sprintf("id=%s", mem1.ID))

	mem2, err := mgr.Create(ctx, &model.CreateMemoryRequest{Content: "这是一条测试记忆"})
	require.NoError(t, err)
	tc.Step("第二次写入（重复内容）")

	assert.Equal(t, mem1.ID, mem2.ID, "重复内容应返回相同 ID")
	tc.Step("验证: 返回相同 ID（去重成功）")

	tc.Output("首次ID", mem1.ID)
	tc.Output("二次ID", mem2.ID)
	tc.Output("去重", "成功")
}

func TestDedup_WhitespaceDifference(t *testing.T) {
	tc := testreport.NewCase(t, suiteDedup, suiteDedupIcon, suiteDedupDesc,
		"空白差异归一化去重")
	defer tc.Done()

	tc.Input("内容A", "hello  world")
	tc.Input("内容B", "hello world")

	mgr, cleanup := setupReportManager(t)
	defer cleanup()
	tc.Step("创建 Manager 实例")

	ctx := context.Background()
	mem1, err := mgr.Create(ctx, &model.CreateMemoryRequest{Content: "hello  world"})
	require.NoError(t, err)
	tc.Step("写入 'hello  world'（双空格）")

	mem2, err := mgr.Create(ctx, &model.CreateMemoryRequest{Content: "hello world"})
	require.NoError(t, err)
	tc.Step("写入 'hello world'（单空格）")

	assert.Equal(t, mem1.ID, mem2.ID, "空白归一化后应为同一记忆")
	tc.Step("验证: 空白差异被归一化，去重成功")

	tc.Output("hash_A", hashutil.ContentHash("hello  world"))
	tc.Output("hash_B", hashutil.ContentHash("hello world"))
	tc.Output("去重", "成功")
}

func TestDedup_CaseDifference(t *testing.T) {
	tc := testreport.NewCase(t, suiteDedup, suiteDedupIcon, suiteDedupDesc,
		"大小写差异归一化去重")
	defer tc.Done()

	tc.Input("内容A", "Hello World")
	tc.Input("内容B", "hello world")

	mgr, cleanup := setupReportManager(t)
	defer cleanup()
	tc.Step("创建 Manager 实例")

	ctx := context.Background()
	mem1, err := mgr.Create(ctx, &model.CreateMemoryRequest{Content: "Hello World"})
	require.NoError(t, err)

	mem2, err := mgr.Create(ctx, &model.CreateMemoryRequest{Content: "hello world"})
	require.NoError(t, err)

	assert.Equal(t, mem1.ID, mem2.ID, "大小写归一化后应为同一记忆")
	tc.Step("验证: 大小写差异被归一化，去重成功")

	tc.Output("去重", "成功")
}

func TestDedup_DifferentContent(t *testing.T) {
	tc := testreport.NewCase(t, suiteDedup, suiteDedupIcon, suiteDedupDesc,
		"不同内容正常创建")
	defer tc.Done()

	tc.Input("内容A", "Go语言编程")
	tc.Input("内容B", "Python数据分析")

	mgr, cleanup := setupReportManager(t)
	defer cleanup()
	tc.Step("创建 Manager 实例")

	ctx := context.Background()
	mem1, err := mgr.Create(ctx, &model.CreateMemoryRequest{Content: "Go语言编程"})
	require.NoError(t, err)

	mem2, err := mgr.Create(ctx, &model.CreateMemoryRequest{Content: "Python数据分析"})
	require.NoError(t, err)

	assert.NotEqual(t, mem1.ID, mem2.ID, "不同内容应创建不同记忆")
	tc.Step("验证: 不同内容生成不同 ID")

	tc.Output("ID_A", mem1.ID)
	tc.Output("ID_B", mem2.ID)
	tc.Output("去重", "未触发（正确）")
}

func TestDedup_ContentHashComputed(t *testing.T) {
	tc := testreport.NewCase(t, suiteDedup, suiteDedupIcon, suiteDedupDesc,
		"content_hash 字段自动计算")
	defer tc.Done()

	content := "测试哈希计算"
	tc.Input("内容", content)

	mgr, cleanup := setupReportManager(t)
	defer cleanup()
	tc.Step("创建 Manager 实例")

	ctx := context.Background()
	mem, err := mgr.Create(ctx, &model.CreateMemoryRequest{Content: content})
	require.NoError(t, err)
	tc.Step("创建记忆")

	expectedHash := memory.ContentHash(content)
	assert.Equal(t, expectedHash, mem.ContentHash, "content_hash 应自动计算")
	assert.NotEmpty(t, mem.ContentHash)
	tc.Step("验证: content_hash 非空且与 ContentHash() 一致")

	tc.Output("content_hash", mem.ContentHash)
}

// setupReportManager 创建用于 report 测试的 Manager
func setupReportManager(t *testing.T) (*memory.Manager, func()) {
	t.Helper()
	s := newTestStore(t, tokenizer.NewSimpleTokenizer())
	mgr := memory.NewManager(memory.ManagerDeps{MemStore: s.(*store.SQLiteMemoryStore)})
	return mgr, func() { s.Close() }
}
