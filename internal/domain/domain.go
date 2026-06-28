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
	ID           int64  `json:"id"`
	StableID     string `json:"stable_id"`
	Publisher    string `json:"publisher"`
	IssueDate    string `json:"issue_date"`
	Slug         string `json:"slug"`
	Title        string `json:"title"`
	Description  string `json:"description"`
	Author       string `json:"author"`
	Section      string `json:"section"`
	PublishedAt  string `json:"published_at"`
	SourceURL    string `json:"source_url"`
	Body         string `json:"body"`
	SummaryZH    string `json:"summary_zh"`
	SummaryError string `json:"summary_error"`
}

// SearchHit is a compact full-text search result.
type SearchHit struct {
	ID        int64  `json:"id"`
	StableID  string `json:"stable_id"`
	Publisher string `json:"publisher"`
	IssueDate string `json:"issue_date"`
	Section   string `json:"section"`
	Title     string `json:"title"`
	Preview   string `json:"preview"`
}

// ArticleSummary is a compact item for paginated article browsing.
type ArticleSummary struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
}
