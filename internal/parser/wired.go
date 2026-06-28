package parser

import (
	"errors"
	"net/url"
	"strings"

	"magazine2db/internal/domain"
)

func parseWired(text, issueDate string) ([]domain.Article, error) {
	lines := strings.Split(text, "\n")
	var markers []marker
	for _, source := range sourceMarkers(lines) {
		parsedURL, err := url.Parse(source.url)
		if err != nil || !strings.EqualFold(parsedURL.Hostname(), "www.wired.com") {
			continue
		}
		parts := strings.Split(strings.Trim(parsedURL.Path, "/"), "/")
		slug := parts[len(parts)-1]
		if slug == "" {
			continue
		}
		source.slug = slug
		markers = append(markers, source)
	}
	if len(markers) == 0 {
		return nil, errors.New("no Wired article markers found")
	}
	tocTitles := wiredTOCTitles(lines[:markers[0].line])

	articles := make([]domain.Article, 0, len(markers))
	for i, m := range markers {
		start := 0
		if i > 0 {
			start = markers[i-1].line + 1
		}
		article, ok := parseWiredBlock(trimBlank(lines[start:m.line]), issueDate, m, tocTitles)
		if ok {
			articles = append(articles, article)
		}
	}
	return articles, nil
}

func parseWiredBlock(lines []string, issueDate string, m marker, tocTitles []string) (domain.Article, bool) {
	dateIndex := -1
	for i, line := range lines {
		if dateLineRE.MatchString(strings.TrimSpace(line)) {
			dateIndex = i
			break
		}
	}
	if dateIndex < 0 {
		bodyStart := nextContentIndex(lines, 0)
		if bodyStart < 0 {
			return domain.Article{}, false
		}
		return domain.Article{
			StableID: "wired:" + issueDate + ":" + stableSlug(m.slug), Slug: stableSlug(m.slug),
			Title: wiredTitleFromTOC(m.slug, tocTitles), Section: "The Big Story", SourceURL: m.url,
			Body: strings.TrimSpace(strings.Join(cutWiredFooter(lines[bodyStart:]), "\n")),
		}, true
	}
	previous := previousContent(lines, dateIndex, 2)
	if len(previous) < 2 {
		return domain.Article{}, false
	}
	section, author := previous[0], previous[1]
	titleIndex := nextContentIndex(lines, dateIndex+1)
	if titleIndex < 0 {
		return domain.Article{}, false
	}
	title := strings.TrimSpace(lines[titleIndex])
	descriptionIndex := nextContentIndex(lines, titleIndex+1)
	description := ""
	bodyStart := titleIndex + 1
	if descriptionIndex >= 0 {
		description = strings.TrimSpace(lines[descriptionIndex])
		bodyStart = descriptionIndex + 1
	}
	return domain.Article{
		StableID: "wired:" + issueDate + ":" + stableSlug(m.slug), Slug: stableSlug(m.slug),
		Title: title, Description: description, Author: author, Section: section,
		PublishedAt: strings.TrimSpace(lines[dateIndex]), SourceURL: m.url,
		Body: strings.TrimSpace(strings.Join(cutWiredFooter(lines[bodyStart:]), "\n")),
	}, true
}

func wiredTOCTitles(lines []string) []string {
	end := len(lines)
	for i, line := range lines {
		if dateLineRE.MatchString(strings.TrimSpace(line)) {
			end = i
			break
		}
	}
	var titles []string
	for _, line := range lines[:end] {
		value := strings.TrimSpace(line)
		if !isContentLine(value) || value == "Magazine Articles" || strings.HasPrefix(value, "[") {
			continue
		}
		titles = append(titles, value)
	}
	return titles
}

func wiredTitleFromTOC(slug string, titles []string) string {
	slugNormalized := normalizeTitle(slug)
	best := ""
	for _, title := range titles {
		titleNormalized := normalizeTitle(title)
		if titleNormalized == slugNormalized {
			return title
		}
		if strings.HasPrefix(slugNormalized, titleNormalized) && len(titleNormalized) > len(normalizeTitle(best)) {
			best = title
		}
	}
	if best != "" {
		return best
	}
	return titleCaseSlug(slug)
}

func cutWiredFooter(lines []string) []string {
	body := trimBlank(lines)
	for i, line := range body {
		value := strings.TrimSpace(line)
		if value == "What Say You?" || value == "Have your say" || strings.HasPrefix(value, "Let us know what you think about this article") {
			body = body[:i]
			break
		}
	}
	return trimDecorativeTail(body)
}
