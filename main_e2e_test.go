//go:build e2e

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"magazine2db/internal/domain"
)

func TestCLIEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	projectDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	runtimeDir := t.TempDir()

	binary := filepath.Join(runtimeDir, "magazine2db")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, ".")
	build.Dir = projectDir
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}

	// 从其他工作目录运行，验证默认数据库始终位于可执行文件旁。
	workingDir := t.TempDir()
	issuePath := filepath.Join(runtimeDir, "economist_2026.09.05")
	if err := os.Mkdir(issuePath, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile(filepath.Join(projectDir, "internal/parser/testdata/economist/issue.epub"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(issuePath, "issue.epub"), fixture, 0o600); err != nil {
		t.Fatal(err)
	}

	for _, args := range [][]string{
		{"ingest", issuePath},
		{"ingest", "--force", issuePath},
	} {
		output := runE2ECommand(t, ctx, workingDir, binary, args...)
		assertContains(t, output, "ingested: economist 2026-09-05, 79 articles")
	}
	if _, err := os.Stat(filepath.Join(runtimeDir, "magazines.db")); err != nil {
		t.Fatalf("database was not created next to the executable: %v", err)
	}

	jsonIssues := runE2ECommand(t, ctx, workingDir, binary, "issue", "--json")
	var issueResult struct {
		Count  int                `json:"count"`
		Issues []domain.IssueInfo `json:"issues"`
	}
	if err := json.Unmarshal([]byte(jsonIssues), &issueResult); err != nil {
		t.Fatalf("decode issue output: %v\n%s", err, jsonIssues)
	}
	if issueResult.Count != 1 || len(issueResult.Issues) != 1 || issueResult.Issues[0].ArticleCount != 79 {
		t.Fatalf("unexpected issue output: %+v", issueResult)
	}
	issueID := strconv.FormatInt(issueResult.Issues[0].ID, 10)

	listOutput := runE2ECommand(t, ctx, workingDir, binary, "list", "--page", "1", "--page-size", "1", "--issue", issueID, "--json")
	var listResult struct {
		Page     int                      `json:"page"`
		PageSize int                      `json:"page_size"`
		Total    int                      `json:"total"`
		Items    []domain.ArticleListItem `json:"items"`
	}
	if err := json.Unmarshal([]byte(listOutput), &listResult); err != nil {
		t.Fatalf("decode list output: %v\n%s", err, listOutput)
	}
	if listResult.Page != 1 || listResult.PageSize != 1 || listResult.Total != 79 || len(listResult.Items) != 1 {
		t.Fatalf("unexpected list pagination: %+v", listResult)
	}
	if listResult.Items[0].Title != "Politics" || listResult.Items[0].Excerpt == "" {
		t.Fatalf("unexpected list item: %+v", listResult.Items[0])
	}

	// 按列表返回的 ID 读取正文，验证下游实际使用的数据访问链路。
	numericID := strconv.FormatInt(listResult.Items[0].ID, 10)
	readOutput := runE2ECommand(t, ctx, workingDir, binary, "read", "--json", numericID)
	var article domain.StoredArticle
	if err := json.Unmarshal([]byte(readOutput), &article); err != nil {
		t.Fatalf("decode read output: %v\n%s", err, readOutput)
	}
	if article.ID != listResult.Items[0].ID || article.Title != "Politics" || article.Publisher != "economist" || article.IssueDate != "2026-09-05" || article.StableID == "" {
		t.Fatalf("unexpected article: %+v", article)
	}
	assertContains(t, article.Body, "Fighting broke out between America and Iran")
	if strings.Contains(readOutput, "body_xhtml") {
		t.Fatal("default read must not include XHTML")
	}

	// 显式 --db 应选中独立空库，而不是默认库。
	otherDB := filepath.Join(workingDir, "other.db")
	other := runE2ECommand(t, ctx, workingDir, binary, "issue", "--db", otherDB, "--json")
	if err := json.Unmarshal([]byte(other), &issueResult); err != nil || issueResult.Count != 0 || len(issueResult.Issues) != 0 {
		t.Fatalf("database override failed: %s, %v", other, err)
	}
}

func runE2ECommand(t *testing.T, ctx context.Context, dir, binary string, args ...string) string {
	t.Helper()
	command := exec.CommandContext(ctx, binary, args...)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", binary, strings.Join(args, " "), err, output)
	}
	return string(output)
}

func assertContains(t *testing.T, value, expected string) {
	t.Helper()
	if !strings.Contains(value, expected) {
		t.Fatalf("output does not contain %q:\n%s", expected, value)
	}
}
