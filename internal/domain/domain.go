package domain

// Issue is one imported magazine issue.
type Issue struct {
	Publisher  string
	IssueDate  string
	SourcePath string
	Articles   []Article
}

// Article is one article extracted from an issue.
type Article struct {
	StableID    string
	Slug        string
	Title       string
	Description string
	Author      string
	Section     string
	PublishedAt string
	SourceURL   string
	Body        string
}

// StoredArticle is an article loaded from SQLite.
type StoredArticle struct {
	ID           int64
	StableID     string
	Publisher    string
	IssueDate    string
	Slug         string
	Title        string
	Description  string
	Author       string
	Section      string
	PublishedAt  string
	SourceURL    string
	Body         string
	SummaryZH    string
	SummaryError string
}

// SearchHit is a compact full-text search result.
type SearchHit struct {
	ID        int64
	StableID  string
	Publisher string
	IssueDate string
	Section   string
	Title     string
	Preview   string
}
