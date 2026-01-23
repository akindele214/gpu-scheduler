# Context: V2 Roadmap — Efficiency, Accuracy & Conversational

**Goal:** Transform Context from a single-shot query tool into an efficient, accurate, and conversational assistant for database exploration.

**Guiding Principles:**
- Efficiency: <2s p95 latency, minimal token cost, smart caching & routing.
- Accuracy: Confidence scoring, clarifications before execution, hybrid retrieval with schema signals.
- Conversational: Multi-turn sessions, lightweight entity resolution, iterative refinement.
- Structure: Enforce JSON output from LLMs for determinism and clarity.

---

## Phase 2.0: Structured Output & Confidence (2–3 days)

**Goal:** Enforce deterministic LLM responses and enable confidence-gated execution.

### LLM JSON Schema

Intent types (suggested): `count`, `list`, `lookup`, `aggregate`, `filter`, `join`, `group`, `rank`, `temporal`, `comparison`, `exists`, `distribution`, `schema_explore`, `explain`, `help`, `conversational`, `unknown`.

```json
{
   "intent": "string (see intent types above)",
   "sql": "string (validated SQL, for single-step queries)",
   "plan": [
      {
         "step": "number (execution order)",
         "description": "string (what this step does)",
         "sql": "string (validated SQL)",
         "depends_on": ["number (step numbers this depends on)"]
      }
   ],
   "response": "string (for conversational intents; no SQL)",
   "params": ["string (positional params for parameterized queries)"],
   "assumptions": ["string (things the LLM inferred about the schema or business logic)"],
   "needs_clarification": "boolean",
   "clarification_questions": ["string (only if needs_clarification=true)"],
   "confidence": "number (0.0–1.0, e.g., 0.95)",
   "tables_used": ["string (table names referenced in SQL)"],
   "columns_used": ["string (column names referenced in SQL)"]
}
```

**Notes:**
- Use `response` when intent is conversational/help/explain/schema_explore and no SQL is needed.
- Either `sql` (single query) or `plan[]` (multi-step) should be present for data-access intents, not both. Prefer single SQL with CTEs when possible.

### Tasks

1. **Update Prompts** (`internal/llm/prompt.go`)
   - Add strict "Output JSON only" instructions.
   - Include schema with examples.
   - Inject business rules and top-k relevant history.
   - Add confidence scoring guidance (high if question unambiguous, low if multiple interpretations).

2. **Add Go Structs** (`internal/domain/llm_output.go`)
   - Define `LLMResponse` struct matching the JSON schema.
   - Implement `ParseLLMOutput(data []byte) (*LLMResponse, error)` with validation.
   - Handle partial/missing fields gracefully (use defaults).

3. **Provider JSON Mode** (`internal/llm/providers/*.go`)
   - Add system message: "You are a SQL expert. You MUST respond with JSON only."
   - Groq: Use `response_format: {"type": "json_object"}` if available, else rely on prompt.
   - Ollama: Use prompt suffix and retry logic on non-JSON.
   - Retry once if JSON parse fails (request clarification from LLM).

4. **Wire into Ask** (`cmd/context/ask.go`)
   - Parse JSON response into `LLMResponse`.
   - If `needs_clarification=true`, show questions and prompt user interactively.
   - If `confidence < 0.7`, warn user before execution.
   - Extract `tables_used` and `columns_used` for telemetry.

5. **Multi-Query Execution Engine** (`internal/query/executor.go`)
   - Detect if response has `plan[]` instead of single `sql`.
   - Execute steps sequentially; validate each SQL (SELECT-only, LIMIT enforcement) before running.
   - Store intermediate results in memory (small snapshots only; warn if result set too large).
   - If step fails, abort entire plan and surface which step + error.
   - Row caps per step (default: 1000); total execution timeout (default: 30s).
   - If plan has >3 steps or confidence <0.7, ask user for confirmation before executing.

### Deliverable

**Single-step query:**
```bash
$ context ask "How many active users?" --config config.yaml
[JSON parsed successfully]
Intent: count
Assumptions:
  - 'active' means status='active'
  - User table has a status column
Confidence: 0.92

SQL: SELECT COUNT(*) FROM users WHERE status = 'active' LIMIT 1000
Execute? (y/n/e) y
Result: 1,234
```

**Multi-step query:**
```bash
$ context ask "Top 5 countries by average order value for active users with >3 orders"
[JSON parsed successfully]
Intent: aggregate
Plan: 3 steps
  Step 1: Find active users with >3 orders
  Step 2: Calculate average order value per user
  Step 3: Aggregate by country and rank top 5
Confidence: 0.85

Execute multi-step plan? (y/n) y
[Executing step 1/3...] 542 users found
[Executing step 2/3...] Averages calculated
[Executing step 3/3...] Done

Result:
  US: $145.20
  UK: $132.50
  CA: $128.75
  DE: $121.30
  FR: $118.90
```

If confidence < 0.7:
```bash
Confidence is LOW (0.62). The system asks:
  1. Do you mean 'active users in the last 30 days' or 'users with active=true'?
Answer (1/2):
```

---

## Phase 2.1: Conversational & Sessions (3–4 days)

**Goal:** Enable multi-turn conversations with persistent context and lightweight entity resolution.

### Tasks

1. **Session Storage** (`internal/storage/sqlite.go`)
   - Add tables:
     - `sessions` (id, name, created_at, last_accessed_at, rules_snapshot)
     - `conversation_turns` (id, session_id, turn_num, user_query, llm_json, sql, result_summary, timestamp)
   - Implement `CreateSession(name)`, `LoadSession(id)`, `SaveTurn(...)`, `GetSessionContext(id, last_n_turns)`.

2. **Context Building** (`internal/app/app.go` or new `internal/context/context_builder.go`)
   - When starting a turn, fetch:
     - Last 5 conversation turns from this session.
     - Top-5 rules by keyword relevance (vector search optional for v2.1, use string matching MVP).
     - Last 3 unique tables accessed in session.
   - Inject into prompt as "Conversation history:" and "Relevant rules:".

3. **Entity Resolution (Lightweight MVP)** (`internal/domain/entity_resolver.go`)
   - Build a simple registry: `{ entity_name -> (table_name | column_name | business_rule_name) }`.
   - On follow-up queries, normalize ambiguous names (e.g., "user" → "users" table if unambiguous, else ask).
   - Use exact-match first, then Levenshtein distance for typos (e.g., "ordr" → "orders").
   - No heavy pre-indexing; lazy-load on demand.

4. **Wire Sessions into Ask** (`cmd/context/ask.go`)
   - Add flag: `--session <name>` (default: "default" or UUID).
   - Load/create session on startup.
   - After successful query, save turn to `conversation_turns`.
   - Pass session context to `BuildSQLPrompt`.

5. **Clarification Refinement**
   - If user answers a clarification question, include the answer in the next turn's prompt.
   - Track clarification acceptance (did user accept refined SQL?).

### Deliverable

```bash
$ context ask "How many active users?" --session my-analysis
[Session 'my-analysis' loaded; 2 prior turns]
Intent: count
SQL: SELECT COUNT(*) FROM users WHERE status = 'active'
Result: 1,234

$ context ask "What's their average order value?" --session my-analysis
[Session context: 'users' table from previous turn]
Intent: aggregate
Assumptions:
  - 'they' refers to the 1,234 active users from the previous query
  - 'order value' is the 'total' column in 'orders' table
SQL: SELECT AVG(o.total) FROM orders o 
     JOIN users u ON o.user_id = u.id 
     WHERE u.status = 'active'
Result: $127.50
```

Conversation history preserved and reused intelligently.

---

## Phase 2.2: Efficiency — Caching, Streaming & Routing (2–3 days)

**Goal:** Reduce latency and token cost via caching, streaming, and smart provider selection.

### Tasks

1. **Schema Caching & Digests** (`internal/storage/sqlite.go`)
   - Add table: `schema_cache` (digest, schema_json, cached_at).
   - Compute digest as hash of (table names + column list).
   - Before introspection, check if digest exists; if yes, use cached schema.
   - Add `--refresh-schema` flag to force refresh.

2. **Rule Digest & Compression** (`internal/storage/sqlite.go`)
   - Compute hash of all business_rules; include in session context only if changed.
   - Compress rule descriptions (e.g., keep first 100 chars for prompt injection).

3. **Streaming Responses** (`internal/llm/providers/*.go`, `cmd/context/ask.go`)
   - Groq: Support streaming via `stream=true` and SSE parsing.
   - Ollama: Already supports streaming; wire it up.
   - Display partial JSON (or "Generating SQL...") to user in real-time.
   - Buffer full response before parsing JSON (for 2.0 JSON validation).

4. **Provider Routing & Fallback** (`internal/llm/router.go`)
   - Add simple heuristics:
     - For simple queries (low token count estimate): use Ollama (fastest, free).
     - For complex queries (high token count): use Groq (faster inference).
     - On provider timeout/error: fallback to alternative.
   - Track latency and cost per provider; use exponential backoff.

5. **Batch Clarifications** (`cmd/context/ask.go`)
   - If multiple clarifications needed, ask all at once (reduce round trips).

### Deliverable

```bash
$ context ask "How many active users by country?" --session analysis --stream
[Schema loaded from cache (digest: abc123); 2.1ms]
[Using Groq (complex query estimated)]
Generating SQL... [0.8s]
Intent: aggregate
SQL: SELECT country, COUNT(*) as user_count ...
[Executed in 1.2s]
Result: 
  US: 456
  UK: 234
  ...

Total latency: 2.5s
```

---

## Phase 2.3: Accuracy — Hybrid Retrieval & Evaluation (3–4 days)

**Goal:** Improve SQL accuracy via context-aware retrieval and systematic evaluation.

### Tasks

1. **Hybrid Retrieval** (`internal/query/retriever.go`, `internal/storage/sqlite.go`)
   - For each query, retrieve:
     - **Vector search**: Top-5 rules by semantic similarity (embed rule names/descriptions vs. question).
       - Optional: Use OpenAI embeddings or lightweight local embedding model.
       - If unavailable, fall back to keyword search.
     - **Structural signals**: 
       - Recent tables/columns (from session history).
       - FK graph distance (nearby tables are more likely relevant).
       - Table type (fact vs. dimension, active vs. archive).
     - **Temporal signals**: 
       - Rules modified recently.
       - Queries executed recently.
   - Score and rerank: `final_score = α * vector_sim + β * recency + γ * structural_proximity`.
   - Top-k rules injected into prompt.

2. **Confidence Gating & Fallback** (`cmd/context/ask.go`)
   - If `confidence < 0.6`: Show two alternative SQLs with explanations.
   - If `confidence < 0.4`: Require manual approval before execution.
   - Track accept/reject on each confidence band for telemetry.

3. **Hallucination Tracking** (`internal/storage/sqlite.go`)
   - Add table: `hallucinations` (id, session_id, generated_sql, user_feedback, category).
   - After execution, optionally ask user: "Did this result make sense? y/n".
   - Log rejected queries to identify patterns (e.g., "LLM assumes column X exists when it doesn't").

4. **Evaluation Harness** (`tests/golden_queries_test.go`, `tests/fixtures/`)
   - Create test fixture: schema + 10–20 golden (question, expected_sql) pairs.
   - Include multi-step test cases (e.g., "Top 5 countries by avg order value for users with >3 orders").
   - Run `context ask` on each question; compare generated SQL to golden SQL (allow minor variations).
   - For multi-step plans: validate step count, dependencies, and final result equivalence.
   - Metrics:
     - Exact match rate (%).
     - Semantic equivalence (execute both, compare result sets).
     - Multi-step plan correctness (step order, dependencies valid).
     - Confidence distribution.
     - Hallucination rate (user rejections / total queries).

5. **Schema Drift Detection** (`internal/database/introspector.go`)
   - On schema refresh, compare to previous; log new/dropped/renamed columns.
   - Warn if newly added columns might invalidate cached business_rules.

### Deliverable

```bash
$ context ask "Top 10 customers by revenue?" --session analysis --eval
[Hybrid retrieval: 5 rules, 3 recent tables, FK proximity=0.8]
Intent: aggregate
Assumptions:
  - 'customers' means the 'users' table (rule: "customer_type='business'")
  - 'revenue' means sum of order totals
Confidence: 0.88
SQL: SELECT u.id, u.name, SUM(o.total) as revenue FROM users u 
     JOIN orders o ON o.user_id = u.id 
     WHERE u.customer_type = 'business' 
     GROUP BY u.id, u.name 
     ORDER BY revenue DESC LIMIT 10
Result: [top 10 customers]

Did this make sense? (y/n) y
[Logged as correct]
```

---

## Phase 2.4: Polish & Telemetry (1–2 days)

**Goal:** Improve UX, add observability, and prepare for production.

### Tasks

1. **Telemetry Logging** (`internal/telemetry/logger.go`)
   - Track per-query: latency, token cost, confidence, provider used, tables/columns accessed, user acceptance.
   - Add flags: `--verbose`, `--telemetry-file <path>`.
   - Aggregate metrics: daily/weekly reports on accuracy, latency, cost.

2. **Error Messages & Fallbacks** (`cmd/context/ask.go`, providers)
   - User-friendly error messages for schema issues, LLM failures, SQL parse errors.
   - Fallback chain: Groq → Ollama → user edits SQL interactively.

3. **Command UX Polish** (`cmd/context/ask.go`)
   - Add flags:
     - `--confidence-threshold <0.0-1.0>`: Auto-execute if ≥ threshold, else ask.
     - `--format {table, json, csv}`: Output format.
     - `--rows <N>`: Limit result rows (default: 100).
   - Interactive mode: Show SQL, result preview, allow edits before execution.

4. **Documentation** (`docs/`)
   - Expand `docs/README.md` with v2 examples (sessions, clarifications, etc.).
   - Add troubleshooting guide.
   - Update `docs/ARCHITECTURE.md` with hybrid retrieval and streaming.

5. **Command Aliases & Helpers** (Makefile, completions)
   - `context analyze <session-name>`: alias for `ask --session <session-name>`.
   - Tab completion for session names, table names, column names.

### Deliverable

Polished CLI with helpful error messages, rich telemetry, and comprehensive docs.

---

## Phase 2.5: Semantic Search with Embeddings (3–4 days)

**Goal:** Retrieve the most relevant rules/history via semantic similarity, reducing misses from phrasing differences.

### Tasks

1. **Embedding Pipeline** (`internal/embedding/`) 
   - Pick model: OpenAI text-embedding-3 (cloud) or local small model (optional) with ~1536 dims.
   - Embed: business_rules (name + description), table/column descriptions, recent query history summaries.
   - Store vectors in SQLite-backed FAISS index (local) with metadata; Pinecone as optional remote backend.

2. **Index & Retrieval** (`internal/retriever/`) 
   - Add vector search: top-k by cosine similarity; fallback to keyword if embeddings unavailable.
   - Hybrid scoring: `final_score = α * vector_sim + β * recency + γ * structural_proximity`.
   - Expose a `RetrieveContext(question)` that returns rules + entities ranked.

3. **Refresh & Drift Handling** 
   - Re-embed on rule changes and schema drift detection; background job or on-write update.
   - Cache embeddings with digests to avoid recompute.

4. **Evaluation** 
   - Track retrieval precision@k and latency; compare against keyword-only baseline.
   - Add flag `--no-embeddings` for A/B testing.

### Deliverable

Semantic retrieval plugged into prompt building:

```bash
$ context ask "Average order value for premium customers in Q4" --session analysis
[Retrieval] vector top-5 rules, hybrid score applied (α=0.6, β=0.2, γ=0.2)
Intent: aggregate
Assumptions: 'premium' = customer_tier='premium' (rule)
SQL: SELECT AVG(o.total) ...
```

---

## Timeline & Effort Estimate

| Phase | Features | Estimated Days | Dependencies |
|-------|----------|---|---|
| 2.0   | JSON schema, confidence, clarifications | 2–3 | None |
| 2.1   | Sessions, entity resolution, context building | 3–4 | 2.0 |
| 2.2   | Caching, streaming, provider routing | 2–3 | 2.0, 2.1 |
| 2.3   | Hybrid retrieval, evaluation harness, hallucination tracking | 3–4 | 2.0, 2.1, 2.2 |
| 2.4   | Telemetry, UX polish, docs | 1–2 | 2.0–2.3 |
| 2.5   | Semantic search with embeddings | 3–4 | 2.0–2.3 |
| **Total** | | **14–20 days** (part-time) | — |

---

## Success Metrics

- **Efficiency**: p95 latency < 2s; avg token cost < 500 tokens/query; cache hit rate > 70%.
- **Accuracy**: SQL success rate > 90%; user acceptance rate > 85%; hallucination rate < 10%.
- **Conversational**: Avg session length > 3 turns; clarification acceptance > 80%; entity resolution accuracy > 95%.

---

## Nice-to-Have (v2.5+)

- ReAct-style agent loop: "Run query → Check results → Ask follow-up → Refine SQL" for complex questions.
- Visual query builder (GUI / TUI).
- Multi-database support (auto-routing based on schema).
- Export conversations to markdown/HTML.
