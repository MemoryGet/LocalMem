<h1 align="center">
  <br>
  IClude
  <br>
</h1>

<h4 align="center">AI 애플리케이션을 위한 로컬 우선 하이브리드 메모리 시스템.</h4>

<p align="center">
  <a href="../../README.md">🇺🇸 English</a> •
  <a href="README.zh.md">🇨🇳 中文</a> •
  <a href="README.ja.md">🇯🇵 日本語</a> •
  <a href="README.es.md">🇪🇸 Español</a> •
  <a href="README.de.md">🇩🇪 Deutsch</a> •
  <a href="README.fr.md">🇫🇷 Français</a> •
  <a href="README.ru.md">🇷🇺 Русский</a> •
  <a href="README.pt.md">🇵🇹 Português</a> •
  <a href="README.ar.md">🇸🇦 العربية</a>
</p>

<p align="center">
  <a href="../../LICENSE">
    <img src="https://img.shields.io/badge/License-MIT-blue.svg" alt="License">
  </a>
  <a href="https://github.com/MemeryGit/LocalMem/releases">
    <img src="https://img.shields.io/github/v/release/MemeryGit/LocalMem?color=green" alt="Release">
  </a>
  <a href="../../go.mod">
    <img src="https://img.shields.io/badge/Go-1.25+-00ADD8.svg?logo=go" alt="Go Version">
  </a>
</p>

<p align="center">
  IClude는 SQLite(구조화 + 전문 검색)와 Qdrant(벡터 시맨틱 검색)를 결합하여 3방향 하이브리드 검색, 다중 라운드 LLM 추론, 지식 그래프 추출, 문서 수집 기능을 단일 Go 바이너리로 제공합니다.
</p>

---

## 빠른 시작

```bash
# 저장소 클론
git clone https://github.com/MemeryGit/LocalMem.git
cd LocalMem

# 종속성 설치
go mod download

# 설정
cp config/config.yaml ./config.yaml

# API 서버 실행 (포트 8080)
go run ./cmd/server/

# MCP 서버 실행 (포트 8081, 선택사항)
go run ./cmd/mcp/
```

### Docker 배포

```bash
docker-compose -f deploy/docker-compose.yml up
```

### 요구 사항

- **Go** 1.25+
- **Qdrant** (선택, 벡터 검색용)
- **Docling / Apache Tika** (선택, 문서 파싱용)

---

## 주요 기능

- **하이브리드 검색** — SQLite FTS5 (BM25), Qdrant 벡터 유사도, 지식 그래프 연관을 결합한 3방향 검색. RRF (k=60)로 통합
- **메모리 라이프사이클** — 보존 등급 (`permanent` / `long_term` / `standard` / `short_term` / `ephemeral`), 설정 가능한 감쇠율, 소프트 삭제, 강화 메커니즘
- **다중 라운드 추론** — Reflect Engine이 검색된 메모리에 대해 반복적 LLM 추론 수행, 결론 자동 저장
- **지식 그래프** — LLM을 통한 메모리 콘텐츠에서 엔티티/관계 자동 추출
- **문서 수집** — 업로드 → 파싱 → 청킹 → 임베딩 → 저장 파이프라인
- **MCP 서버** — Model Context Protocol 지원 (SSE 전송)
- **CJK 전문 검색** — 플러그인 가능한 FTS5 토크나이저

---

## 스토리지 모드

| 모드 | 설명 |
|------|------|
| **SQLite 전용** | 구조화 쿼리 + FTS5 전문 검색 |
| **Qdrant 전용** | 벡터 시맨틱 검색 |
| **하이브리드** | 둘 다 활성화 — 가중 RRF로 결과 통합 |

---

## API 엔드포인트

모든 엔드포인트는 `/v1/` 하위:

| 그룹 | 설명 |
|------|------|
| `/v1/memories` | CRUD + 소프트 삭제/복원 + 강화 |
| `/v1/retrieve` | 3방향 하이브리드 검색 |
| `/v1/reflect` | 다중 라운드 LLM 추론 |
| `/v1/conversations` | 대화 일괄 수집 |
| `/v1/entities` | 지식 그래프 |
| `/v1/documents` | 문서 업로드/처리 |

---

## MCP 통합

```json
{
  "mcpServers": {
    "iclude": {
      "type": "sse",
      "url": "http://localhost:8081/sse"
    }
  }
}
```

**사용 가능한 MCP 도구:** `recall_memories`, `save_memory`, `reflect`, `ingest_conversation`, `timeline`

---

## 라이선스

**MIT 라이선스** — Copyright (c) 2026 MemeryGit.

자세한 내용은 [LICENSE](../../LICENSE)를 참조하세요.

---

<p align="center">
  <b>Go로 구축</b> • <b>SQLite + Qdrant 기반</b> • <b>MCP 지원</b>
</p>
