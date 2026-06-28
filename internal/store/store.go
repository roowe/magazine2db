package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"magazine2db/internal/domain"
)

const schema = `
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
`

// DB owns the shared magazine SQLite database.
type DB struct {
	db *sql.DB
}

// Open initializes the database and verifies FTS5 trigram support.
func Open(path string) (*DB, error) {
	if path == "" {
		return nil, errors.New("database path is empty")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create database directory: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`CREATE VIRTUAL TABLE temp.fts_probe USING fts5(x, tokenize='trigram')`); err != nil {
		db.Close()
		return nil, fmt.Errorf("SQLite FTS5 trigram is unavailable: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize schema: %w", err)
	}
	return &DB{db: db}, nil
}

// Close closes the database.
func (d *DB) Close() error {
	return d.db.Close()
}

// HasIssue reports whether publisher+issue_date is already present.
func (d *DB) HasIssue(ctx context.Context, publisher, issueDate string) (bool, error) {
	var exists int
	err := d.db.QueryRowContext(ctx,
		`SELECT EXISTS(SELECT 1 FROM issues WHERE publisher = ? AND issue_date = ?)`,
		publisher, issueDate,
	).Scan(&exists)
	return exists == 1, err
}

// InsertIssue atomically stores an issue and removes issues older than the latest keep count.
func (d *DB) InsertIssue(ctx context.Context, issue domain.Issue, keep int) error {
	if keep < 1 {
		return errors.New("retention count must be positive")
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin ingest transaction: %w", err)
	}
	defer tx.Rollback()

	result, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO issues(publisher, issue_date, source_path, imported_at)
		 VALUES (?, ?, ?, ?)`,
		issue.Publisher, issue.IssueDate, issue.SourcePath, time.Now().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("insert issue: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check inserted issue: %w", err)
	}
	if rows == 0 {
		return nil
	}
	issueID, err := result.LastInsertId()
	if err != nil {
		return fmt.Errorf("read issue id: %w", err)
	}

	stmt, err := tx.PrepareContext(ctx, `
		INSERT INTO articles(
		    stable_id, issue_id, publisher, issue_date, slug, title, description,
		    author, section, published_at, source_url, body
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare article insert: %w", err)
	}
	defer stmt.Close()
	for _, article := range issue.Articles {
		if _, err := stmt.ExecContext(ctx,
			article.StableID, issueID, issue.Publisher, issue.IssueDate, article.Slug,
			article.Title, article.Description, article.Author, article.Section,
			article.PublishedAt, article.SourceURL, article.Body,
		); err != nil {
			return fmt.Errorf("insert article %s: %w", article.StableID, err)
		}
	}

	if _, err := tx.ExecContext(ctx, `
		DELETE FROM issues
		WHERE publisher = ? AND id IN (
		    SELECT id FROM issues
		    WHERE publisher = ?
		    ORDER BY issue_date DESC, id DESC
		    LIMIT -1 OFFSET ?
		)`, issue.Publisher, issue.Publisher, keep); err != nil {
		return fmt.Errorf("apply retention: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit ingest: %w", err)
	}
	return nil
}

// Search performs trigram FTS and falls back to LIKE for queries shorter than three runes.
func (d *DB) Search(ctx context.Context, query, publisher string, limit int) ([]domain.SearchHit, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, errors.New("search query is empty")
	}
	if limit < 1 {
		limit = 20
	}
	if len([]rune(query)) < 3 {
		return d.searchLike(ctx, query, publisher, limit)
	}

	sqlText := `
		SELECT a.id, a.stable_id, a.publisher, a.issue_date, a.section, a.title,
		       snippet(articles_fts, -1, '[', ']', '…', 24)
		FROM articles_fts
		JOIN articles a ON a.id = articles_fts.rowid
		WHERE articles_fts MATCH ?`
	args := []any{quoteFTS(query)}
	if publisher != "" {
		sqlText += ` AND a.publisher = ?`
		args = append(args, publisher)
	}
	sqlText += ` ORDER BY articles_fts.rank LIMIT ?`
	args = append(args, limit)
	return scanHits(d.db.QueryContext(ctx, sqlText, args...))
}

func (d *DB) searchLike(ctx context.Context, query, publisher string, limit int) ([]domain.SearchHit, error) {
	pattern := "%" + query + "%"
	sqlText := `
		SELECT id, stable_id, publisher, issue_date, section, title,
		       substr(CASE WHEN summary_zh LIKE ? THEN summary_zh ELSE body END, 1, 240)
		FROM articles
		WHERE (title LIKE ? OR description LIKE ? OR body LIKE ? OR summary_zh LIKE ?)`
	args := []any{pattern, pattern, pattern, pattern, pattern}
	if publisher != "" {
		sqlText += ` AND publisher = ?`
		args = append(args, publisher)
	}
	sqlText += ` ORDER BY issue_date DESC, id LIMIT ?`
	args = append(args, limit)
	return scanHits(d.db.QueryContext(ctx, sqlText, args...))
}

func scanHits(rows *sql.Rows, err error) ([]domain.SearchHit, error) {
	if err != nil {
		return nil, fmt.Errorf("search articles: %w", err)
	}
	defer rows.Close()
	var hits []domain.SearchHit
	for rows.Next() {
		var hit domain.SearchHit
		if err := rows.Scan(&hit.ID, &hit.StableID, &hit.Publisher, &hit.IssueDate, &hit.Section, &hit.Title, &hit.Preview); err != nil {
			return nil, fmt.Errorf("scan search result: %w", err)
		}
		hits = append(hits, hit)
	}
	return hits, rows.Err()
}

func quoteFTS(query string) string {
	return `"` + strings.ReplaceAll(query, `"`, `""`) + `"`
}

// Read loads one article by stable ID or numeric row ID.
func (d *DB) Read(ctx context.Context, identifier string) (domain.StoredArticle, error) {
	where := "stable_id = ?"
	arg := any(identifier)
	if id, err := strconv.ParseInt(identifier, 10, 64); err == nil {
		where = "id = ?"
		arg = id
	}
	var article domain.StoredArticle
	err := d.db.QueryRowContext(ctx, `
		SELECT id, stable_id, publisher, issue_date, slug, title, description, author,
		       section, published_at, source_url, body, summary_zh, summary_error
		FROM articles WHERE `+where, arg,
	).Scan(
		&article.ID, &article.StableID, &article.Publisher, &article.IssueDate,
		&article.Slug, &article.Title, &article.Description, &article.Author,
		&article.Section, &article.PublishedAt, &article.SourceURL, &article.Body,
		&article.SummaryZH, &article.SummaryError,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.StoredArticle{}, fmt.Errorf("article %q not found", identifier)
	}
	if err != nil {
		return domain.StoredArticle{}, fmt.Errorf("read article: %w", err)
	}
	return article, nil
}

// PendingSummaries returns articles whose Chinese summary is empty.
func (d *DB) PendingSummaries(ctx context.Context, limit int) ([]domain.StoredArticle, error) {
	sqlText := `
		SELECT id, stable_id, publisher, issue_date, slug, title, description, author,
		       section, published_at, source_url, body, summary_zh, summary_error
		FROM articles WHERE summary_zh = '' ORDER BY issue_date DESC, id`
	var args []any
	if limit > 0 {
		sqlText += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := d.db.QueryContext(ctx, sqlText, args...)
	if err != nil {
		return nil, fmt.Errorf("list pending summaries: %w", err)
	}
	defer rows.Close()
	var articles []domain.StoredArticle
	for rows.Next() {
		var article domain.StoredArticle
		if err := rows.Scan(
			&article.ID, &article.StableID, &article.Publisher, &article.IssueDate,
			&article.Slug, &article.Title, &article.Description, &article.Author,
			&article.Section, &article.PublishedAt, &article.SourceURL, &article.Body,
			&article.SummaryZH, &article.SummaryError,
		); err != nil {
			return nil, fmt.Errorf("scan pending summary: %w", err)
		}
		articles = append(articles, article)
	}
	return articles, rows.Err()
}

// SaveSummary updates one summary; the FTS update trigger keeps search in sync.
func (d *DB) SaveSummary(ctx context.Context, id int64, summary, provider string) error {
	_, err := d.db.ExecContext(ctx, `
		UPDATE articles
		SET summary_zh = ?, summary_provider = ?, summary_error = '', summarized_at = ?
		WHERE id = ?`, summary, provider, time.Now().Format(time.RFC3339), id)
	if err != nil {
		return fmt.Errorf("save summary for article %d: %w", id, err)
	}
	return nil
}

// SaveSummaryError records a failed attempt without removing the article from the retry queue.
func (d *DB) SaveSummaryError(ctx context.Context, id int64, message string) error {
	_, err := d.db.ExecContext(ctx,
		`UPDATE articles SET summary_error = ? WHERE id = ?`, message, id)
	if err != nil {
		return fmt.Errorf("save summary error for article %d: %w", id, err)
	}
	return nil
}
