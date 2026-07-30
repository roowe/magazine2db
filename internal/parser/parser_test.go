package parser

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHasSource(t *testing.T) {
	dir := t.TempDir()
	if HasSource(dir) {
		t.Fatal("empty directory should have no source")
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if HasSource(dir) {
		t.Fatal("README-only directory should have no source")
	}
	if err := os.WriteFile(filepath.Join(dir, "issue.epub"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !HasSource(dir) {
		t.Fatal("directory with EPUB should have source")
	}
}

func TestInspectInputRejectsFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "economist_2026.06.27.txt")
	if err := os.WriteFile(path, []byte("fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := InspectInput(path); err == nil || !strings.Contains(err.Error(), "issue directory") {
		t.Fatalf("expected directory error, got %v", err)
	}
}

func TestParseEconomistUsesBodyTitleAfterTOC(t *testing.T) {
	text := strings.TrimSpace(`
Leaders
The world cup paradox

Leaders | Our cover

The World Cup paradox

June 11th 2026

The article body is long enough to be unmistakably real content and not a table of contents entry.

This article was downloaded by another-tool from https://www.economist.com//leaders/2026/06/10/the-world-cup-paradox
`)
	articles, err := parseEconomist(text, "2026-06-13")
	if err != nil {
		t.Fatal(err)
	}
	if len(articles) != 1 {
		t.Fatalf("got %d articles, want 1", len(articles))
	}
	article := articles[0]
	if article.Title != "The World Cup paradox" {
		t.Fatalf("title = %q", article.Title)
	}
	if article.StableID != "economist:2026-06-13:the-world-cup-paradox" {
		t.Fatalf("stable id = %q", article.StableID)
	}
	if !strings.Contains(article.Body, "article body") {
		t.Fatalf("body was truncated: %q", article.Body)
	}
}

func TestParseWiredReadsHeaderAndCutsFooter(t *testing.T) {
	text := strings.TrimSpace(`
Magazine Articles
First title

| Next | Section menu | Main menu |
* * *

Jane Writer

Business

May 18, 2026 6:00 AM

First title

The explanatory deck.

The first article body.

What Say You?

This footer must not be stored.

This article was downloaded by another-tool from https://www.wired.com/story/first-title/

| Next | Section menu | Main menu | Previous |
* * *

John Writer

Science

Apr 2, 2026 7:00 AM

Second title

Another deck.

The second article body.

Let us know what you think about this article. Submit a letter.

This article was downloaded by calibre from https://www.wired.com/story/second-title/
`)
	articles, err := parseWired(text, "2026-06-02")
	if err != nil {
		t.Fatal(err)
	}
	if len(articles) != 2 {
		t.Fatalf("got %d articles, want 2", len(articles))
	}
	first := articles[0]
	if first.Author != "Jane Writer" || first.Section != "Business" || first.Title != "First title" {
		t.Fatalf("bad first metadata: %+v", first)
	}
	if first.Description != "The explanatory deck." {
		t.Fatalf("description = %q", first.Description)
	}
	if strings.Contains(first.Body, "footer") {
		t.Fatalf("footer leaked into body: %q", first.Body)
	}
	if articles[1].StableID != "wired:2026-06-02:second-title" {
		t.Fatalf("stable id = %q", articles[1].StableID)
	}
}

func TestParseWiredFallsBackWhenHeaderIsMissing(t *testing.T) {
	text := strings.TrimSpace(`
Magazine Articles
The Baby Died. Whose Fault Is It?

Author
The Big Story
May 18, 2026 6:00 AM
Normal article
Normal deck
Normal body
This article was downloaded by calibre from https://www.wired.com/story/normal-article/

| Section menu | Main menu |
* * *
The headerless article body.
Have your say
This footer must not remain.
This article was downloaded by calibre from https://www.wired.com/story/the-baby-died-whose-fault-is-it-surrogate-pregnancy/
`)
	articles, err := parseWired(text, "2026-06-02")
	if err != nil {
		t.Fatal(err)
	}
	if len(articles) != 2 {
		t.Fatalf("got %d articles, want 2", len(articles))
	}
	article := articles[1]
	if article.Title != "The Baby Died. Whose Fault Is It?" {
		t.Fatalf("TOC fallback title = %q", article.Title)
	}
	if strings.Contains(article.Body, "footer") {
		t.Fatalf("footer leaked into body: %q", article.Body)
	}
}

func TestNormalizeTitleHandlesSmartPunctuation(t *testing.T) {
	if normalizeTitle("Donald Trump’s least-bad option") != normalizeTitle("donald-trump-s-least-bad-option") {
		t.Fatal("smart punctuation should normalize like URL separators")
	}
}

func TestParseEconomistJournalLayout(t *testing.T) {
	text := strings.TrimSpace(`
July 25th 2026

The world this week

Leaders

优质App推荐

The world this week

Politics

Business

The world this week

Politics

* * *

Jul 23rd 2026

First politics item with enough text to look like a real paragraph of news.

Second politics item.

Leaders

When a president stops pretending that voters count, disaster beckons

Leaders | In praise of hypocrisy

When a president stops pretending that voters count, disaster beckons

Tiny Nicaragua offers a cautionary tale for democrats everywhere

* * *

Jul 23rd 2026

Hypocrisy is the homage that vice pays to virtue, wrote a French moralist in a long opening paragraph.

A second paragraph of the leader body.

Britain | Grey expectations

Tours of British post-war housing are a quiet hit

Perambulations of the anti-Instagram kind

Jul 24th 2026

This header is not followed by a separator, yet the body that follows is still long and real.

Economic & financial indicators | Indicators

Economic data, commodities and markets

Jul 23rd 2026

Obituary

Wally Funk was told women couldn’t be astronauts

Obituary | Per ardua ad astra

Wally Funk was told women couldn’t be astronauts

The aviator who refused to agree died on July 8th, aged 87

* * *

Jul 23rd 2026

When she was told she had to do something, Wally Funk often refused, and this obituary body is long.
`)
	articles, err := parseEconomist(text, "2026-07-25")
	if err != nil {
		t.Fatal(err)
	}
	if len(articles) != 4 {
		t.Fatalf("got %d articles, want 4 (indicators without body must be skipped)", len(articles))
	}

	politics := articles[0]
	if politics.Section != "The World This Week" || politics.Title != "Politics" {
		t.Fatalf("bad TWTW metadata: %+v", politics)
	}
	if politics.PublishedAt != "2026-07-23" {
		t.Fatalf("published_at = %q", politics.PublishedAt)
	}
	if !strings.Contains(politics.Body, "Second politics item") {
		t.Fatalf("TWTW body truncated: %q", politics.Body)
	}
	if strings.Contains(politics.Body, "disaster beckons") {
		t.Fatalf("section navigation leaked into TWTW body: %q", politics.Body)
	}

	leader := articles[1]
	if leader.Section != "Leaders" || leader.Description != "Tiny Nicaragua offers a cautionary tale for democrats everywhere" {
		t.Fatalf("bad leader metadata: %+v", leader)
	}
	if leader.StableID != "economist:2026-07-25:when-a-president-stops-pretending-that-voters-count-disaster-beckons" {
		t.Fatalf("stable id = %q", leader.StableID)
	}
	if !strings.Contains(leader.Body, "second paragraph") {
		t.Fatalf("leader body truncated: %q", leader.Body)
	}

	starless := articles[2]
	if starless.Title != "Tours of British post-war housing are a quiet hit" || starless.PublishedAt != "2026-07-24" {
		t.Fatalf("bad starless header metadata: %+v", starless)
	}

	obituary := articles[3]
	if obituary.Section != "Obituary" || !strings.Contains(obituary.Body, "often refused") {
		t.Fatalf("bad obituary: %+v", obituary)
	}
}

func TestParseEconomistJournalLayoutRejectsForeignText(t *testing.T) {
	text := "Just some prose without any Economist journal structure at all.\nNo markers here."
	if _, err := parseEconomist(text, "2026-07-25"); err == nil {
		t.Fatal("expected marker error for text without journal structure")
	}
}
