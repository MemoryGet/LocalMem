#!/bin/bash
# 每5次任务完成触发专家评估 / Trigger expert evaluation every 5 task completions
COUNTER_FILE="/root/LocalMem/.claude/eval_count"

# 读取当前计数 / Read current count
count=0
if [ -f "$COUNTER_FILE" ]; then
    count=$(cat "$COUNTER_FILE" 2>/dev/null || echo 0)
    [[ "$count" =~ ^[0-9]+$ ]] || count=0
fi

count=$((count + 1))

if [ "$count" -ge 5 ]; then
    echo 0 > "$COUNTER_FILE"
    cat <<'EOF'
{
  "decision": "block",
  "reason": "【自动评估触发】已累计完成第5次任务。请立即对 IClude 项目（/root/LocalMem）进行一次系统性专家评估：并行 dispatch 以下4个专家 agent——Database Optimizer（数据库存储层评估）、AI Engineer（AI检索与推理质量评估）、Security Engineer（安全漏洞扫描）、Software Architect（整体架构评估），等所有 agent 完成后汇总报告展示给用户。这是定期质量保障机制，请不要跳过。"
}
EOF
else
    echo "$count" > "$COUNTER_FILE"
fi
