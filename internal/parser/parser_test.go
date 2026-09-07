package parser

import (
	"path/filepath"
	"strings"
	"testing"
)

// 使用完整真实 EPUB 验证 ZIP → container → OPF → 目录 → 文章的解析流程。
func TestParseEPUBRealIssue(t *testing.T) {
	dir := filepath.Join("testdata", "economist")
	issue, err := parseEPUB(Input{
		Path: dir, Publisher: "economist", IssueDate: "2026-09-05",
	}, filepath.Join(dir, "issue.epub"))
	if err != nil {
		t.Fatal(err)
	}
	if issue.Publisher != "economist" || issue.IssueDate != "2026-09-05" || issue.SourcePath != dir || len(issue.Articles) != 79 {
		t.Fatalf("unexpected issue: publisher=%s date=%s path=%s articles=%d", issue.Publisher, issue.IssueDate, issue.SourcePath, len(issue.Articles))
	}
	if issue.Articles[0].Title != "Politics" {
		t.Fatalf("unexpected first article: %q", issue.Articles[0].Title)
	}
	for _, article := range issue.Articles {
		if article.StableID == "" || article.Body == "" || article.BodyXHTML == "" || article.SourceHref == "" {
			t.Fatalf("incomplete article: %q", article.Title)
		}
	}
	// 用已有原始 XHTML 样本交叉核对两篇文章，避免仅断言解析结果非空。
	for _, example := range articleExamples {
		if example.publisher != "economist" {
			continue
		}
		found := false
		for _, article := range issue.Articles {
			if article.SourceHref != example.href {
				continue
			}
			found = true
			if article.Title != example.title || article.Section != example.section || !strings.Contains(article.Body, example.excerpt) {
				t.Fatalf("incorrect article mapping: %q", article.Title)
			}
			if article.BodyXHTML != string(readExample(t, "economist", example.sample+".original.xhtml")) {
				t.Fatalf("original XHTML changed: %q", article.Title)
			}
			break
		}
		if !found {
			t.Fatalf("missing article: %q", example.title)
		}
	}
}
