package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"magazine2db/internal/config"
	"magazine2db/internal/domain"
	"magazine2db/internal/parser"
	"magazine2db/internal/store"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	if len(args) == 0 {
		usage()
		return errors.New("missing command")
	}
	var handler func(context.Context, config.Config, []string) error
	switch args[0] {
	case "help", "-h", "--help":
		usage()
		return nil
	case "ingest":
		handler = runIngest
	case "issue":
		handler = runIssue
	case "read":
		handler = runRead
	case "list":
		handler = runList
	default:
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	return handler(ctx, cfg, args[1:])
}

func runIssue(ctx context.Context, cfg config.Config, args []string) error {
	flags := flag.NewFlagSet("issue", flag.ContinueOnError)
	dbPath := flags.String("db", cfg.Database, "shared SQLite database path")
	jsonOutput := flags.Bool("json", false, "output machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: magazines2db issue [flags]")
	}
	db, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	issues, err := db.ListIssues(ctx)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSON(struct {
			Count  int                `json:"count"`
			Issues []domain.IssueInfo `json:"issues"`
		}{Count: len(issues), Issues: issues})
	}
	for _, issue := range issues {
		fmt.Printf("[%d] %-10s %s  %d articles  %s\n", issue.ID, issue.Publisher, issue.IssueDate, issue.ArticleCount, issue.ImportedAt)
	}
	return nil
}

// runIngest 先从目录路径识别刊物和期号，再查数据库决定是否需要解析。
// 已存在且未指定 --force 时直接跳过，无需查找或读取 EPUB。
// 需要导入时才定位并解析 EPUB：新期刊新增，强制导入时整期删除后重新入库；缺少 EPUB 则报错。
func runIngest(ctx context.Context, cfg config.Config, args []string) error {
	flags := flag.NewFlagSet("ingest", flag.ContinueOnError)
	dbPath := flags.String("db", cfg.Database, "shared SQLite database path")
	keep := flags.Int("keep", cfg.Retention, "number of latest issues retained per publisher")
	force := flags.Bool("force", false, "reparse EPUB and replace the entire issue")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: magazines2db ingest [flags] <issue-dir>")
	}
	input, err := parser.InspectInput(flags.Arg(0))
	if err != nil {
		return err
	}
	db, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	exists, err := db.HasIssue(ctx, input.Publisher, input.IssueDate)
	if err != nil {
		return fmt.Errorf("check duplicate issue: %w", err)
	}
	if exists && !*force {
		fmt.Printf("skipped: %s %s already exists\n", input.Publisher, input.IssueDate)
		return nil
	}
	issue, err := parser.Parse(input)
	if err != nil {
		return err
	}
	if err := db.InsertIssue(ctx, issue, *keep, *force); err != nil {
		return err
	}
	fmt.Printf("ingested: %s %s, %d articles -> %s\n", issue.Publisher, issue.IssueDate, len(issue.Articles), *dbPath)
	return nil
}

func runRead(ctx context.Context, cfg config.Config, args []string) error {
	flags := flag.NewFlagSet("read", flag.ContinueOnError)
	dbPath := flags.String("db", cfg.Database, "shared SQLite database path")
	jsonOutput := flags.Bool("json", false, "output machine-readable JSON")
	xhtml := flags.Bool("xhtml", false, "return original XHTML; with --json include body_xhtml")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: magazines2db read [flags] <stable-id|numeric-id>")
	}
	db, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	article, err := db.Read(ctx, flags.Arg(0))
	if err != nil {
		return err
	}
	if *xhtml && article.BodyXHTML == "" {
		return errors.New("original XHTML unavailable; refresh this issue from EPUB")
	}
	if *jsonOutput {
		if !*xhtml {
			article.BodyXHTML = ""
		}
		return writeJSON(article)
	}
	if *xhtml {
		_, err := fmt.Print(article.BodyXHTML)
		return err
	}
	printArticle(article)
	return nil
}

func runList(ctx context.Context, cfg config.Config, args []string) error {
	flags := flag.NewFlagSet("list", flag.ContinueOnError)
	dbPath := flags.String("db", cfg.Database, "shared SQLite database path")
	page := flags.Int("page", 1, "page number, starting from 1")
	pageSize := flags.Int("page-size", 20, "number of articles per page")
	issueID := flags.Int64("issue", 0, "filter by issue ID")
	jsonOutput := flags.Bool("json", false, "output machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: magazines2db list [flags]")
	}
	if *page < 1 || *pageSize < 1 || *issueID < 0 {
		return errors.New("page and page-size must be positive; issue must not be negative")
	}
	db, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	items, total, err := db.ListArticles(ctx, *page, *pageSize, *issueID)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSON(struct {
			Page     int                      `json:"page"`
			PageSize int                      `json:"page_size"`
			Total    int                      `json:"total"`
			Items    []domain.ArticleListItem `json:"items"`
		}{Page: *page, PageSize: *pageSize, Total: total, Items: items})
	}
	for _, item := range items {
		fmt.Printf("[%d] %s\n%s\n\n", item.ID, item.Title, removeBlankLines(item.Excerpt))
	}
	fmt.Printf("page %d | page size %d | total %d\n", *page, *pageSize, total)
	return nil
}

func removeBlankLines(value string) string {
	lines := strings.Split(value, "\n")
	result := lines[:0]
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			result = append(result, line)
		}
	}
	return strings.Join(result, "\n")
}

func printArticle(article domain.StoredArticle) {
	fmt.Printf("# %s\n\n", article.Title)
	fmt.Printf("ID: %d\nStable ID: %s\nPublisher: %s\nIssue: %s\n",
		article.ID, article.StableID, article.Publisher, article.IssueDate)
	if article.Author != "" {
		fmt.Printf("Author: %s\n", article.Author)
	}
	if article.Section != "" {
		fmt.Printf("Section: %s\n", article.Section)
	}
	if article.PublishedAt != "" {
		fmt.Printf("Published: %s\n", article.PublishedAt)
	}
	fmt.Printf("Source: %s\n", article.SourceURL)
	if article.Description != "" {
		fmt.Printf("\n%s\n", article.Description)
	}
	fmt.Printf("\n## Article\n\n%s\n", article.Body)
}

func writeJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func usage() {
	fmt.Fprintln(os.Stderr, `magazine2db - ingest and read Economist/Wired issues

Usage:
  magazine2db ingest [--db PATH] [--force] <issue-dir>
  magazine2db issue [--db PATH] [--json]
  magazine2db read [--db PATH] [--json] [--xhtml] <stable-id|numeric-id>
  magazine2db list [--db PATH] [--page N] [--page-size N] [--issue ID] [--json]

Configuration is loaded from ./cfg.json, or from cfg.json next to the executable.`)
}
