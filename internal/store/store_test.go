package store

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"magazine2db/internal/domain"
)

func TestInsertRetentionSearchAndRead(t *testing.T) {
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
	hits, err := db.Search(ctx, "interest rates", "economist", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(hits) != 4 {
		t.Fatalf("got %d retained search hits, want 4", len(hits))
	}

	if err := db.SaveSummary(ctx, article.ID, "央行关注通胀与利率。", "primary"); err != nil {
		t.Fatal(err)
	}
	zhHits, err := db.Search(ctx, "央行", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(zhHits) != 1 || zhHits[0].ID != article.ID {
		t.Fatalf("Chinese summary was not searchable: %+v", zhHits)
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
