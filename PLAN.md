# JobHunter — Technical Roadmap

> Generated from planning session. All architectural decisions are locked.
> Pick up any section and start building — context is fully preserved here.

---

## Core Architecture Decisions

| Concern | Decision |
|---|---|
| LLM provider | OpenRouter, OpenAI-compatible API, one model in `.env` for now |
| Rate limit response | Exponential backoff + retry, no fallback model yet |
| Public scraping | Jina first → MCP fallback → `NEEDS_REVIEW` if both fail |
| Auth'd scraping | MCP directly (LinkedIn, etc.) |
| Pipeline mode | Automatic by default, `--interactive` flag available |
| Raw content storage | SQLite for recent runs, archive to `.md` on disk after 30 days |
| MCP lifecycle | Started manually before pipeline runs — no daemon assumption |

---

## Model Selection

### Chosen Models

| Task | Model | Pricing (input/output per 1M tokens) | Reason |
|---|---|---|---|
| Classification | `openai/gpt-oss-120b:free` | $0 / $0 | Free tier, 131K context, native JSON, fast enough for bulk classification |
| Everything else (default) | `google/gemini-2.5-flash-lite` | $0.10 / $0.40 | Best cost/quality balance for extraction; 1M context window; thinking toggle available |

### Why not the alternatives considered

**LFM-2.5-1.2B-Thinking** — rejected. Too small (1.2B params) for reliable JSON schema compliance on messy Jina-fetched markdown. 32K context limit is hit easily by a rich job listing + system prompt + schema. Cost advantage disappears on retry overhead.

**Gemini 2.5 Flash (full)** — same quality as Flash-Lite for these tasks, but output is $2.50/M vs $0.40/M. Since extraction tasks produce large JSON outputs, the output price dominates — Flash-Lite wins on cost with no meaningful quality trade-off.

**DeepSeek V3.2** — strong model ($0.25/$0.38, 163K context), good fallback option. Loses to Flash-Lite only on context window (163K vs 1M) which matters when enriching companies with large LinkedIn pages. Keep as a fallback option in config.

### Pricing Estimate (typical run, 50 companies)

| Task | Model | Approx. cost |
|---|---|---|
| 50 classifications | gpt-oss-120b:free | $0.00 |
| 50 extractions | gemini-2.5-flash-lite | ~$0.03 |
| 20 enrichments | gemini-2.5-flash-lite | ~$0.02 |
| 10 drafts (V2) | gemini-2.5-flash-lite | ~$0.01 |
| **Total** | | **~$0.06** |

A full week of daily runs costs well under $1.

### Free Tier Limits (gpt-oss-120b:free)

20 requests/minute, 200 requests/day. Fine for classification bursts. If you hit the daily cap, the `llm.py` client falls back to `gemini-2.5-flash-lite` automatically for that task (configured via `OPENROUTER_MODEL_CLASSIFY_FALLBACK`).

### Per-Task Config (future, zero code change)

```env
OPENROUTER_MODEL_CLASSIFY=openai/gpt-oss-120b:free
OPENROUTER_MODEL_CLASSIFY_FALLBACK=google/gemini-2.5-flash-lite
OPENROUTER_MODEL_ENRICH=google/gemini-2.5-flash-lite
OPENROUTER_MODEL_DRAFT=google/gemini-2.5-flash-lite
```

Until then, set `OPENROUTER_MODEL=google/gemini-2.5-flash-lite` as the single default.



### On the Go Rewrite

Don't do it in V1. The real bottleneck is network I/O (scraping, LLM calls) — Python asyncio handles that fine. Go would shine for single-binary deployment and long-running worker components. The right move: finish V1–V2 in Python, then optionally port the backend API + scheduler + llm client to Go for V2.5. The dashboard frontend stays the same either way.

**Key Go libraries when the time comes:** `chi` (router), `sqlc` (type-safe queries from SQL schema), `golang.org/x/time/rate` (rate limiter), OpenAI Go SDK (works with OpenRouter), `mattn/go-sqlite3`.

---

## V1 — Production-Grade Core

### `errors.py` — Exception Hierarchy

The first file to write. Everything else imports from it.

```
JobHunterError
  ScrapingError
    JinaError(url, status_code, response_preview)
    MCPError(url, reason)
    EmptyContentError(url, method)
  LLMError
    RateLimitError(retry_after: float, model: str)
    ParseError(raw_response: str, expected_schema: str)
    ModelError(model: str, status_code: int)
  EnrichmentError(company_id: int, step: str)
  DatabaseError
```

Every pipeline function returns a `Result[T]` — either `Ok(value)` or `Err(error)` — instead of raising. A `@pipeline_step(run_id, company_id, step_name)` decorator wraps any function, catches exceptions, writes to `run_log`, and converts raises into `Err`. The pipeline loop inspects results and continues — it never crashes on a single company failure.

After 3 consecutive `Err` entries for the same `(company_id, step)`, that company gets status `FAILED` and is excluded from future runs until manually reset via the dashboard or CLI.

---

### `llm.py` — OpenRouter Client

```python
class LLMClient:
    async def complete(self, system: str, user: str) -> str
    async def complete_json(self, system: str, user: str, schema: type[BaseModel]) -> BaseModel
```

**Rate limiter:** Token bucket, one bucket total. Configured by `OPENROUTER_RPM` in `.env` (default 60). `asyncio.Semaphore` limits concurrent in-flight requests.

**Retry logic:** On 429, read `Retry-After` header if present, otherwise exponential backoff starting at 2s, multiplied by 2 with ±20% jitter, capped at 120s. Max 4 retries, then raises `RateLimitError`. On 5xx, same backoff but only 2 retries then raises `ModelError`.

**`complete_json`:** Uses OpenRouter's `response_format: {type: "json_object"}` where supported. Parses with Pydantic. On `ValidationError` or `JSONDecodeError`, retries once with the error appended to the prompt. Second failure raises `ParseError` with raw response stored for debugging.

**Usage tracking:** Every response writes `(run_id, task, model, prompt_tokens, completion_tokens, cost_usd)` to `llm_usage`. OpenRouter returns `usage` and `x-openrouter-cost` on every response.

**Config in `.env`:**
```env
OPENROUTER_API_KEY=sk-or-...
OPENROUTER_MODEL=google/gemini-2.5-flash-lite   # single default model
OPENROUTER_RPM=60
OPENROUTER_MAX_TOKENS=2048
```

**Per-task model config (future)** — add these keys and the client picks them up automatically, zero code change:
```env
OPENROUTER_MODEL_CLASSIFY=openai/gpt-oss-120b:free
OPENROUTER_MODEL_CLASSIFY_FALLBACK=google/gemini-2.5-flash-lite
OPENROUTER_MODEL_ENRICH=google/gemini-2.5-flash-lite
OPENROUTER_MODEL_DRAFT=google/gemini-2.5-flash-lite
```

---

### `scraper/fetcher.py` — Jina → MCP → `NEEDS_REVIEW`

```python
async def fetch_url(url: str, *, force_mcp: bool = False) -> FetchResult

@dataclass
class FetchResult:
    url: str
    content_md: str
    method: Literal["jina", "mcp", "cache"]
    fetched_at: datetime
    quality_score: float  # 0.0–1.0
```

**Flow:**
1. Check `scrape_cache` — if found and not expired, return immediately with `method="cache"`
2. Try Jina: `GET https://r.jina.ai/{url}`, quality check (length > 800 chars, no error strings, expected content signals present)
3. If Jina fails quality check → try MCP (`browser_navigate` + `browser_get_content` via `http://localhost:3000`)
4. If MCP unreachable → raise `MCPError` immediately (don't silently fail)
5. If both fail → write `run_log` entry with `status=needs_review`, return `Err(EmptyContentError)`
6. On success → write to `scrape_cache` with appropriate TTL

**Archive job** (runs nightly via `scheduler.py`): moves `scrape_cache` rows older than 30 days to `data/cache/{YYYY-MM}/{domain}/{hash}.md` and deletes them from SQLite.

**Domain TTL config in `.env`:**
```env
JINA_CACHE_TTL_DEFAULT=86400             # 24h — company career pages
MCP_HOST=http://localhost:3000
```

---

### `scraper/parsers/` — Company Page Extraction

V1 only needs one parser: `careers_page.py`, a generic parser for company career pages and LinkedIn profiles. Job board parsers (WTTJ, Indeed, Lesjeudis) are a V3 concern.

Each parser receives fetched markdown and returns a Pydantic model. The LLM does the extraction — parsers provide site-specific system prompts.

`RawCompanyPage` fields:
- name, description, city, headcount
- tech_stack, github_org, engineering_blog_url
- open_source_mentioned, infrastructure_keywords
- contact_name, contact_role, contact_linkedin, contact_email
- `company_type`: `TECH` | `TECH_ADJACENT` | `NON_TECH`
- `has_internal_tech_team`: bool — true if non-tech company with evidence of an internal IT/infra/dev team
- `tech_team_signals`: list of strings — evidence found (e.g. "posts DevOps job listings", "has a /tech blog", "digital subsidiary mentioned")

---

### Company Type Classification

This is a three-tier problem, not binary. The classifier must distinguish:

| Type | Definition | Examples | Internship angle |
|---|---|---|---|
| `TECH` | Core business is software/infra — the product IS tech | SaaS, cloud providers, dev tools, DevOps shops | Direct: they have an engineering team by definition |
| `TECH_ADJACENT` | Non-tech business but large enough to have an internal IT/infra/dev team | Retailer with a tech division, logistics company running their own platform, hospital with an IT department | Indirect: target the internal team, not the product team |
| `NON_TECH` | No meaningful technical needs at intern level | Law firm, bakery, small accountancy | Skip |

The `TECH_ADJACENT` category is the important new addition. A company like Leroy Merlin, SNCF, or a mid-size regional bank isn't a tech company — but they absolutely have DevOps engineers, infrastructure teams, and internal platforms. These are valid prospects that the current binary filter discards entirely.

**Signals for `TECH_ADJACENT`:**
- Large headcount (100+) in a non-tech NAF code
- Has a known digital subsidiary or "digital transformation" mentions
- Job postings for infra/dev roles despite non-tech primary activity
- Has a `.io` or tech-looking careers page despite non-tech sector

**How it affects the pipeline:**

Classification happens in two stages:

1. **SIRENE pre-filter (heuristic, free):** NAF code alone. Pure `TECH` NAF codes (62xx, 63xx) pass automatically. Non-tech NAF codes with headcount ≥ 100 go to the LLM for `TECH_ADJACENT` evaluation. Non-tech NAF codes with headcount < 100 are skipped without an LLM call.

2. **LLM classifier (on enrichment):** Given company description + website content, outputs `company_type` + `has_internal_tech_team` (bool) + `tech_team_signals` (list). This refines the heuristic and catches edge cases.

**Scoring adjustments by type:**
- `TECH`: score 1–10 based on stack relevance as before
- `TECH_ADJACENT`: score capped at 7 (harder to land, longer shot) — but still worth contacting if score ≥ 5
- `NON_TECH`: score 0, status → `NOT_TECH`, excluded from enrichment

**Contact strategy differs by type (V2):**
- `TECH`: target CTO / tech lead / engineering manager
- `TECH_ADJACENT`: target IT director / infrastructure manager / CIO — NOT the general HR team

---

### Database — Migration Files

Applied in order by `db.py` on startup. A `schema_migrations` table tracks which have run.

#### `001_contacts.sql`

```sql
CREATE TABLE contacts (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  company_id   INTEGER NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
  name         TEXT,
  role         TEXT,
  email        TEXT,
  linkedin_url TEXT,
  source       TEXT CHECK(source IN ('linkedin','careers_page','manual','guessed')),
  confidence   TEXT CHECK(confidence IN ('verified','probable','guessed')),
  status       TEXT NOT NULL DEFAULT 'active'
               CHECK(status IN ('active','bounced','unsubscribed','do_not_contact')),
  notes        TEXT,
  created_at   TEXT NOT NULL DEFAULT (datetime('now')),
  updated_at   TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_contacts_company ON contacts(company_id);
CREATE INDEX idx_contacts_email   ON contacts(email);

-- Migrate existing data
INSERT INTO contacts (company_id, name, role, email, linkedin_url, source, confidence)
SELECT id, contact_name, contact_role, contact_email, contact_linkedin,
       'linkedin', 'probable'
FROM companies
WHERE contact_name IS NOT NULL OR contact_email IS NOT NULL;

ALTER TABLE companies ADD COLUMN primary_contact_id INTEGER REFERENCES contacts(id);

UPDATE companies SET primary_contact_id = (
  SELECT id FROM contacts WHERE company_id = companies.id LIMIT 1
);

-- Add company type classification
ALTER TABLE companies ADD COLUMN company_type TEXT DEFAULT 'UNKNOWN'
  CHECK(company_type IN ('TECH', 'TECH_ADJACENT', 'NON_TECH', 'UNKNOWN'));
ALTER TABLE companies ADD COLUMN has_internal_tech_team INTEGER DEFAULT NULL; -- boolean
ALTER TABLE companies ADD COLUMN tech_team_signals TEXT; -- comma-separated evidence
```

#### `002_run_log.sql`

```sql
CREATE TABLE run_log (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id      TEXT NOT NULL,
  company_id  INTEGER REFERENCES companies(id),
  job_id      INTEGER REFERENCES jobs(id),
  step        TEXT NOT NULL,
  status      TEXT NOT NULL CHECK(status IN ('ok','error','skipped','needs_review')),
  error_type  TEXT,
  error_msg   TEXT,
  duration_ms INTEGER,
  ts          TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_run_log_run_id     ON run_log(run_id);
CREATE INDEX idx_run_log_company_id ON run_log(company_id);
CREATE INDEX idx_run_log_status     ON run_log(status);
```

#### `003_llm_usage.sql`

```sql
CREATE TABLE llm_usage (
  id                INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id            TEXT,
  step              TEXT,
  model             TEXT NOT NULL,
  prompt_tokens     INTEGER,
  completion_tokens INTEGER,
  cost_usd          REAL,
  ts                TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_llm_usage_ts ON llm_usage(ts);
```

#### `004_scrape_cache.sql`

```sql
CREATE TABLE scrape_cache (
  id          INTEGER PRIMARY KEY AUTOINCREMENT,
  url         TEXT NOT NULL UNIQUE,
  method      TEXT NOT NULL CHECK(method IN ('jina','mcp','manual')),
  content_md  TEXT NOT NULL,
  quality     REAL NOT NULL DEFAULT 1.0,
  fetched_at  TEXT NOT NULL DEFAULT (datetime('now')),
  expires_at  TEXT NOT NULL
);
CREATE INDEX idx_scrape_cache_url        ON scrape_cache(url);
CREATE INDEX idx_scrape_cache_expires_at ON scrape_cache(expires_at);
```

---

### Dashboard — V1 New Panels

Single-file `index.html` gains two new tabs and one new sidebar panel.

**Runs tab** (`GET /api/runs`, `GET /api/runs/{run_id}`):
- Table of pipeline runs, newest first
- Per-run: run ID, start time, duration, companies processed, ok/error/skipped/needs_review counts
- Click to expand: per-company × per-step grid, color-coded (green/red/yellow/grey)
- Error cells: tooltip with `error_type: error_msg` on hover
- `NEEDS_REVIEW` cells: link to company detail panel

**Usage tab** (`GET /api/usage/today`, `GET /api/usage/history`):
- Today: total requests, total tokens, total cost USD, rate limit hits, parse errors
- Hourly cost bar chart (pure SVG, no library)
- 30-day history table with daily totals

**Scraping health panel** (sidebar, not a tab):
- Jina success rate last 24h (e.g. "47/52 ok")
- MCP calls last 24h
- `NEEDS_REVIEW` queue count with link to filtered prospects view
- Refreshes every 30s via lightweight poll

**Contacts panel in company detail:**
- List of contacts with name, role, source badge, confidence badge, status badge
- Primary contact marked with star
- Company type badge (`TECH` / `TECH_ADJACENT` / `NON_TECH`) shown prominently in header
- For `TECH_ADJACENT` companies: a note showing the `tech_team_signals` that justified inclusion
- Read-only in V1 — contacts managed by enrichment pipeline
- "Re-enrich" button triggers enrichment for this specific company

---

## V2 — Cold Outreach Engine

### New `drafts` Table

```sql
CREATE TABLE drafts (
  id           INTEGER PRIMARY KEY AUTOINCREMENT,
  company_id   INTEGER NOT NULL REFERENCES companies(id),
  contact_id   INTEGER REFERENCES contacts(id),
  type         TEXT NOT NULL CHECK(type IN ('email','linkedin')),
  subject      TEXT,
  body         TEXT NOT NULL,
  model        TEXT,
  prompt_hash  TEXT,
  status       TEXT NOT NULL DEFAULT 'draft'
               CHECK(status IN ('draft','edited','sent','bounced')),
  generated_at TEXT NOT NULL DEFAULT (datetime('now')),
  edited_at    TEXT,
  sent_at      TEXT
);
```

### Generation

Inputs per draft: company description, tech stack, linked job postings (from V3 or empty), contact name + role, and `profile.json` (your resume as structured data — projects, skills, school, availability). LLM picks the 2–3 most relevant signals per company. Email and LinkedIn message generated in one call, stored as two separate `drafts` rows.

Draft generation is per-contact, not per-company. The angle varies by both contact role and company type:
- `TECH` company + CTO/tech lead → technically-angled, stack-specific
- `TECH` company + HR → impact/team-fit angle
- `TECH_ADJACENT` company → emphasise ability to work in a non-pure-tech environment, interest in internal tooling and infrastructure, adaptability

### Dashboard Outreach UI

In the company detail panel, an **Outreach** section:
- Inline editable textarea (saves on blur via `PATCH /api/drafts/{id}`)
- Regenerate button with optional instruction hint (e.g. "be more casual")
- Send email: calls `/api/drafts/{id}/send` → SMTP
- LinkedIn message: "Copy + Open Profile" button — copies text, opens contact's LinkedIn URL in new tab
- Status timeline: Draft → Edited → Sent → Replied with timestamps

### Follow-up Tracking

`follow_ups` table. At 7 days post-send, company surfaces for follow-up nudge. LLM generates short follow-up referencing the first message (stored in `drafts`).

### Email Sending

SMTP stays as primary. `EMAIL_PROVIDER=smtp|resend` config flag for optional Resend API.

---

## V2.5 — Optional Go Migration

Port `api.py` + `scheduler.py` + `llm.py` + `db.py` to Go. Keep scraper in Python if MCP integration is easier there. Both processes share the SQLite file.

**Go libraries:** `chi`, `sqlc`, `golang.org/x/time/rate`, OpenAI Go SDK, `mattn/go-sqlite3`.

---

## V3 — Job Opportunity Scraping

### Sources (priority order)
1. Welcome to the Jungle — best structured data
2. Indeed France — high volume
3. LinkedIn Jobs — richest but restricted, use MCP session
4. Lesjeudis — smaller, less competitive

### Architecture

Each source is a `scraper/parsers/{site}.py` plugin with:
- `search(query: str, location: str) -> list[str]` — returns listing URLs
- `parse_listing(md: str) -> RawListing` — extracts structured data

Jina for WTTJ, Indeed, Lesjeudis. MCP for LinkedIn Jobs.

### Company Linking

Fuzzy match scraped company names against `companies` table using `rapidfuzz.fuzz.token_sort_ratio`, threshold 85. Matches link the job to the company and increment `relevance_score` by 1 (capped at 10).

Dashboard "hot leads" filter: companies with both a contact found AND a recent matched job posting.

Draft generation (V2) automatically pulls the most recent matched posting as context.

---

## Final File Structure

```
jobhunter/
  jobhunter.py             CLI entry point (interface unchanged)
  errors.py                ← NEW: exception hierarchy + Result type
  llm.py                   ← NEW: OpenRouter client, rate limiter, usage tracking
  db.py                    ← UPDATED: migration runner added
  classifier.py            ← UPDATED: uses llm.py
  emailer.py               ← UPDATED: uses llm.py, contacts table
  guesser.py               (unchanged)
  prospector.py            ← UPDATED: uses llm.py, contacts table
  scheduler.py             ← UPDATED: + nightly archive job
  api.py                   ← UPDATED: + /api/runs, /api/usage, /api/contacts endpoints
  scraper/
    __init__.py
    fetcher.py             ← NEW: Jina → MCP fallback, cache
    pipeline.py            ← EXTRACTED from scraper.py
    parsers/
      __init__.py
      careers_page.py      ← NEW: generic company/LinkedIn page parser
      # wttj.py, indeed.py, lesjeudis.py → V3
  migrations/
    001_contacts.sql
    002_run_log.sql
    003_llm_usage.sql
    004_scrape_cache.sql
  static/
    index.html             ← UPDATED: Runs tab, Usage tab, scraping health panel
  data/
    cache/                 ← archived markdown files (YYYY-MM/domain/hash.md)
  emails/
  logs/
  profile.json             ← V2: resume as structured data
  .env
  PLAN.md                  ← this file
```

---

## Build Order

| Step | What | Time estimate |
|---|---|---|
| 1 | `errors.py` — exception hierarchy + `Result` type | 1–2h |
| 2 | `llm.py` — OpenRouter client, rate limiter, usage tracking | ~4h |
| 3 | Migration SQL files + `db.py` migration runner | ~3h |
| 4 | `scraper/fetcher.py` — Jina + MCP fallback + cache | ~1 day |
| 5 | `scraper/parsers/careers_page.py` — generic company page parser | ~3h |
| 6 | `@pipeline_step` decorator + run_log wiring | ~3h |
| 7 | Dashboard: Runs tab + Usage tab + scraping health panel | ~1 day |
| **V1 done** | | |
| 8 | `profile.json` schema + draft generation per-contact | ~1 day |
| 9 | Dashboard outreach UI (edit, send, status timeline) | ~1 day |
| 10 | Follow-up tracking | ~3h |
| **V2 done** | | |
| 11 | Job board scraper plugins (WTTJ, Indeed, LinkedIn, Lesjeudis) | ~2 days |
| 12 | Company fuzzy matching + relevance scoring | ~3h |
| 13 | Dashboard hot leads filter + job tab in company detail | ~3h |
| **V3 done** | | |

---

## Future Considerations

### Additional Data Sources

The current SIRENE seed is good but has a real weakness: it tells you nothing about whether a company is actually tech. The following sources could significantly reduce wasted enrichment calls on dormant shells and one-person consultancies, and improve starting data quality. Not planned for any specific version — revisit after V1 is stable.

**`recherche-entreprises.api.gouv.fr`** — Free, no auth, daily updates. Synthesizes SIRENE + RNE. Returns `dirigeants` (legal executives — often the founder/CTO at small companies). Useful as a live lookup to get a pre-enrichment contact signal before hitting LinkedIn, saving LLM calls on companies where the name is already available.

**INPI RNE API (`data.inpi.fr`)** — Annual accounts in JSON. Gives exact revenue and headcount figures. Could be used as a pre-filter before enrichment: skip companies with revenue below or above a threshold. Reduces wasted LLM calls significantly.

**French Tech 5000 CSV (Salesdorado)** — ~5,000 French digital companies enriched with SIREN, LinkedIn company URLs, headcount. The LinkedIn URLs are the key value — skip the search step in enrichment entirely. Free download, last updated 2021, so treat as a seed not a live source.

**French Tech Next40/120** — 120 high-signal scale-ups. Mostly too large for intern cold emails, but useful in V3 for job scraping context. Free CSV with LinkedIn URLs available via Datablist.

If integrated, the ideal pipeline would be:
```
SIRENE          → bulk seed
INPI RNE        → pre-filter micro/dormant
Recherche Ent.  → add dirigeant name
FrenchTech5k    → add LinkedIn URL if matched
Jina/MCP        → full enrichment with better starting data
```
