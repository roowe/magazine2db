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
	"sync"
	"syscall"

	"magazine2db/internal/config"
	"magazine2db/internal/domain"
	"magazine2db/internal/parser"
	"magazine2db/internal/store"
	"magazine2db/internal/summary"
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
	if args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		usage()
		return nil
	}
	if args[0] != "ingest" && args[0] != "search" && args[0] != "read" && args[0] != "list" && args[0] != "summarize" {
		usage()
		return fmt.Errorf("unknown command %q", args[0])
	}
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	switch args[0] {
	case "ingest":
		return runIngest(ctx, cfg, args[1:])
	case "search":
		return runSearch(ctx, cfg, args[1:])
	case "read":
		return runRead(ctx, cfg, args[1:])
	case "list":
		return runList(ctx, cfg, args[1:])
	case "summarize":
		return runSummarize(ctx, cfg, args[1:])
	}
	return nil
}

func runIngest(ctx context.Context, cfg config.Config, args []string) error {
	flags := flag.NewFlagSet("ingest", flag.ContinueOnError)
	dbPath := flags.String("db", cfg.Database, "shared SQLite database path")
	keep := flags.Int("keep", cfg.Retention, "number of latest issues retained per publisher")
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
	if exists {
		fmt.Printf("skipped: %s %s already exists\n", input.Publisher, input.IssueDate)
		return nil
	}
	issue, err := parser.Parse(input)
	if err != nil {
		return err
	}
	if err := db.InsertIssue(ctx, issue, *keep); err != nil {
		return err
	}
	fmt.Printf("ingested: %s %s, %d articles -> %s\n", issue.Publisher, issue.IssueDate, len(issue.Articles), *dbPath)
	return nil
}

func runSearch(ctx context.Context, cfg config.Config, args []string) error {
	flags := flag.NewFlagSet("search", flag.ContinueOnError)
	dbPath := flags.String("db", cfg.Database, "shared SQLite database path")
	publisher := flags.String("publisher", "", "filter by economist or wired")
	limit := flags.Int("limit", 20, "maximum results")
	jsonOutput := flags.Bool("json", false, "output machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	query := strings.Join(flags.Args(), " ")
	if strings.TrimSpace(query) == "" {
		return errors.New("usage: magazines2db search [flags] <query>")
	}
	if err := validatePublisher(*publisher); err != nil {
		return err
	}
	db, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	hits, err := db.Search(ctx, query, *publisher, *limit)
	if err != nil {
		return err
	}
	if *jsonOutput {
		if hits == nil {
			hits = []domain.SearchHit{}
		}
		return writeJSON(struct {
			Count   int                `json:"count"`
			Results []domain.SearchHit `json:"results"`
		}{Count: len(hits), Results: hits})
	}
	for _, hit := range hits {
		fmt.Printf("[%d] %s\n%s | %s | %s\n%s\n\n",
			hit.ID, hit.StableID, hit.Publisher, hit.IssueDate, hit.Title, hit.Preview)
	}
	fmt.Printf("%d result(s)\n", len(hits))
	return nil
}

func runRead(ctx context.Context, cfg config.Config, args []string) error {
	flags := flag.NewFlagSet("read", flag.ContinueOnError)
	dbPath := flags.String("db", cfg.Database, "shared SQLite database path")
	jsonOutput := flags.Bool("json", false, "output machine-readable JSON")
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
	if *jsonOutput {
		return writeJSON(article)
	}
	printArticle(article)
	return nil
}

func runList(ctx context.Context, cfg config.Config, args []string) error {
	flags := flag.NewFlagSet("list", flag.ContinueOnError)
	dbPath := flags.String("db", cfg.Database, "shared SQLite database path")
	page := flags.Int("page", 1, "page number, starting from 1")
	pageSize := flags.Int("page-size", 20, "number of articles per page")
	jsonOutput := flags.Bool("json", false, "output machine-readable JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: magazines2db list [flags]")
	}
	if *page < 1 || *pageSize < 1 {
		return errors.New("page and page-size must be positive")
	}
	db, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	items, total, err := db.ListArticleSummaries(ctx, *page, *pageSize)
	if err != nil {
		return err
	}
	if *jsonOutput {
		return writeJSON(struct {
			Page     int                     `json:"page"`
			PageSize int                     `json:"page_size"`
			Total    int                     `json:"total"`
			Items    []domain.ArticleSummary `json:"items"`
		}{Page: *page, PageSize: *pageSize, Total: total, Items: items})
	}
	for _, item := range items {
		fmt.Printf("[%d] %s\n%s\n\n", item.ID, item.Title, removeBlankLines(item.Summary))
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

func runSummarize(ctx context.Context, cfg config.Config, args []string) error {
	flags := flag.NewFlagSet("summarize", flag.ContinueOnError)
	dbPath := flags.String("db", cfg.Database, "shared SQLite database path")
	limit := flags.Int("limit", 0, "maximum unsummarized articles; 0 means all")
	concurrency := flags.Int("concurrency", cfg.Summary.Concurrency, "parallel model requests")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return errors.New("usage: magazines2db summarize [flags]")
	}
	if *concurrency < 1 {
		return errors.New("concurrency must be positive")
	}
	db, err := store.Open(*dbPath)
	if err != nil {
		return err
	}
	defer db.Close()
	articles, err := db.PendingSummaries(ctx, *limit)
	if err != nil {
		return err
	}
	if len(articles) == 0 {
		fmt.Println("nothing to summarize")
		return nil
	}
	service, err := summary.New(ctx, summary.Config{
		PrimaryBaseURL:  cfg.Summary.Primary.BaseURL,
		PrimaryAPIKey:   cfg.Summary.Primary.APIKey,
		PrimaryModel:    cfg.Summary.Primary.Model,
		FallbackBaseURL: cfg.Summary.Fallback.BaseURL,
		FallbackAPIKey:  cfg.Summary.Fallback.APIKey,
		FallbackModel:   cfg.Summary.Fallback.Model,
		MaxTokens:       cfg.Summary.MaxTokens,
	})
	if err != nil {
		return err
	}

	type result struct {
		article  domain.StoredArticle
		provider string
		err      error
	}
	jobs := make(chan domain.StoredArticle)
	results := make(chan result)
	var workers sync.WaitGroup
	for range *concurrency {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for article := range jobs {
				text, provider, err := service.Summarize(ctx, article)
				if err == nil && strings.TrimSpace(text) == "" {
					err = errors.New("model returned an empty summary")
				}
				if err == nil {
					err = db.SaveSummary(ctx, article.ID, text, provider)
				} else if saveErr := db.SaveSummaryError(ctx, article.ID, err.Error()); saveErr != nil {
					err = errors.Join(err, saveErr)
				}
				results <- result{article: article, provider: provider, err: err}
			}
		}()
	}
	go func() {
		defer close(jobs)
		for _, article := range articles {
			select {
			case jobs <- article:
			case <-ctx.Done():
				return
			}
		}
	}()
	go func() {
		workers.Wait()
		close(results)
	}()

	succeeded, failed := 0, 0
	for result := range results {
		if result.err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "failed: [%d] %s: %v\n", result.article.ID, result.article.Title, result.err)
			continue
		}
		succeeded++
		fmt.Printf("summarized: [%d] %s (%s)\n", result.article.ID, result.article.Title, result.provider)
	}
	fmt.Printf("summary complete: %d succeeded, %d failed\n", succeeded, failed)
	if failed > 0 {
		return fmt.Errorf("%d summary job(s) failed", failed)
	}
	return ctx.Err()
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
	if article.SummaryZH != "" {
		fmt.Printf("\n## 中文摘要\n\n%s\n", article.SummaryZH)
	}
	fmt.Printf("\n## Article\n\n%s\n", article.Body)
}

func writeJSON(value any) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetEscapeHTML(false)
	return encoder.Encode(value)
}

func validatePublisher(value string) error {
	if value == "" || value == "economist" || value == "wired" {
		return nil
	}
	return fmt.Errorf("unsupported publisher %q", value)
}

func usage() {
	fmt.Fprintln(os.Stderr, `magazines2db - ingest and search Economist/Wired issues

Usage:
  magazines2db ingest [--db PATH] <issue-dir>
  magazines2db search [--db PATH] [--publisher NAME] [--limit N] [--json] <query>
  magazines2db read [--db PATH] [--json] <stable-id|numeric-id>
  magazines2db list [--db PATH] [--page N] [--page-size N] [--json]
  magazines2db summarize [--db PATH] [--limit N] [--concurrency N]

Configuration is loaded from ./cfg.json, or from cfg.json next to the executable.`)
}
