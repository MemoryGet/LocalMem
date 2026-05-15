#!/usr/bin/env python3
"""批量重新触发实体抽取 / Batch re-trigger entity extraction for all memories."""

import sqlite3
import urllib.request
import urllib.error
import json
import time
import sys

DB_PATH = "data/iclude.db"
API_BASE = "http://localhost:8080"
BATCH_SIZE = 20   # 并发控制：每批触发后短暂等待
DELAY = 0.3       # 每次请求间隔（秒）


def get_all_memory_ids():
    conn = sqlite3.connect(DB_PATH)
    c = conn.cursor()
    c.execute("SELECT id FROM memories WHERE deleted_at IS NULL ORDER BY created_at DESC")
    ids = [row[0] for row in c.fetchall()]
    conn.close()
    return ids


def trigger_extract(memory_id):
    url = f"{API_BASE}/v1/memories/{memory_id}/extract"
    req = urllib.request.Request(url, method="POST")
    req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=60) as resp:
            body = json.loads(resp.read().decode("utf-8"))
            data = body.get("data") or {}
            entities = data.get("entities") or []
            return len(entities), None
    except urllib.error.HTTPError as e:
        return 0, f"HTTP {e.code}"
    except Exception as ex:
        return 0, str(ex)


def main():
    print("checking server...")
    try:
        with urllib.request.urlopen(f"{API_BASE}/v1/memories?limit=1", timeout=5):
            pass
    except Exception as e:
        print(f"[FAIL] cannot connect {API_BASE}: {e}")
        print("start server first: go run ./cmd/server/")
        sys.exit(1)
    print("[OK] server ready")

    ids = get_all_memory_ids()
    total = len(ids)
    print(f"total {total} memories, starting extraction...\n")

    success = 0
    failed = 0
    total_entities = 0

    for i, mid in enumerate(ids, 1):
        count, err = trigger_extract(mid)
        if err:
            failed += 1
            print(f"[{i:3d}/{total}] {mid[:8]}  FAIL {err}")
        else:
            success += 1
            total_entities += count
            marker = f"  +{count} entities" if count > 0 else ""
            print(f"[{i:3d}/{total}] {mid[:8]}  OK{marker}")

        time.sleep(DELAY)
        if i % BATCH_SIZE == 0:
            print(f"--- processed {i}/{total}, waiting for heartbeat promotion... ---\n")
            time.sleep(2)

    print(f"\ndone: success={success}, failed={failed}, entities_promoted={total_entities}")
    print("wait ~30s for heartbeat to promote candidates to main graph")


if __name__ == "__main__":
    main()
