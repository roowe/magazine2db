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
	if err := initialize(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize schema: %w", err)
	}
	return &DB{db: db}, nil
}

// initialize 只接受当前版本或空数据库；旧库需要从 EPUB 重建，不再自动迁移。
func initialize(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		return err
	}
	if version == 4 {
		return tx.Commit()
	}
	var populated bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%')`).Scan(&populated); err != nil {
		return err
	}
	if version != 0 || populated {
		return fmt.Errorf("unsupported database version %d; rebuild the database from EPUB", version)
	}
	if _, err := tx.Exec(schema); err != nil {
		return err
	}
	if _, err := tx.Exec("PRAGMA user_version = 4"); err != nil {
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

// InsertIssue 在同一事务中入库并清理超过保留期数的期刊。
// force 时先删除整期记录（文章由外键级联删除），再插入本次解析结果；失败则整体回滚。
func (d *DB) InsertIssue(ctx context.Context, issue domain.Issue, keep int, force bool) error {
	if keep < 1 {
		return errors.New("retention count must be positive")
	}
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin ingest transaction: %w", err)
	}
	defer tx.Rollback()

	if force {
		if _, err := tx.ExecContext(ctx, `DELETE FROM issues WHERE publisher = ? AND issue_date = ?`, issue.Publisher, issue.IssueDate); err != nil {
			return fmt.Errorf("delete issue for replacement: %w", err)
		}
	}

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
	query := "SELECT id, title, substr(body, 1, 1000) FROM articles" + filter + " ORDER BY issue_date DESC, id LIMIT ? OFFSET ?"
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
