package parser

import (
	"strings"
	"testing"
)

func TestParseEconomistUsesBodyTitleAfterTOC(t *testing.T) {
	text := strings.TrimSpace(`
Leaders
The world cup paradox

Leaders | Our cover

The World Cup paradox

June 11th 2026

The article body is long enough to be unmistakably real content and not a table of contents entry.

This article was downloaded by zlibrary from https://www.economist.com//leaders/2026/06/10/the-world-cup-paradox
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

This article was downloaded by calibre from https://www.wired.com/story/first-title/

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
