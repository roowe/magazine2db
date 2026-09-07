//go:build e2e

package main

import (
	"archive/zip"
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

const e2eArticleID = "economist:2026-06-27:a-practical-quantum-network"

func TestCLIEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
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
	assertContains(t, help, "magazine2db ingest")

	txtOnly := filepath.Join(runtimeDir, "economist_2026.06.20")
	if err := os.Mkdir(txtOnly, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(txtOnly, "issue.txt"), []byte("Legacy text must not be imported."), 0600); err != nil {
		t.Fatal(err)
	}
	missingSource := exec.CommandContext(ctx, binary, "ingest", txtOnly)
	missingSource.Dir = runtimeDir
	output, err := missingSource.CombinedOutput()
	if err == nil {
		t.Fatal("TXT-only ingest should fail")
	}
	assertContains(t, string(output), "issue directory contains no EPUB")

	issuePath := writeE2EIssue(t, runtimeDir)
	ingested := runE2ECommand(t, ctx, runtimeDir, binary, "ingest", "--force", issuePath)
	assertContains(t, ingested, "ingested: economist 2026-06-27, 1 articles")
	if _, err := os.Stat(filepath.Join(runtimeDir, "magazines.db")); err != nil {
		t.Fatalf("database was not created next to cfg.json: %v", err)
	}

	// An existing issue needs no source unless force is requested.
	epubPath := filepath.Join(issuePath, "issue.epub")
	if err := os.Rename(epubPath, epubPath+".bak"); err != nil {
		t.Fatal(err)
	}
	duplicate := runE2ECommand(t, ctx, runtimeDir, binary, "ingest", issuePath)
	assertContains(t, duplicate, "skipped: economist 2026-06-27 already exists")
	forced := exec.CommandContext(ctx, binary, "ingest", "--force", issuePath)
	forced.Dir = runtimeDir
	output, err = forced.CombinedOutput()
	if err == nil {
		t.Fatal("force must require an EPUB even for an existing issue")
	}
	assertContains(t, string(output), "issue directory contains no EPUB")
	if err := os.Rename(epubPath+".bak", epubPath); err != nil {
		t.Fatal(err)
	}
	plainIssues := runE2ECommand(t, ctx, runtimeDir, binary, "issue")
	assertContains(t, plainIssues, "economist  2026-06-27  1 articles")
	jsonIssues := runE2ECommand(t, ctx, runtimeDir, binary, "issue", "--json")
	var issueResult struct {
		Count  int                `json:"count"`
		Issues []domain.IssueInfo `json:"issues"`
	}
	if err := json.Unmarshal([]byte(jsonIssues), &issueResult); err != nil {
		t.Fatalf("decode issue output: %v\n%s", err, jsonIssues)
	}
	if issueResult.Count != 1 || len(issueResult.Issues) != 1 || issueResult.Issues[0].ArticleCount != 1 {
		t.Fatalf("unexpected issue output: %+v", issueResult)
	}
	issueID := strconv.FormatInt(issueResult.Issues[0].ID, 10)

	byStableID := runE2ECommand(t, ctx, runtimeDir, binary, "read", e2eArticleID)
	assertContains(t, byStableID, "# A practical quantum network")
	lookup := runE2ECommand(t, ctx, runtimeDir, binary, "read", "--json", e2eArticleID)
	var stored domain.StoredArticle
	if err := json.Unmarshal([]byte(lookup), &stored); err != nil || stored.ID == 0 {
		t.Fatalf("read article ID: %+v, %v", stored, err)
	}
	numericID := strconv.FormatInt(stored.ID, 10)
	byNumericID := runE2ECommand(t, ctx, runtimeDir, binary, "read", numericID)
	assertContains(t, byNumericID, "Stable ID: "+e2eArticleID)
	jsonRead := runE2ECommand(t, ctx, runtimeDir, binary, "read", "--json", numericID)
	var articleResult domain.StoredArticle
	if err := json.Unmarshal([]byte(jsonRead), &articleResult); err != nil {
		t.Fatalf("decode JSON read output: %v\n%s", err, jsonRead)
	}
	if articleResult.StableID != e2eArticleID || articleResult.Body == "" {
		t.Fatalf("unexpected JSON read output: %+v", articleResult)
	}
	plainList := runE2ECommand(t, ctx, runtimeDir, binary, "list", "--page", "1", "--page-size", "1", "--issue", issueID)
	assertContains(t, plainList, "["+numericID+"] A practical quantum network")
	assertContains(t, plainList, "page 1 | page size 1 | total 1")
	listOutput := runE2ECommand(t, ctx, runtimeDir, binary, "list", "--page", "1", "--page-size", "1", "--issue", issueID, "--json")
	var listResult struct {
		Page     int                      `json:"page"`
		PageSize int                      `json:"page_size"`
		Total    int                      `json:"total"`
		Items    []domain.ArticleListItem `json:"items"`
	}
	if err := json.Unmarshal([]byte(listOutput), &listResult); err != nil {
		t.Fatalf("decode list output: %v\n%s", err, listOutput)
	}
	if listResult.Page != 1 || listResult.PageSize != 1 || listResult.Total != 1 || len(listResult.Items) != 1 {
		t.Fatalf("unexpected list pagination: %+v", listResult)
	}
	if listResult.Items[0].Title != "A practical quantum network" || listResult.Items[0].Excerpt == "" {
		t.Fatalf("unexpected list item: %+v", listResult.Items[0])
	}

	// Force replaces the issue articles; look up article IDs again after refresh.
	const xhtml = `<html><head><title>xhtmlunindexed</title></head><body><h1>A practical quantum network</h1><p>Fresh evidence about a quantum network.</p></body></html>`
	writeE2EEPUB(t, issuePath, xhtml)
	runE2ECommand(t, ctx, runtimeDir, binary, "ingest", "--force", issuePath)
	updated := runE2ECommand(t, ctx, runtimeDir, binary, "read", "--json", e2eArticleID)
	if err := json.Unmarshal([]byte(updated), &articleResult); err != nil {
		t.Fatal(err)
	}
	numericID = strconv.FormatInt(articleResult.ID, 10)
	if strings.Contains(updated, "body_xhtml") {
		t.Fatal("default read must not include XHTML")
	}
	assertContains(t, updated, "Fresh evidence")
	assertContains(t, updated, e2eArticleID)
	raw := runE2ECommand(t, ctx, runtimeDir, binary, "read", "--xhtml", numericID)
	if raw != xhtml {
		t.Fatal("raw XHTML was changed")
	}
	withXHTML := runE2ECommand(t, ctx, runtimeDir, binary, "read", "--json", "--xhtml", numericID)
	if err := json.Unmarshal([]byte(withXHTML), &articleResult); err != nil || articleResult.BodyXHTML != xhtml {
		t.Fatalf("XHTML JSON round trip failed: %v", err)
	}
	// Running outside the runtime directory must fall back to cfg.json next to the binary.
	outsideDir := t.TempDir()
	fromOutside := runE2ECommand(t, ctx, outsideDir, binary, "read", e2eArticleID)
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
	isolatedCfg, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeDir, "cfg.json"), isolatedCfg, 0o600); err != nil {
		t.Fatal(err)
	}

}

func writeE2EIssue(t *testing.T, runtimeDir string) string {
	t.Helper()
	issueDir := filepath.Join(runtimeDir, "economist_2026.06.27")
	if err := os.Mkdir(issueDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeE2EEPUB(t, issueDir, `<html><body><h1>A practical quantum network</h1><p>Researchers connected quantum devices across a fibre network.</p></body></html>`)
	return issueDir
}

func writeE2EEPUB(t *testing.T, issueDir, xhtml string) {
	t.Helper()
	f, err := os.Create(filepath.Join(issueDir, "issue.epub"))
	if err != nil {
		t.Fatal(err)
	}
	z := zip.NewWriter(f)
	for name, data := range map[string]string{
		"META-INF/container.xml": `<container><rootfiles><rootfile full-path="package.opf"/></rootfiles></container>`,
		"package.opf":            `<package><manifest><item id="toc" href="toc.ncx" media-type="application/x-dtbncx+xml"/><item id="article" href="article.xhtml"/></manifest><spine toc="toc"><itemref idref="article"/></spine></package>`,
		"toc.ncx":                `<ncx><navMap><navPoint><navLabel><text>A practical quantum network</text></navLabel><content src="article.xhtml"/></navPoint></navMap></ncx>`,
		"article.xhtml":          xhtml,
	} {
		w, err := z.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(data)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
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
