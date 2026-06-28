//go:build e2e

package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"magazine2db/internal/domain"
)

const e2eArticleID = "economist:2026-06-27:a-practical-quantum-network"

func TestCLIEndToEndWithRealSummaryAPI(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	projectDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	runtimeDir := t.TempDir()
	prepareRuntime(t, projectDir, runtimeDir)

	binary := filepath.Join(runtimeDir, "magazines2db")
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, ".")
	build.Dir = projectDir
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}

	help := runE2ECommand(t, ctx, runtimeDir, binary, "help")
	assertContains(t, help, "magazines2db ingest")

	issuePath := writeE2EIssue(t, runtimeDir)
	ingested := runE2ECommand(t, ctx, runtimeDir, binary, "ingest", issuePath)
	assertContains(t, ingested, "ingested: economist 2026-06-27, 1 articles")
	if _, err := os.Stat(filepath.Join(runtimeDir, "magazines.db")); err != nil {
		t.Fatalf("database was not created next to cfg.json: %v", err)
	}

	duplicate := runE2ECommand(t, ctx, runtimeDir, binary, "ingest", issuePath)
	assertContains(t, duplicate, "skipped: economist 2026-06-27 already exists")

	search := runE2ECommand(t, ctx, runtimeDir, binary, "search", "quantum network")
	assertContains(t, search, e2eArticleID)
	numericID := regexp.MustCompile(`(?m)^\[(\d+)]`).FindStringSubmatch(search)
	if len(numericID) != 2 {
		t.Fatalf("numeric article ID missing from search output:\n%s", search)
	}
	jsonSearch := runE2ECommand(t, ctx, runtimeDir, binary, "search", "--json", "quantum network")
	var searchResult struct {
		Count   int                `json:"count"`
		Results []domain.SearchHit `json:"results"`
	}
	if err := json.Unmarshal([]byte(jsonSearch), &searchResult); err != nil {
		t.Fatalf("decode JSON search output: %v\n%s", err, jsonSearch)
	}
	if searchResult.Count != 1 || len(searchResult.Results) != 1 || searchResult.Results[0].StableID != e2eArticleID {
		t.Fatalf("unexpected JSON search output: %+v", searchResult)
	}

	byStableID := runE2ECommand(t, ctx, runtimeDir, binary, "read", e2eArticleID)
	assertContains(t, byStableID, "# A practical quantum network")
	byNumericID := runE2ECommand(t, ctx, runtimeDir, binary, "read", numericID[1])
	assertContains(t, byNumericID, "Stable ID: "+e2eArticleID)
	jsonRead := runE2ECommand(t, ctx, runtimeDir, binary, "read", "--json", numericID[1])
	var articleResult domain.StoredArticle
	if err := json.Unmarshal([]byte(jsonRead), &articleResult); err != nil {
		t.Fatalf("decode JSON read output: %v\n%s", err, jsonRead)
	}
	if articleResult.StableID != e2eArticleID || articleResult.Body == "" {
		t.Fatalf("unexpected JSON read output: %+v", articleResult)
	}
	plainList := runE2ECommand(t, ctx, runtimeDir, binary, "list", "--page", "1", "--page-size", "1")
	assertContains(t, plainList, "["+numericID[1]+"] A practical quantum network")
	assertContains(t, plainList, "page 1 | page size 1 | total 1")
	listOutput := runE2ECommand(t, ctx, runtimeDir, binary, "list", "--page", "1", "--page-size", "1", "--json")
	var listResult struct {
		Page     int                     `json:"page"`
		PageSize int                     `json:"page_size"`
		Total    int                     `json:"total"`
		Items    []domain.ArticleSummary `json:"items"`
	}
	if err := json.Unmarshal([]byte(listOutput), &listResult); err != nil {
		t.Fatalf("decode list output: %v\n%s", err, listOutput)
	}
	if listResult.Page != 1 || listResult.PageSize != 1 || listResult.Total != 1 || len(listResult.Items) != 1 {
		t.Fatalf("unexpected list pagination: %+v", listResult)
	}
	if listResult.Items[0].Title != "A practical quantum network" || listResult.Items[0].Summary == "" {
		t.Fatalf("unexpected list item: %+v", listResult.Items[0])
	}

	// This is the only real model call in the test.
	summarized := runE2ECommand(t, ctx, runtimeDir, binary, "summarize", "--limit", "1", "--concurrency", "1")
	assertContains(t, summarized, "summary complete: 1 succeeded, 0 failed")
	article := runE2ECommand(t, ctx, runtimeDir, binary, "read", e2eArticleID)
	assertContains(t, article, "## 中文摘要")

	// Running outside the runtime directory must fall back to cfg.json next to the binary.
	outsideDir := t.TempDir()
	fromOutside := runE2ECommand(t, ctx, outsideDir, binary, "search", "quantum network")
	assertContains(t, fromOutside, e2eArticleID)
}

func prepareRuntime(t *testing.T, projectDir, runtimeDir string) {
	t.Helper()
	cfgData, err := os.ReadFile(filepath.Join(projectDir, "cfg.json"))
	if err != nil {
		t.Fatalf("read project cfg.json: %v", err)
	}
	var cfg map[string]any
	if err := json.Unmarshal(cfgData, &cfg); err != nil {
		t.Fatalf("decode project cfg.json: %v", err)
	}
	cfg["database"] = "magazines.db"
	summary, ok := cfg["summary"].(map[string]any)
	if !ok {
		t.Fatal("cfg.json summary must be an object")
	}
	summary["concurrency"] = 1
	isolatedCfg, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "cfg.json"), isolatedCfg, 0o600); err != nil {
		t.Fatal(err)
	}

	env, err := os.ReadFile(filepath.Join(projectDir, ".env"))
	if err != nil {
		t.Fatalf("read project .env with real API keys: %v", err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, ".env"), env, 0o600); err != nil {
		t.Fatal(err)
	}
}

func writeE2EIssue(t *testing.T, runtimeDir string) string {
	t.Helper()
	issueDir := filepath.Join(runtimeDir, "economist_2026.06.27")
	if err := os.Mkdir(issueDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(issueDir, "issue.txt")
	content := strings.TrimSpace(`
Leaders
A practical quantum network

Science and technology | Networking

A practical quantum network

June 27th 2026

Researchers connected several small quantum devices across an ordinary metropolitan fibre network. The experiment showed how carefully timed signals can preserve fragile quantum states while conventional traffic continues to use the same infrastructure. The team says the work is an engineering demonstration rather than a finished commercial service. Independent scientists regard it as a useful step toward secure distributed computing, while noting that larger networks will require better memories and error correction.

This article was downloaded by zlibrary from https://www.economist.com//science-and-technology/2026/06/27/a-practical-quantum-network
`) + "\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return issueDir
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
