package store

import (
	"context"
	"path/filepath"
	"testing"

	"magazine2db/internal/domain"
)

func TestForceReplacesIssueAndRollsBackOnFailure(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "articles.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	issue := domain.Issue{Publisher: "wired", IssueDate: "2026-09-02", SourcePath: "/old", Articles: []domain.Article{
		{StableID: "wired:2026-09-02:first", Slug: "first", Title: "First", Body: "Old body", Author: "Old author"},
		{StableID: "wired:2026-09-02:second", Slug: "second", Title: "Second", Body: "Second body"},
	}}
	if err := db.InsertIssue(ctx, issue, 4, false); err != nil {
		t.Fatal(err)
	}
	issue.SourcePath = "/new"
	issue.Articles = []domain.Article{{
		StableID: "wired:2026-09-02:replacement", Slug: "replacement", Title: "Replacement",
		Body: "# Fresh evidence", BodyXHTML: "<h1>Fresh evidence</h1>", SourceHref: "replacement.xhtml", Author: "New author",
	}}
	if err := db.InsertIssue(ctx, issue, 4, true); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"wired:2026-09-02:first", "wired:2026-09-02:second"} {
		if _, err := db.Read(ctx, id); err == nil {
			t.Fatalf("old article remains: %s", id)
		}
	}
	article, err := db.Read(ctx, issue.Articles[0].StableID)
	if err != nil || article.Title != "Replacement" || article.Author != "New author" || article.Body != "# Fresh evidence" || article.BodyXHTML != "<h1>Fresh evidence</h1>" || article.SourceHref != "replacement.xhtml" {
		t.Fatalf("replacement article: %+v, %v", article, err)
	}
	after, err := db.ListIssues(ctx)
	if err != nil || len(after) != 1 || after[0].ArticleCount != 1 {
		t.Fatalf("unexpected refreshed issue: %+v, %v", after, err)
	}
	// 第二篇重复违反唯一约束，验证先删后写仍能整体回滚。
	issue.SourcePath = "/failed"
	issue.Articles[0].Body = "Must roll back"
	issue.Articles = append(issue.Articles, issue.Articles[0])
	if err := db.InsertIssue(ctx, issue, 4, true); err == nil {
		t.Fatal("duplicate article should fail")
	}
	restored, err := db.Read(ctx, article.StableID)
	if err != nil || restored != article {
		t.Fatalf("article was not restored: %+v, %v", restored, err)
	}
	var sourcePath string
	if err := db.db.QueryRow(`SELECT source_path FROM issues WHERE id = ?`, after[0].ID).Scan(&sourcePath); err != nil || sourcePath != "/new" {
		t.Fatalf("source path was not restored: %q, %v", sourcePath, err)
	}
}
