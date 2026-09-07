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
	StableID   string
	Slug       string
	Title      string
	Section    string
	SourceURL  string
	Body       string
	BodyXHTML  string
	SourceHref string
}

// StoredArticle is an article loaded from SQLite.
type StoredArticle struct {
	ID         int64  `json:"id"`
	StableID   string `json:"stable_id"`
	Publisher  string `json:"publisher"`
	IssueDate  string `json:"issue_date"`
	Slug       string `json:"slug"`
	Title      string `json:"title"`
	Section    string `json:"section"`
	SourceURL  string `json:"source_url"`
	Body       string `json:"body"`
	BodyXHTML  string `json:"body_xhtml,omitempty"`
	SourceHref string `json:"source_href"`
}

// ArticleListItem is a compact item for paginated article browsing.
type ArticleListItem struct {
	ID      int64  `json:"id"`
	Title   string `json:"title"`
	Excerpt string `json:"excerpt"`
}

// IssueInfo describes one imported magazine issue.
type IssueInfo struct {
	ID           int64  `json:"id"`
	Publisher    string `json:"publisher"`
	IssueDate    string `json:"issue_date"`
	ArticleCount int    `json:"article_count"`
	ImportedAt   string `json:"imported_at"`
}
