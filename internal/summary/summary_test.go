package summary

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"magazine2db/internal/domain"
)

const availableModels = "{\"models\":[{\"slug\":\"gpt-5.6-luna\",\"supported_reasoning_levels\":[{\"effort\":\"max\"}]}]}"

func TestSummarizeWritesAuditableArtifacts(t *testing.T) {
	service := newTestService(t, finalBody("{\"summary\":\"研究展示了量子设备可共享城域光纤，但大规模应用仍需改进存储和纠错。\"}"), time.Second)
	article := domain.StoredArticle{
		ID: 7, StableID: "economist:2026-06-27:quantum-network",
		Publisher: "economist", IssueDate: "2026-06-27", Title: "Quantum network",
		Body: "原文秘密：Researchers connected quantum devices over metropolitan fibre.",
	}

	output, err := service.Summarize(context.Background(), article)
	if err != nil {
		t.Fatal(err)
	}
	if output.Provider != ProviderLabel || !strings.Contains(output.Text, "量子设备") {
		t.Fatalf("unexpected output: %+v", output)
	}
	for _, name := range []string{
		"metadata.json", "article.txt", "prompt.md", "output-schema.json",
		"command.json", "stdout.jsonl", "stderr.log", "final.json", "run.json",
	} {
		if _, err := os.Stat(filepath.Join(output.RunDir, name)); err != nil {
			t.Fatalf("missing artifact %s: %v", name, err)
		}
	}
	command := readFile(t, filepath.Join(output.RunDir, "command.json"))
	for _, expected := range []string{"--ephemeral", "read-only", Model, "model_reasoning_effort=\\\"max\\\"", "--output-schema", "<PROMPT>"} {
		if !strings.Contains(command, expected) {
			t.Fatalf("command manifest missing %q:\n%s", expected, command)
		}
	}
	if strings.Contains(command, "原文秘密") || strings.Contains(command, agentPrompt) {
		t.Fatalf("command manifest leaked prompt or article:\n%s", command)
	}
	var record runRecord
	if err := json.Unmarshal([]byte(readFile(t, filepath.Join(output.RunDir, "run.json"))), &record); err != nil {
		t.Fatal(err)
	}
	if record.Status != "succeeded" || record.Model != Model || record.Reasoning != Reasoning ||
		record.RunnerVersion != "codex-cli fake-1.0" || record.StartedAt <= 0 {
		t.Fatalf("unexpected run record: %+v", record)
	}
}

func TestSummarizeRecordsNonzeroExit(t *testing.T) {
	service := newTestService(t, "printf '%s\\n' 'synthetic failure' >&2\nexit 17", time.Second)

	_, err := service.Summarize(context.Background(), testArticle("body"))
	if err == nil || !strings.Contains(err.Error(), "synthetic failure") || !strings.Contains(err.Error(), "artifacts:") {
		t.Fatalf("unexpected error: %v", err)
	}
	runDir := artifactPath(err.Error())
	var record runRecord
	if decodeErr := json.Unmarshal([]byte(readFile(t, filepath.Join(runDir, "run.json"))), &record); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if record.Status != "failed" || record.ExitCode == nil || *record.ExitCode != 17 {
		t.Fatalf("unexpected failed run record: %+v", record)
	}
}

func TestSummarizeKillsTimedOutProcess(t *testing.T) {
	service := newTestService(t, "exec sleep 5", 30*time.Millisecond)

	_, err := service.Summarize(context.Background(), testArticle("body"))
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewRejectsUnavailableModelOrReasoning(t *testing.T) {
	cases := []struct {
		name     string
		models   string
		expected string
	}{
		{name: "model", models: "{\"models\":[]}", expected: "Codex model gpt-5.6-luna is unavailable"},
		{
			name:     "reasoning",
			models:   "{\"models\":[{\"slug\":\"gpt-5.6-luna\",\"supported_reasoning_levels\":[{\"effort\":\"high\"}]}]}",
			expected: "does not support max",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			codex := fakeCodex(t, "exit 9", test.models)
			_, err := New(context.Background(), Config{CodexBin: codex, WorkDir: t.TempDir(), Timeout: time.Second})
			if err == nil || !strings.Contains(err.Error(), test.expected) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestParseSummaryEnforcesContract(t *testing.T) {
	valid, err := parseSummary([]byte("{\"summary\":\" 公司收入增长，核心需求改善。 \"}"))
	if err != nil || valid != "公司收入增长，核心需求改善。" {
		t.Fatalf("valid summary = %q, %v", valid, err)
	}
	for name, value := range map[string][]byte{
		"unknown field": []byte("{\"summary\":\"公司增长。\",\"source\":\"model\"}"),
		"empty":         []byte("{\"summary\":\"\"}"),
		"English":       []byte("{\"summary\":\"English only\"}"),
		"prefix":        []byte("{\"summary\":\"摘要：公司增长。\"}"),
		"multiline":     []byte("{\"summary\":\"公司增长。\\n需求改善。\"}"),
		"too long":      []byte("{\"summary\":\"" + strings.Repeat("中", 301) + "\"}"),
		"Markdown":      []byte("{\"summary\":\"# 公司增长。\"}"),
		"trailing":      []byte("{\"summary\":\"公司增长。\"} {}"),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := parseSummary(value); err == nil {
				t.Fatalf("accepted invalid payload: %s", value)
			}
		})
	}
}

func TestSummarizeRejectsInvalidFinalOutput(t *testing.T) {
	service := newTestService(t, finalBody("{\"summary\":\"English only\"}"), time.Second)

	_, err := service.Summarize(context.Background(), testArticle("body"))
	if err == nil || !strings.Contains(err.Error(), "must contain Chinese") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSummarizeRejectsEmptyArticleBeforeCreatingRun(t *testing.T) {
	service := newTestService(t, finalBody("{\"summary\":\"合成摘要。\"}"), time.Second)

	if _, err := service.Summarize(context.Background(), testArticle("  ")); err == nil {
		t.Fatal("expected empty body error")
	}
	entries, err := os.ReadDir(service.runsRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("empty article created %d run(s)", len(entries))
	}
}

func newTestService(t *testing.T, execBody string, timeout time.Duration) *Service {
	t.Helper()
	workDir := t.TempDir()
	codex := fakeCodex(t, execBody, availableModels)
	service, err := New(context.Background(), Config{CodexBin: codex, WorkDir: workDir, Timeout: timeout})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func fakeCodex(t *testing.T, execBody, models string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("synthetic Codex executable uses POSIX shell")
	}
	path := filepath.Join(t.TempDir(), "fake-codex")
	script := fmt.Sprintf(`#!/bin/sh
set -eu
if [ "$1" = "--version" ]; then
  printf '%%s\n' 'codex-cli fake-1.0'
  exit 0
fi
if [ "$1" = "debug" ] && [ "$2" = "models" ]; then
  printf '%%s\n' '%s'
  exit 0
fi
if [ "$1" = "exec" ]; then
%s
  exit 0
fi
exit 9
`, models, execBody)
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func finalBody(payload string) string {
	return fmt.Sprintf(`output=''
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then
    shift
    output="$1"
  fi
  shift
done
printf '%%s\n' '{"type":"turn.completed"}'
printf '%%s' '%s' > "$output"
`, payload)
}

func testArticle(body string) domain.StoredArticle {
	return domain.StoredArticle{ID: 1, StableID: "wired:2026-08-28:test", Title: "Test", Body: body}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(content)
}

func artifactPath(message string) string {
	_, path, _ := strings.Cut(message, "artifacts: ")
	return path
}
