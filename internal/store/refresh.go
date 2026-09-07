package store

import (
	"context"
	"fmt"
	"strings"

	"magazine2db/internal/domain"
)

// RefreshIssue retains existing IDs and metadata. An unmatched old article
// aborts the entire refresh instead of silently replacing or deleting it.
func (d *DB) RefreshIssue(ctx context.Context, issue domain.Issue) error {
	tx, err := d.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var issueID int64
	if err := tx.QueryRowContext(ctx, `UPDATE issues SET source_path = ? WHERE publisher = ? AND issue_date = ? RETURNING id`, issue.SourcePath, issue.Publisher, issue.IssueDate).Scan(&issueID); err != nil {
		return err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id, stable_id, title, source_href, source_url FROM articles WHERE issue_id = ?`, issueID)
	if err != nil {
		return err
	}
	type oldArticle struct {
		id                         int64
		stableID, title, href, url string
	}
	var old []oldArticle
	for rows.Next() {
		var a oldArticle
		if err := rows.Scan(&a.id, &a.stableID, &a.title, &a.href, &a.url); err != nil {
			rows.Close()
			return err
		}
		old = append(old, a)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	matched := map[int64]bool{}
	for _, a := range issue.Articles {
		if a.BodyXHTML == "" {
			return fmt.Errorf("refresh requires original EPUB XHTML")
		}
		var candidates []int64
		for _, previous := range old {
			if (previous.href != "" && previous.href == a.SourceHref) || (a.SourceURL != "" && previous.url == a.SourceURL) || previous.stableID == a.StableID || titleKey(previous.title) == titleKey(a.Title) {
				candidates = append(candidates, previous.id)
			}
		}
		if len(candidates) > 1 {
			return fmt.Errorf("ambiguous refresh match for %q", a.Title)
		}
		if len(candidates) == 1 {
			id := candidates[0]
			if matched[id] {
				return fmt.Errorf("multiple EPUB articles match article %d", id)
			}
			matched[id] = true
			_, err = tx.ExecContext(ctx, `UPDATE articles SET title = ?, section = ?, body = ?, body_xhtml = ?, source_href = ? WHERE id = ?`, a.Title, a.Section, a.Body, a.BodyXHTML, a.SourceHref, id)
		} else {
			_, err = tx.ExecContext(ctx, `INSERT INTO articles (stable_id, issue_id, publisher, issue_date, slug, title, description, author, section, published_at, source_url, body, body_xhtml, source_href) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, a.StableID, issueID, issue.Publisher, issue.IssueDate, a.Slug, a.Title, a.Description, a.Author, a.Section, a.PublishedAt, a.SourceURL, a.Body, a.BodyXHTML, a.SourceHref)
		}
		if err != nil {
			return fmt.Errorf("refresh %q: %w", a.Title, err)
		}
	}
	for _, previous := range old {
		if !matched[previous.id] {
			return fmt.Errorf("EPUB has no unambiguous match for existing article %d %q; refresh rolled back", previous.id, previous.title)
		}
	}
	return tx.Commit()
}

func titleKey(title string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.NewReplacer("’", "'", "‘", "'", "“", `"`, "”", `"`).Replace(title)), " "))
}
