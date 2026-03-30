<h1 align="center">
  <br>
  IClude
  <br>
</h1>

<h4 align="center">نظام ذاكرة هجين محلي أولاً لتطبيقات الذكاء الاصطناعي.</h4>

<p align="center">
  <a href="../../README.md">🇺🇸 English</a> •
  <a href="README.zh.md">🇨🇳 中文</a> •
  <a href="README.ja.md">🇯🇵 日本語</a> •
  <a href="README.ko.md">🇰🇷 한국어</a> •
  <a href="README.es.md">🇪🇸 Español</a> •
  <a href="README.de.md">🇩🇪 Deutsch</a> •
  <a href="README.fr.md">🇫🇷 Français</a> •
  <a href="README.ru.md">🇷🇺 Русский</a> •
  <a href="README.pt.md">🇵🇹 Português</a>
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
  يجمع IClude بين SQLite (البحث المنظم + النص الكامل) و Qdrant (البحث الدلالي المتجهي) لتوفير استرجاع هجين ثلاثي الاتجاهات، واستدلال LLM متعدد الجولات، واستخراج رسم بياني للمعرفة، واستيعاب المستندات — كل ذلك في ملف Go ثنائي واحد.
</p>

---

## البداية السريعة

```bash
git clone https://github.com/MemeryGit/LocalMem.git
cd LocalMem
go mod download
cp config/config.yaml ./config.yaml
go run ./cmd/server/       # خادم API (المنفذ 8080)
go run ./cmd/mcp/          # خادم MCP (المنفذ 8081، اختياري)
```

### المتطلبات

- **Go** 1.25+
- **Qdrant** (اختياري، للبحث المتجهي)
- **Docling / Apache Tika** (اختياري، لتحليل المستندات)

---

## الميزات الرئيسية

- **الاسترجاع الهجين** — بحث ثلاثي الاتجاهات: SQLite FTS5 (BM25) + Qdrant متجهي + رسم بياني للمعرفة، مدمجة عبر RRF (k=60)
- **دورة حياة الذاكرة** — مستويات الاحتفاظ (`permanent` / `long_term` / `standard` / `short_term` / `ephemeral`) مع معدلات تلاشي قابلة للتكوين
- **الاستدلال متعدد الجولات** — محرك Reflect للاستدلال التكراري لـ LLM على الذكريات المسترجعة
- **رسم بياني للمعرفة** — استخراج تلقائي للكيانات/العلاقات عبر LLM
- **استيعاب المستندات** — خط أنابيب: رفع → تحليل → تقطيع → تضمين → تخزين
- **خادم MCP** — دعم بروتوكول سياق النموذج (نقل SSE)
- **بحث النص الكامل CJK** — محلل FTS5 قابل للتوصيل

---

## نقاط نهاية API

جميعها تحت `/v1/`:

| المجموعة | الوصف |
|----------|-------|
| `/v1/memories` | CRUD + حذف ناعم/استعادة |
| `/v1/retrieve` | استرجاع هجين ثلاثي الاتجاهات |
| `/v1/reflect` | استدلال LLM متعدد الجولات |
| `/v1/conversations` | استيعاب المحادثات دفعة واحدة |
| `/v1/entities` | رسم بياني للمعرفة |
| `/v1/documents` | رفع/معالجة المستندات |

---

## تكامل MCP

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

---

## الرخصة

**رخصة MIT** — حقوق النشر (c) 2026 MemeryGit.

راجع [LICENSE](../../LICENSE) للتفاصيل.

---

<p align="center">
  <b>مبني بـ Go</b> • <b>يعمل بـ SQLite + Qdrant</b> • <b>متوافق مع MCP</b>
</p>
