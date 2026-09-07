package store

import (
	"context"
	"path/filepath"
	"testing"

	"magazine2db/internal/domain"
)

func TestRefreshRetainsIdentityAndRollsBackMissingArticles(t *testing.T) {
	ctx := context.Background()
	db, err := Open(filepath.Join(t.TempDir(), "articles.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	issue := domain.Issue{Publisher: "wired", IssueDate: "2026-09-02", SourcePath: "/old", Articles: []domain.Article{
		{StableID: "wired:2026-09-02:old-id", Slug: "old-id", Title: "First", Body: "Old body", Author: "Known author"},
		{StableID: "wired:2026-09-02:second", Slug: "second", Title: "Second", Body: "Second body"},
	}}
	if err := db.InsertIssue(ctx, issue, 4); err != nil {
		t.Fatal(err)
	}
	before, err := db.Read(ctx, issue.Articles[0].StableID)
	if err != nil {
		t.Fatal(err)
	}
	issue.SourcePath = "/new"
	issue.Articles[0].StableID = "wired:2026-09-02:first"
	issue.Articles[0].Slug = "first"
	issue.Articles[0].Author = ""
	issue.Articles = append(issue.Articles, domain.Article{
		StableID: "wired:2026-09-02:third", Slug: "third", Title: "Third", Author: "New author",
	})
	for i := range issue.Articles {
		issue.Articles[i].Body = "Fresh evidence"
		issue.Articles[i].BodyXHTML = `<html><head><title>xhtmlonlytoken</title></head><body><p>Fresh evidence</p></body></html>`
		issue.Articles[i].SourceHref = issue.Articles[i].Slug + ".xhtml"
	}
	if err := db.RefreshIssue(ctx, issue); err != nil {
		t.Fatal(err)
	}
	after, err := db.Read(ctx, before.StableID)
	if err != nil {
		t.Fatal(err)
	}
	if after.ID != before.ID || after.Slug != before.Slug || after.Author != before.Author || after.Body != "Fresh evidence" || after.BodyXHTML != issue.Articles[0].BodyXHTML {
		t.Fatalf("refresh changed identity/metadata or lost XHTML: %+v", after)
	}
	added, err := db.Read(ctx, issue.Articles[2].StableID)
	if err != nil || added.ID == before.ID || added.Title != "Third" || added.Author != "New author" || added.Body != "Fresh evidence" || added.BodyXHTML != issue.Articles[2].BodyXHTML || added.SourceHref != "third.xhtml" {
		t.Fatalf("refresh did not insert new article: %+v, %v", added, err)
	}
	issue.Articles = issue.Articles[:1]
	issue.Articles[0].Body = "Must roll back"
	if err := db.RefreshIssue(ctx, issue); err == nil {
		t.Fatal("missing old article must abort refresh")
	}
	after, err = db.Read(ctx, before.StableID)
	if err != nil || after.Body != "Fresh evidence" {
		t.Fatalf("refresh did not roll back: %+v, %v", after, err)
	}
}
