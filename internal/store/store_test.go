package store

import (
	"context"
	"fmt"
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
	if err := db.InsertIssue(ctx, domain.Issue{Publisher: "wired", IssueDate: "2026-09-02"}, 4, false); err != nil {
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
		if err := db.InsertIssue(ctx, issue, 4, false); err != nil {
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
	if err := db.InsertIssue(ctx, issue, 4, false); err != nil {
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
	if err := db.InsertIssue(ctx, issue, 4, false); err != nil {
		t.Fatal(err)
	}
	issue.SourcePath = "/second"
	issue.Articles[0].Title = "Changed"
	if err := db.InsertIssue(ctx, issue, 4, false); err != nil {
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
