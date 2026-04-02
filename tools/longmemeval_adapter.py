#!/usr/bin/env python3
"""
LongMemEval → LocalMem EvalDataset 适配器

将 LongMemEval oracle 数据集转换为 LocalMem 的 per-question 评测格式。
每个 question 独立一组 seed_memories + 1 个 case。

输出格式：JSON array of {seed_memories, case}
"""

import json
import sys
import re


def extract_keywords(answer: str) -> list[str]:
    """从答案中提取可匹配的关键词"""
    ans = str(answer).strip()
    if not ans:
        return []

    keywords = []

    # 整个答案作为一个匹配项
    keywords.append(ans)

    # 如果答案包含括号说明（如 "7 days. 8 days (including...) is also acceptable"），提取替代答案
    alt_match = re.findall(r'(\d+)\s*(?:days?|minutes?|hours?|seconds?)', ans)
    for m in alt_match:
        keywords.append(m)

    # 如果答案是短语，也拆成核心词
    if len(ans.split()) <= 5:
        keywords.append(ans.lower())

    return list(set(keywords))


def convert_oracle(input_path: str, output_path: str):
    with open(input_path, 'r', encoding='utf-8') as f:
        data = json.load(f)

    entries = []
    for item in data:
        # 将对话 turns 转为 seed memories
        seeds = []
        for sid, session, date in zip(
            item['haystack_session_ids'],
            item['haystack_sessions'],
            item['haystack_dates']
        ):
            for turn in session:
                content = turn['content'].strip()
                if not content:
                    continue
                seeds.append({
                    "content": content,
                    "kind": "conversation",
                    "sub_kind": turn['role'],
                    "metadata": {
                        "session_id": sid,
                        "timestamp": date,
                        "is_evidence": turn.get('has_answer', False),
                    }
                })

        # difficulty heuristic
        qtype = item['question_type']
        is_abstention = '_abs' in item.get('question_id', '')
        if is_abstention:
            difficulty = 'hard'
        elif qtype.startswith('single-session'):
            difficulty = 'easy'
        elif qtype == 'knowledge-update':
            difficulty = 'medium'
        elif qtype == 'temporal-reasoning':
            difficulty = 'medium'
        else:  # multi-session
            difficulty = 'hard'

        case = {
            "query": item['question'],
            "expected": extract_keywords(item['answer']),
            "category": qtype,
            "difficulty": difficulty,
            "question_id": item['question_id'],
            "gold_answer": str(item['answer']),
            "is_abstention": is_abstention,
        }

        entries.append({
            "seed_memories": seeds,
            "case": case,
        })

    with open(output_path, 'w', encoding='utf-8') as f:
        json.dump(entries, f, ensure_ascii=False, indent=2)

    print(f"Converted {len(entries)} questions")
    print(f"Output: {output_path}")

    # Stats
    from collections import Counter
    types = Counter(e['case']['category'] for e in entries)
    for k, v in types.most_common():
        print(f"  {k}: {v}")


if __name__ == '__main__':
    input_path = sys.argv[1] if len(sys.argv) > 1 else '/tmp/longmemeval_oracle.json'
    output_path = sys.argv[2] if len(sys.argv) > 2 else 'testing/eval/testdata/longmemeval-oracle.json'
    convert_oracle(input_path, output_path)
    print(f"Done: {input_path} → {output_path}")
