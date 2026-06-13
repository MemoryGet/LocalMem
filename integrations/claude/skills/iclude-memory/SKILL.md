# iclude Memory Protocol

Use this skill when working with the iclude memory system to ensure memories are stored with high quality.

TRIGGER when: user asks to save, remember, or store something ("记住这个", "帮我存一下", "把这个记下来", "save this", "remember this"); about to call iclude_retain; user asks how to use iclude memory tools; a decision, preference, or important fact emerges in conversation that should be persisted across sessions.

## When to Retain

Retain when a semantic unit is complete:
- A new fact about a person, project, or system was established
- A decision was made
- A user preference or constraint was expressed
- A task or milestone completed
- Context needed in future sessions

## When NOT to Retain

- Confirmations ("OK", "Got it", "Sure")
- Questions without answers yet
- Transient in-progress steps
- Information already in the codebase

## Mandatory Sequence

Always follow this sequence when retaining:

1. **iclude_recall** — find related prior memories
   ```
   iclude_recall(query="<key terms of current topic>", limit=5)
   ```
2. **Evaluate** — are any results topically related to what you're about to retain?
3. **iclude_retain** — with summary and derived_from
   ```
   iclude_retain(
     content     = "raw or summarized exchange",
     summary     = "2-3 sentences, third person, no pronouns, all entities named",
     derived_from = ["IDs from step 1 that are related"]
   )
   ```

## Summary Writing Rules

- **Third person**: "Caroline was promoted", NOT "She was promoted"
- **No pronouns**: Name every entity explicitly
- **Self-contained**: Readable without surrounding context
- **2-3 sentences maximum**
- **Cover**: who, what, when (if relevant)

### Examples

| Raw turn | Good summary |
|---|---|
| "She got the job!" | "Alice received a job offer from Google as senior engineer." |
| "We chose PostgreSQL" | "The team chose PostgreSQL for the primary database, prioritizing ACID compliance." |
| "OK" | *(skip — confirmation)* |

## Why Recall-Before-Retain

Prevents duplicate memories. Builds `derived_from` links that allow the retrieval engine to surface the full history of a topic when queries revisit it in future sessions.
