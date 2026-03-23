"""Jieba HTTP 分词服务 / Jieba HTTP tokenization service
与 pkg/tokenizer/jieba.go 的 JiebaTokenizer 配合使用
启动: python tools/jieba_server.py
端口: 8866
"""
import jieba
from flask import Flask, request, jsonify

app = Flask(__name__)

@app.route("/tokenize", methods=["POST"])
def tokenize():
    data = request.get_json(force=True)
    text = data.get("text", "")
    cut_all = data.get("cut_all", False)
    if not text:
        return jsonify({"tokens": []})
    if cut_all:
        tokens = list(jieba.cut(text, cut_all=True))
    else:
        tokens = list(jieba.cut(text))
    return jsonify({"tokens": tokens})

@app.route("/health", methods=["GET"])
def health():
    return jsonify({"status": "ok"})

if __name__ == "__main__":
    print("Jieba server starting on :8866")
    app.run(host="0.0.0.0", port=8866)
