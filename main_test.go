package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"magazine2db/internal/config"
	"magazine2db/internal/domain"
	"magazine2db/internal/store"
)

func TestRemoveBlankLines(t *testing.T) {
	input := "first paragraph\n\n   \nsecond paragraph\nthird line"
	want := "first paragraph\nsecond paragraph\nthird line"
	if got := removeBlankLines(input); got != want {
		t.Fatalf("removeBlankLines() = %q, want %q", got, want)
	}
}

func TestRunRejectsInvalidLimits(t *testing.T) {
	tests := []struct {
		name string
		run  func() error
		want string
	}{
		{
			name: "search zero",
			run: func() error {
				return runSearch(context.Background(), config.Config{}, []string{"--limit", "0", "query"})
			},
			want: "limit must be positive",
		},
		{
			name: "summarize negative",
			run: func() error {
				cfg := config.Config{Summary: config.Summary{Concurrency: 1}}
				return runSummarize(context.Background(), cfg, []string{"--limit", "-1"})
			},
			want: "limit must not be negative",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.run(); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q error, got %v", test.want, err)
			}
		})
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	if err := run(context.Background(), []string{"unknown"}); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Fatalf("expected unknown command error, got %v", err)
	}
}

func TestRunSummarizeCommitsSuccessAndFailureIndependently(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("synthetic Codex executable uses POSIX shell")
	}
	root := t.TempDir()
	codexBin := filepath.Join(root, "fake-codex")
	script := `#!/bin/sh
set -eu
if [ "$1" = "--version" ]; then
  printf '%s\n' 'codex-cli fake-1.0'
  exit 0
fi
if [ "$1" = "debug" ] && [ "$2" = "models" ]; then
  printf '%s\n' '{"models":[{"slug":"gpt-5.6-luna","supported_reasoning_levels":[{"effort":"max"}]}]}'
  exit 0
fi
if [ "$1" = "exec" ]; then
  workspace=''
  output=''
  while [ "$#" -gt 0 ]; do
    if [ "$1" = "-C" ]; then
      shift
      workspace="$1"
    elif [ "$1" = "-o" ]; then
      shift
      output="$1"
    fi
    shift
  done
  if grep -q '模拟失败' "$workspace/article.txt"; then
    printf '%s\n' 'synthetic failure' >&2
    exit 17
  fi
  printf '%s\n' '{"type":"turn.completed"}'
  printf '%s' '{"summary":"合成文章说明测试成功。"}' > "$output"
  exit 0
fi
exit 9
`
	if err := os.WriteFile(codexBin, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	dbPath := filepath.Join(root, "magazines.db")
	database, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	issue := domain.Issue{
		Publisher: "wired", IssueDate: "2026-08-28", SourcePath: "synthetic",
		Articles: []domain.Article{
			{StableID: "wired:2026-08-28:ok", Slug: "ok", Title: "成功", Body: "这是一篇合成成功文章。"},
			{StableID: "wired:2026-08-28:failed", Slug: "failed", Title: "失败", Body: "这篇文章用于模拟失败。"},
		},
	}
	if err := database.InsertIssue(ctx, issue, 4); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{
		WorkDir: root, Database: dbPath, Retention: 4,
		Summary: config.Summary{CodexBin: codexBin, TimeoutSeconds: 1, Concurrency: 4},
	}
	err = runSummarize(ctx, cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "1 summary job(s) failed") {
		t.Fatalf("unexpected summarize result: %v", err)
	}

	database, err = store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	succeeded, err := database.Read(ctx, issue.Articles[0].StableID)
	if err != nil {
		t.Fatal(err)
	}
	failed, err := database.Read(ctx, issue.Articles[1].StableID)
	if err != nil {
		t.Fatal(err)
	}
	if succeeded.SummaryZH != "合成文章说明测试成功。" || succeeded.SummaryError != "" {
		t.Fatalf("unexpected success row: %+v", succeeded)
	}
	if failed.SummaryZH != "" || !strings.Contains(failed.SummaryError, "synthetic failure") {
		t.Fatalf("unexpected failure row: %+v", failed)
	}
	entries, err := os.ReadDir(filepath.Join(root, ".agent-runs", "summary"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("run artifacts = %d, want 2", len(entries))
	}
}
