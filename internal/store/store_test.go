package store

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"magazine2db/internal/domain"
)

func TestHasIssue(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "magazines.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.InsertIssue(ctx, domain.Issue{Publisher: "wired", IssueDate: "2026-09-02"}, 4); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		publisher, date string
		want            bool
	}{
		{"wired", "2026-09-02", true},
		{"economist", "2026-09-02", false},
		{"wired", "2026-09-03", false},
	} {
		t.Run(tc.publisher+"/"+tc.date, func(t *testing.T) {
			got, err := db.HasIssue(ctx, tc.publisher, tc.date)
			if err != nil || got != tc.want {
				t.Fatalf("HasIssue = %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestLegacyDatabaseMigratesOriginalContent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	legacySchema, err := os.ReadFile("testdata/legacy.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := legacy.Exec(string(legacySchema)); err != nil {
		t.Fatal(err)
	}
	db := &DB{db: legacy}
	ctx := context.Background()
	issue := domain.Issue{
		Publisher: "wired", IssueDate: "2026-09-01", SourcePath: "/synthetic",
		Articles: []domain.Article{{StableID: "wired:2026-09-01:test", Slug: "test", Title: "Original title", Body: "Original evidence about networks."}},
	}
	if _, err := legacy.Exec(`INSERT INTO issues(id, publisher, issue_date, source_path, imported_at) VALUES (1, 'wired', '2026-09-01', '/synthetic', '2026-09-01');
INSERT INTO articles(id, stable_id, issue_id, publisher, issue_date, slug, title, source_url, body) VALUES (1, 'wired:2026-09-01:test', 1, 'wired', '2026-09-01', 'test', 'Original title', '', 'Original evidence about networks.');`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec("UPDATE articles SET summary_zh = ?", "历史摘要含有额外推断"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	items, total, err := db.ListArticles(ctx, 1, 20, 0)
	if err != nil || total != 1 || len(items) != 1 || items[0].Excerpt != issue.Articles[0].Body {
		t.Fatalf("unexpected original excerpt: %+v, total=%d, %v", items, total, err)
	}
	article, err := db.Read(ctx, issue.Articles[0].StableID)
	if err != nil || article.Body != issue.Articles[0].Body {
		t.Fatalf("legacy article unreadable: %+v, %v", article, err)
	}
	if article.ID != 1 || article.StableID != issue.Articles[0].StableID {
		t.Fatalf("article identity changed: %+v", article)
	}
	var count int
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('articles') WHERE name IN ('summary_zh', 'summary_provider', 'summary_error', 'summarized_at')`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("summary columns remain: %d, %v", count, err)
	}
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name LIKE 'articles_fts%' OR name IN ('articles_ai', 'articles_ad', 'articles_au')`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("obsolete search objects remain: %d, %v", count, err)
	}
	if err := migrate(db.db); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
	if _, err := db.db.Exec("UPDATE articles SET body = ? WHERE id = ?", "Changed network evidence.", article.ID); err != nil {
		t.Fatal(err)
	}
	article, err = db.Read(ctx, article.StableID)
	if err != nil || article.Body != "Changed network evidence." {
		t.Fatalf("migrated article update failed: %+v, %v", article, err)
	}

}

func TestInsertRetentionAndRead(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "magazines.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	for day := 1; day <= 5; day++ {
		date := fmt.Sprintf("2026-06-%02d", day)
		issue := domain.Issue{
			Publisher: "economist", IssueDate: date, SourcePath: "/fixture/" + date,
			Articles: []domain.Article{{
				StableID: "economist:" + date + ":rates", Slug: "rates",
				Title: "Interest rates", Section: "Finance", SourceURL: "https://example.com/" + date,
				Body: "Central banks discussed interest rates and inflation.",
			}},
		}
		if err := db.InsertIssue(ctx, issue, 4); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := db.Read(ctx, "economist:2026-06-01:rates"); err == nil {
		t.Fatal("oldest issue should have been removed")
	}
	article, err := db.Read(ctx, "economist:2026-06-05:rates")
	if err != nil {
		t.Fatal(err)
	}
	byNumber, err := db.Read(ctx, fmt.Sprint(article.ID))
	if err != nil || byNumber.StableID != article.StableID {
		t.Fatalf("numeric read failed: %+v, %v", byNumber, err)
	}
	issues, err := db.ListIssues(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 4 || issues[0].ID == 0 || issues[0].IssueDate != "2026-06-05" || issues[0].ArticleCount != 1 || issues[0].ImportedAt == "" {
		t.Fatalf("unexpected issue list: %+v", issues)
	}

}

func TestListArticlesPaginatesOriginalExcerpts(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "magazines.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	longBody := strings.Repeat("界", 220)
	issue := domain.Issue{
		Publisher: "wired", IssueDate: "2026-06-02", SourcePath: "/fixture",
		Articles: []domain.Article{
			{StableID: "wired:2026-06-02:first", Slug: "first", Title: "First", SourceURL: "https://example.com/first", Body: "first body"},
			{StableID: "wired:2026-06-02:second", Slug: "second", Title: "Second", SourceURL: "https://example.com/second", Body: longBody},
			{StableID: "wired:2026-06-02:third", Slug: "third", Title: "Third", SourceURL: "https://example.com/third", Body: "third body"},
		},
	}
	if err := db.InsertIssue(ctx, issue, 4); err != nil {
		t.Fatal(err)
	}

	issues, err := db.ListIssues(ctx)
	if err != nil || len(issues) != 1 {
		t.Fatalf("issues = %+v, err=%v", issues, err)
	}
	items, total, err := db.ListArticles(ctx, 1, 2, issues[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if total != 3 || len(items) != 2 {
		t.Fatalf("total=%d items=%d, want 3 and 2", total, len(items))
	}
	if items[0].Excerpt != "first body" {
		t.Fatalf("unexpected first excerpt: %q", items[0].Excerpt)
	}
	if got := len([]rune(items[1].Excerpt)); got != 200 {
		t.Fatalf("fallback body length = %d, want 200", got)
	}
	secondPage, _, err := db.ListArticles(ctx, 2, 2, issues[0].ID)
	if err != nil || len(secondPage) != 1 {
		t.Fatalf("second page = %+v, err=%v", secondPage, err)
	}
	empty, emptyTotal, err := db.ListArticles(ctx, 1, 2, issues[0].ID+1000)
	if err != nil || emptyTotal != 0 || len(empty) != 0 {
		t.Fatalf("unknown issue result = %+v total=%d err=%v", empty, emptyTotal, err)
	}
}

func TestDuplicateIssueIsIgnored(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "magazines.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	issue := domain.Issue{
		Publisher: "wired", IssueDate: "2026-06-02", SourcePath: "/first",
		Articles: []domain.Article{{
			StableID: "wired:2026-06-02:first", Slug: "first", Title: "First",
			SourceURL: "https://example.com/first", Body: "first body",
		}},
	}
	if err := db.InsertIssue(ctx, issue, 4); err != nil {
		t.Fatal(err)
	}
	issue.SourcePath = "/second"
	issue.Articles[0].Title = "Changed"
	if err := db.InsertIssue(ctx, issue, 4); err != nil {
		t.Fatal(err)
	}
	article, err := db.Read(ctx, issue.Articles[0].StableID)
	if err != nil {
		t.Fatal(err)
	}
	if article.Title != "First" {
		t.Fatalf("duplicate changed stored article: %q", article.Title)
	}
}

func TestVersionTwoRemovesSearchIndex(t *testing.T) {
	path := filepath.Join(t.TempDir(), "v2.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer legacy.Close()
	if _, err := legacy.Exec(schema + `
CREATE VIRTUAL TABLE IF NOT EXISTS articles_fts
USING fts5(
    title,
    description,
    body,
    content='articles',
    content_rowid='id',
    tokenize='trigram'
);

CREATE TRIGGER IF NOT EXISTS articles_ai
AFTER INSERT ON articles BEGIN
    INSERT INTO articles_fts(rowid, title, description, body)
    VALUES (new.id, new.title, new.description, new.body);
END;

CREATE TRIGGER IF NOT EXISTS articles_ad
AFTER DELETE ON articles BEGIN
    INSERT INTO articles_fts(articles_fts, rowid, title, description, body)
    VALUES ('delete', old.id, old.title, old.description, old.body);
END;

CREATE TRIGGER IF NOT EXISTS articles_au
AFTER UPDATE ON articles BEGIN
    INSERT INTO articles_fts(articles_fts, rowid, title, description, body)
    VALUES ('delete', old.id, old.title, old.description, old.body);
    INSERT INTO articles_fts(rowid, title, description, body)
    VALUES (new.id, new.title, new.description, new.body);
END;
PRAGMA user_version = 2;
INSERT INTO issues(id, publisher, issue_date, source_path, imported_at) VALUES (1, 'wired', '2026-09-02', '/fixture', '2026-09-02');
INSERT INTO articles(id, stable_id, issue_id, publisher, issue_date, slug, title, source_url, body, body_xhtml, source_href)
VALUES (1, 'wired:2026-09-02:test', 1, 'wired', '2026-09-02', 'test', 'Title', '', 'Original body', '<p>Original body</p>', 'test.xhtml');
`); err != nil {
		t.Fatal(err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	article, err := db.Read(context.Background(), "1")
	if err != nil || article.StableID != "wired:2026-09-02:test" || article.Body != "Original body" || article.BodyXHTML != "<p>Original body</p>" || article.SourceHref != "test.xhtml" {
		t.Fatalf("migration changed article: %+v, %v", article, err)
	}
	var count, version int
	if err := db.db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE name LIKE 'articles_fts%' OR name IN ('articles_ai', 'articles_ad', 'articles_au')`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("obsolete search objects remain: %d, %v", count, err)
	}
	if err := db.db.QueryRow("PRAGMA user_version").Scan(&version); err != nil || version != 3 {
		t.Fatalf("version=%d err=%v", version, err)
	}
	if err := migrate(db.db); err != nil {
		t.Fatalf("repeat migration: %v", err)
	}
}
