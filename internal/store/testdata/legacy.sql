
PRAGMA journal_mode = WAL;
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;

CREATE TABLE IF NOT EXISTS issues (
    id          INTEGER PRIMARY KEY,
    publisher   TEXT NOT NULL CHECK (publisher IN ('economist', 'wired')),
    issue_date  TEXT NOT NULL,
    source_path TEXT NOT NULL,
    imported_at TEXT NOT NULL,
    UNIQUE (publisher, issue_date)
);

CREATE INDEX IF NOT EXISTS idx_issues_publisher_date
ON issues(publisher, issue_date DESC);

CREATE TABLE IF NOT EXISTS articles (
    id               INTEGER PRIMARY KEY,
    stable_id        TEXT NOT NULL UNIQUE,
    issue_id         INTEGER NOT NULL REFERENCES issues(id) ON DELETE CASCADE,
    publisher        TEXT NOT NULL,
    issue_date       TEXT NOT NULL,
    slug             TEXT NOT NULL,
    title            TEXT NOT NULL,
    description      TEXT NOT NULL DEFAULT '',
    author           TEXT NOT NULL DEFAULT '',
    section          TEXT NOT NULL DEFAULT '',
    published_at     TEXT NOT NULL DEFAULT '',
    source_url       TEXT NOT NULL,
    body             TEXT NOT NULL,
    summary_zh       TEXT NOT NULL DEFAULT '',
    summary_provider TEXT NOT NULL DEFAULT '',
    summary_error    TEXT NOT NULL DEFAULT '',
    summarized_at    TEXT,
    UNIQUE (issue_id, slug)
);

CREATE INDEX IF NOT EXISTS idx_articles_issue ON articles(issue_id);
CREATE INDEX IF NOT EXISTS idx_articles_publisher_date ON articles(publisher, issue_date DESC);

CREATE VIRTUAL TABLE IF NOT EXISTS articles_fts
USING fts5(
    title,
    description,
    body,
    summary_zh,
    content='articles',
    content_rowid='id',
    tokenize='trigram'
);

CREATE TRIGGER IF NOT EXISTS articles_ai
AFTER INSERT ON articles BEGIN
    INSERT INTO articles_fts(rowid, title, description, body, summary_zh)
    VALUES (new.id, new.title, new.description, new.body, new.summary_zh);
END;

CREATE TRIGGER IF NOT EXISTS articles_ad
AFTER DELETE ON articles BEGIN
    INSERT INTO articles_fts(articles_fts, rowid, title, description, body, summary_zh)
    VALUES ('delete', old.id, old.title, old.description, old.body, old.summary_zh);
END;

CREATE TRIGGER IF NOT EXISTS articles_au
AFTER UPDATE ON articles BEGIN
    INSERT INTO articles_fts(articles_fts, rowid, title, description, body, summary_zh)
    VALUES ('delete', old.id, old.title, old.description, old.body, old.summary_zh);
    INSERT INTO articles_fts(rowid, title, description, body, summary_zh)
    VALUES (new.id, new.title, new.description, new.body, new.summary_zh);
END;
