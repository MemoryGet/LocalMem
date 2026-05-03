#!/usr/bin/env python3
"""BGE-Reranker HTTP sidecar — compatible with rerank_remote.go

Implements POST /rerank with Jina/Cohere-compatible request/response format.

Usage:
    pip install FlagEmbedding flask
    python tools/reranker_server.py

Environment variables:
    RERANKER_MODEL  — model name (default: BAAI/bge-reranker-v2-m3)
    RERANKER_PORT   — listen port (default: 8868)
"""
import os
import sys

from flask import Flask, request, jsonify

MODEL_NAME = os.getenv("RERANKER_MODEL", "BAAI/bge-reranker-v2-m3")
PORT = int(os.getenv("RERANKER_PORT", "8868"))

print(f"Loading reranker model: {MODEL_NAME} ...", flush=True)
try:
    from FlagEmbedding import FlagReranker
    reranker = FlagReranker(MODEL_NAME, use_fp16=True)
    print("Reranker model loaded.", flush=True)
except ImportError:
    print("ERROR: FlagEmbedding not installed. Run: pip install FlagEmbedding", file=sys.stderr)
    sys.exit(1)
except Exception as e:
    print(f"ERROR loading model: {e}", file=sys.stderr)
    sys.exit(1)

app = Flask(__name__)


@app.route("/healthz")
def health():
    return jsonify({"status": "ok", "model": MODEL_NAME})


@app.route("/rerank", methods=["POST"])
def rerank():
    body = request.get_json(force=True)
    query = body.get("query", "")
    documents = body.get("documents", [])
    top_n = body.get("top_n", len(documents))

    if not query or not documents:
        return jsonify({"results": []})

    pairs = [[query, doc] for doc in documents]
    scores = reranker.compute_score(pairs, normalize=True)
    if isinstance(scores, float):
        scores = [scores]

    indexed = sorted(enumerate(scores), key=lambda x: x[1], reverse=True)
    results = [
        {"index": idx, "relevance_score": float(score)}
        for idx, score in indexed[:top_n]
    ]
    return jsonify({"results": results})


if __name__ == "__main__":
    app.run(host="0.0.0.0", port=PORT)
