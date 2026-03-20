-- 000_initial.sql

CREATE TABLE IF NOT EXISTS schema_migrations (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name TEXT NOT NULL UNIQUE,
    applied_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS companies (
    id               INTEGER PRIMARY KEY AUTOINCREMENT,
    name             TEXT NOT NULL,
    siren            TEXT UNIQUE,
    naf_code         TEXT,
    naf_label        TEXT,
    city             TEXT,
    department       TEXT,
    headcount_range  TEXT,
    website          TEXT,
    linkedin_url     TEXT,
    careers_page_url TEXT,
    tech_stack       TEXT,
    status           TEXT NOT NULL DEFAULT 'NEW',
    relevance_score  INTEGER DEFAULT 0,
    notes            TEXT,
    date_found       TEXT NOT NULL DEFAULT (date('now')),
    updated_at       TEXT NOT NULL DEFAULT (datetime('now')),
    primary_contact_id INTEGER,
    company_type     TEXT DEFAULT 'UNKNOWN',
    has_internal_tech_team INTEGER,
    tech_team_signals TEXT,
    company_email    TEXT
);

CREATE INDEX IF NOT EXISTS idx_companies_status ON companies(status);
CREATE INDEX IF NOT EXISTS idx_companies_city   ON companies(city);
CREATE INDEX IF NOT EXISTS idx_companies_score  ON companies(relevance_score DESC);

CREATE TABLE IF NOT EXISTS contacts (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    company_id   INTEGER NOT NULL REFERENCES companies(id) ON DELETE CASCADE,
    name         TEXT,
    role         TEXT,
    email        TEXT,
    linkedin_url TEXT,
    source       TEXT CHECK(source IN ('linkedin','careers_page','manual','guessed')),
    confidence   TEXT CHECK(confidence IN ('verified','probable','guessed','hallucinated')),
    status       TEXT NOT NULL DEFAULT 'active'
                 CHECK(status IN ('active','bounced','unsubscribed','do_not_contact')),
    notes        TEXT,
    created_at   TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at   TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_contacts_company ON contacts(company_id);
CREATE INDEX IF NOT EXISTS idx_contacts_email   ON contacts(email);

CREATE TABLE IF NOT EXISTS jobs (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    date_found      TEXT    NOT NULL DEFAULT (date('now')),
    source_site     TEXT    NOT NULL,
    type            TEXT    NOT NULL CHECK(type IN ('DIRECT','COMPANY_LEAD')),
    title           TEXT    NOT NULL,
    company         TEXT    NOT NULL,
    location        TEXT,
    contract_type   TEXT,
    tech_stack      TEXT,
    description_summary TEXT,
    apply_url       TEXT,
    careers_page_url TEXT,
    relevance_score INTEGER DEFAULT 0,
    status          TEXT    NOT NULL DEFAULT 'TO_APPLY',
    created_at      TEXT    NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT    NOT NULL DEFAULT (datetime('now')),
    UNIQUE(company, title)
);

CREATE TABLE IF NOT EXISTS drafts (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    company_id INTEGER REFERENCES companies(id) ON DELETE CASCADE,
    contact_id INTEGER REFERENCES contacts(id) ON DELETE SET NULL,
    type       TEXT CHECK(type IN ('email','linkedin')),
    subject    TEXT,
    body       TEXT NOT NULL,
    status     TEXT DEFAULT 'pending' CHECK(status IN ('pending','sent','discarded')),
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS pipeline_runs (
    id         TEXT PRIMARY KEY,
    status     TEXT NOT NULL DEFAULT 'running',
    started_at TEXT NOT NULL DEFAULT (datetime('now')),
    ended_at   TEXT
);

CREATE TABLE IF NOT EXISTS run_log (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id     TEXT NOT NULL REFERENCES pipeline_runs(id) ON DELETE CASCADE,
    step       TEXT NOT NULL,
    status     TEXT NOT NULL,
    error_msg  TEXT,
    started_at TEXT NOT NULL DEFAULT (datetime('now')),
    ended_at   TEXT
);

CREATE TABLE IF NOT EXISTS scrape_cache (
    url        TEXT PRIMARY KEY,
    content    TEXT NOT NULL,
    method     TEXT NOT NULL,
    quality    REAL NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE IF NOT EXISTS token_usage (
    id                INTEGER PRIMARY KEY AUTOINCREMENT,
    run_id            TEXT,
    task              TEXT NOT NULL,
    model             TEXT NOT NULL,
    provider          TEXT NOT NULL,
    prompt_tokens     INTEGER DEFAULT 0,
    completion_tokens INTEGER DEFAULT 0,
    cost_usd          REAL DEFAULT 0,
    is_estimated      INTEGER DEFAULT 0, -- boolean 0/1
    ts                TEXT NOT NULL DEFAULT (datetime('now'))
);
