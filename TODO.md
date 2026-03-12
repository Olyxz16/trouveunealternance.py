# JobHunter V1 Implementation TODO

## Phase 1: Core Infrastructure
- [x] Create `errors.py` (Exception hierarchy + `Result` type)
- [x] Create `llm.py` (OpenRouter client, rate limiter, usage tracking)
- [x] Database Refactor
    - [x] Create `migrations/` directory
    - [x] Create `001_contacts.sql`
    - [x] Create `002_run_log.sql`
    - [x] Create `003_llm_usage.sql`
    - [x] Create `004_scrape_cache.sql`
    - [x] Update `db.py` to include a migration runner
- [x] Create `scraper/fetcher.py` (Jina + MCP fallback + cache)

## Phase 2: Pipeline & Parsers
- [ ] Create `scraper/parsers/careers_page.py` (Generic company page parser)
- [ ] Create `scraper/pipeline.py` (Extract pipeline logic from `scraper.py`)
- [ ] Implement `@pipeline_step` decorator and `run_log` wiring

## Phase 3: Dashboard Updates
- [ ] Update `api.py` with new endpoints (`/api/runs`, `/api/usage`, `/api/contacts`)
- [ ] Update `index.html` with new tabs (Runs, Usage) and Scraping Health panel

## Phase 4: Refactoring Existing Components
- [ ] Update `classifier.py` to use `llm.py`
- [ ] Update `prospector.py` to use `llm.py` and new `contacts` table
- [ ] Update `emailer.py` to use `llm.py` and new `contacts` table
- [ ] Update `scheduler.py` (add nightly archive job)
