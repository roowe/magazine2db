package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"time"

	_ "modernc.org/sqlite"

	"magazine2db/internal/domain"
)

const schema = `
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
    body_xhtml       TEXT NOT NULL DEFAULT '',
    source_href      TEXT NOT NULL DEFAULT '',
    UNIQUE (issue_id, slug)
);

CREATE INDEX IF NOT EXISTS idx_articles_issue ON articles(issue_id);
CREATE INDEX IF NOT EXISTS idx_articles_publisher_date ON articles(publisher, issue_date DESC);

`

// DB owns the shared magazine SQLite database.
type DB struct {
	db *sql.DB
}

// Open initializes the shared SQLite database.
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
	if _, err := db.Exec(`PRAGMA journal_mode = WAL; PRAGMA foreign_keys = ON; PRAGMA busy_timeout = 5000;`); err != nil {
		db.Close()
		return nil, fmt.Errorf("configure database: %w", err)
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize schema: %w", err)
	}
	return &DB{db: db}, nil
}

// migrate upgrades the article schema and removes obsolete search indexes in one transaction.
func migrate(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version > 3 {
		return fmt.Errorf("unsupported database version %d", version)
	}
	if version < 3 {
		if _, err := tx.Exec(`DROP TRIGGER IF EXISTS articles_ai;
DROP TRIGGER IF EXISTS articles_ad;
DROP TRIGGER IF EXISTS articles_au;
DROP TABLE IF EXISTS articles_fts;
`); err != nil {
			return fmt.Errorf("remove obsolete search index: %w", err)
		}
	}
	var legacy bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('articles') WHERE name = 'summary_zh')`).Scan(&legacy); err != nil {
		return err
	}
	if legacy {
		if _, err := tx.Exec(`
ALTER TABLE articles DROP COLUMN summary_zh;
ALTER TABLE articles DROP COLUMN summary_provider;
ALTER TABLE articles DROP COLUMN summary_error;
ALTER TABLE articles DROP COLUMN summarized_at;
`); err != nil {
			return fmt.Errorf("remove legacy summary schema: %w", err)
		}
	}
	if _, err := tx.Exec(schema); err != nil {
		return err
	}
	var hasXHTML bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('articles') WHERE name = 'body_xhtml')`).Scan(&hasXHTML); err != nil {
		return err
	}
	if !hasXHTML {
		if _, err := tx.Exec(`ALTER TABLE articles ADD COLUMN body_xhtml TEXT NOT NULL DEFAULT ''; ALTER TABLE articles ADD COLUMN source_href TEXT NOT NULL DEFAULT '';`); err != nil {
			return fmt.Errorf("add original XHTML columns: %w", err)
		}
	}
	if _, err := tx.Exec("PRAGMA user_version = 3"); err != nil {
		return err
	}
	return tx.Commit()
}

// Close closes the database.
func (d *DB) Close() error {
	return d.db.Close()
}

// HasIssue reports whether publisher+issue_date is already present.
func (d *DB) HasIssue(ctx context.Context, publisher, issueDate string) (bool, error) {
	const query = `SELECT 1 FROM issues WHERE publisher = ? AND issue_date = ? LIMIT 1`
	var found int
	err := d.db.QueryRowContext(ctx, query, publisher, issueDate).Scan(&found)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// ListIssues returns imported issues, newest first.
func (d *DB) ListIssues(ctx context.Context) ([]domain.IssueInfo, error) {
	const query = `SELECT i.id, i.publisher, i.issue_date, COUNT(a.id), i.imported_at
FROM issues i
LEFT JOIN articles a ON a.issue_id = i.id
GROUP BY i.id, i.publisher, i.issue_date, i.imported_at
ORDER BY i.issue_date DESC, i.publisher`
	rows, err := d.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list issues: %w", err)
	}
	defer rows.Close()

	issues := make([]domain.IssueInfo, 0)
	for rows.Next() {
		var issue domain.IssueInfo
		if err := rows.Scan(&issue.ID, &issue.Publisher, &issue.IssueDate, &issue.ArticleCount, &issue.ImportedAt); err != nil {
			return nil, fmt.Errorf("scan issue: %w", err)
		}
		issues = append(issues, issue)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate issues: %w", err)
	}
	return issues, nil
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

	result, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO issues
(publisher, issue_date, source_path, imported_at) VALUES (?, ?, ?, ?)`,
		issue.Publisher, issue.IssueDate, issue.SourcePath, time.Now().Format(time.RFC3339))
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

	for _, article := range issue.Articles {
		if _, err := tx.ExecContext(ctx, `INSERT INTO articles
(stable_id, issue_id, publisher, issue_date, slug, title, description, author,
 section, published_at, source_url, body, body_xhtml, source_href)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			article.StableID, issueID, issue.Publisher, issue.IssueDate, article.Slug,
			article.Title, article.Description, article.Author, article.Section,
			article.PublishedAt, article.SourceURL, article.Body, article.BodyXHTML, article.SourceHref,
		); err != nil {
			return fmt.Errorf("insert article %s: %w", article.StableID, err)
		}
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM issues WHERE publisher = ? AND id NOT IN (
SELECT id FROM issues WHERE publisher = ? ORDER BY issue_date DESC, id DESC LIMIT ?
)`, issue.Publisher, issue.Publisher, keep); err != nil {
		return fmt.Errorf("apply retention: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit ingest: %w", err)
	}
	return nil
}

// Read loads one article by stable ID or numeric row ID.
func (d *DB) Read(ctx context.Context, identifier string) (domain.StoredArticle, error) {
	condition := "stable_id = ?"
	var value any = identifier
	if id, err := strconv.ParseInt(identifier, 10, 64); err == nil {
		condition, value = "id = ?", id
	}
	query := `SELECT id, stable_id, publisher, issue_date, slug, title, description, author,
section, published_at, source_url, body, body_xhtml, source_href FROM articles WHERE ` + condition
	var article domain.StoredArticle
	err := d.db.QueryRowContext(ctx, query, value).Scan(
		&article.ID, &article.StableID, &article.Publisher, &article.IssueDate,
		&article.Slug, &article.Title, &article.Description, &article.Author,
		&article.Section, &article.PublishedAt, &article.SourceURL, &article.Body, &article.BodyXHTML, &article.SourceHref,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.StoredArticle{}, fmt.Errorf("article %q not found", identifier)
	}
	if err != nil {
		return domain.StoredArticle{}, fmt.Errorf("read article: %w", err)
	}
	return article, nil
}

// ListArticles returns titles and original-text excerpts, newest issues first.
func (d *DB) ListArticles(ctx context.Context, page, pageSize int, issueID int64) ([]domain.ArticleListItem, int, error) {
	filter := ""
	var args []any
	if issueID > 0 {
		filter = " WHERE issue_id = ?"
		args = append(args, issueID)
	}
	var total int
	if err := d.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM articles"+filter, args...).Scan(&total); err != nil {
		return nil, 0, fmt.Errorf("count articles: %w", err)
	}
	query := "SELECT id, title, substr(body, 1, 200) FROM articles" + filter + " ORDER BY issue_date DESC, id LIMIT ? OFFSET ?"
	args = append(args, pageSize, (page-1)*pageSize)
	rows, err := d.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("list articles: %w", err)
	}
	defer rows.Close()

	items := make([]domain.ArticleListItem, 0, pageSize)
	for rows.Next() {
		var item domain.ArticleListItem
		if err := rows.Scan(&item.ID, &item.Title, &item.Excerpt); err != nil {
			return nil, 0, fmt.Errorf("scan article list: %w", err)
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("iterate articles: %w", err)
	}
	return items, total, nil
}
