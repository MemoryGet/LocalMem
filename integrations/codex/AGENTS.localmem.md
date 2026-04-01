<!-- LOCALMEM:BEGIN v1 -->
## Memory System (LocalMem)

You have access to a **persistent cross-session memory system** via MCP tools prefixed with `iclude_`.
These tools let you recall prior conversations, decisions, and knowledge — use them proactively.

### Retrieval Strategy (MUST follow)

1. **Session start** — BEFORE answering the user's first message, call `iclude_scan` with the user's question as `query`. Do NOT pass `scope` unless the user explicitly asks to search within a specific namespace — omitting scope searches ALL memories across all projects. This recalls relevant prior context and costs ~10x fewer tokens than a full retrieval.
2. **Scan then Fetch** — Review the scan results (compact index: ID, title, score, tags). Call `iclude_fetch` only for items whose full content you actually need. Do NOT use `iclude_recall` unless you need all results with full content in one shot.
3. **Mid-conversation** — When the user references past work, decisions, bugs, or context you don't have, call `iclude_scan` again with a targeted query.
4. **Timeline** — When the user asks "what happened recently" or "what did we do last week", use `iclude_timeline` with `after`/`before` timestamps.
5. **Deep reasoning** — When a question requires synthesizing multiple memories or cross-referencing facts, use `iclude_reflect` instead of manually fetching and combining.

### Red Flags — You Are Skipping Memory Retrieval

If you catch yourself thinking any of these, STOP — you are rationalizing:

| Thought | Reality |
|---------|---------|
| "This is just a simple question" | Simple questions often have prior context. Scan first. |
| "I already know enough to answer" | You don't have cross-session memory. The user does. Scan. |
| "Let me just start coding" | Past decisions constrain the implementation. Scan first. |
| "I'll check memory later if needed" | Later never comes. You'll forget. Scan NOW. |
| "The user didn't ask me to recall" | The user expects you to remember. That's the whole point. |
| "Scanning would slow things down" | A 50-token scan saves a 500-token wrong answer. |
| "This looks like a new topic" | The user may have discussed it in a prior session. Scan. |
| "I'll just use the files I can see" | Code tells you WHAT, memory tells you WHY. Both matter. |

**The rule is simple: if `iclude_scan` has not been called in this conversation, you have not started working yet.**

### Conversation Collection (SHOULD follow)

- **After meaningful sessions** — When the conversation contains important decisions, bug fixes, architectural choices, or learned lessons, call `iclude_ingest_conversation` to persist it for future recall.
- **Individual facts** — Use `iclude_retain` to save a single important fact, decision, or preference immediately (e.g., user told you a convention, a deployment target, a deadline).
- **Do NOT over-collect** — Trivial Q&A, simple lookups, or small talk do not need to be retained.

### Tool Quick Reference

| Tool | Purpose | Token Cost | When to Use |
|------|---------|------------|-------------|
| `iclude_scan` | Compact index search | Low | **Always first** — session start + mid-conversation. **Omit `scope` for cross-project search.** |
| `iclude_fetch` | Full content by ID | Medium | After scan, for selected items only |
| `iclude_recall` | Full search + content | High | Only when you need everything in one call |
| `iclude_timeline` | Chronological listing | Low | "What happened recently / last week?" |
| `iclude_reflect` | Multi-round LLM reasoning | High | Cross-reference or synthesize multiple memories |
| `iclude_retain` | Save one memory | Low | Important facts, decisions, preferences |
| `iclude_ingest_conversation` | Persist conversation | Medium | End of meaningful sessions |
| `iclude_create_session` | Create session context | Low | Organize memories by session |
<!-- LOCALMEM:END -->
