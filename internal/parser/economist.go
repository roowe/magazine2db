package parser

import (
	"errors"
	"net/url"
	"strings"
	"time"

	"magazine2db/internal/domain"
)

var economistSections = map[string]string{
	"the-world-this-week": "The World This Week", "leaders": "Leaders", "letters": "Letters",
	"by-invitation": "By Invitation", "briefing": "Briefing", "united-states": "United States",
	"interactive/united-states": "United States", "interactive/europe": "Europe", "the-americas": "The Americas",
	"international": "International", "asia": "Asia", "china": "China", "middle-east-and-africa": "Middle East & Africa",
	"europe": "Europe", "britain": "Britain", "special-report": "Special Report", "business": "Business",
	"finance-and-economics": "Finance & Economics", "science-and-technology": "Science & Technology",
	"culture": "Culture", "economic-and-financial-indicators": "Economic & Financial Indicators",
	"essay": "Essay", "obituary": "Obituary",
}

func parseEconomist(text, issueDate string) ([]domain.Article, error) {
	lines := strings.Split(text, "\n")
	var markers []marker
	for _, source := range sourceMarkers(lines) {
		parsedURL, err := url.Parse(source.url)
		if err != nil || !strings.EqualFold(parsedURL.Hostname(), "www.economist.com") {
			continue
		}
		parts := strings.Split(strings.Trim(parsedURL.Path, "/"), "/")
		if len(parts) < 5 {
			continue
		}
		date := strings.Join(parts[len(parts)-4:len(parts)-1], "/")
		if _, err := time.Parse("2006/01/02", date); err != nil {
			continue
		}
		source.section = strings.Join(parts[:len(parts)-4], "/")
		source.date = date
		source.slug = parts[len(parts)-1]
		markers = append(markers, source)
	}
	if len(markers) == 0 {
		return nil, errors.New("no Economist article markers found")
	}

	articles := make([]domain.Article, 0, len(markers))
	for i, m := range markers {
		start := 0
		if i > 0 {
			start = markers[i-1].line + 1
		}
		block := cleanLines(lines[start:m.line], []string{"优质App推荐", "英阅阅读器", "Duolingo", "Notability", "点击下载"})
		if len(block) == 0 {
			continue
		}
		block = trimBlank(block[findEconomistBodyStart(block, m.section, m.slug):])
		if len(block) == 0 {
			continue
		}
		title := economistTitle(block, m.section, m.slug)
		section := economistSections[m.section]
		if section == "" {
			section = titleCaseSlug(strings.ReplaceAll(m.section, "/", "-"))
		}
		articles = append(articles, domain.Article{
			StableID: "economist:" + issueDate + ":" + stableSlug(m.slug), Slug: stableSlug(m.slug),
			Title: title, Section: section, PublishedAt: strings.ReplaceAll(m.date, "/", "-"),
			SourceURL: m.url, Body: strings.TrimSpace(strings.Join(block, "\n")),
		})
	}
	return articles, nil
}

func findEconomistBodyStart(lines []string, section, slug string) int {
	for i, line := range lines {
		if strings.Contains(strings.TrimSpace(line), " | ") {
			return i
		}
	}
	if section == "the-world-this-week" {
		for i := len(lines) - 1; i >= 0; i-- {
			if strings.EqualFold(strings.TrimSpace(lines[i]), "the world this week") && nearbyDate(lines, i) {
				return i
			}
		}
	}
	last := -1
	for i, line := range lines {
		if normalizeTitle(line) == normalizeTitle(slug) {
			last = i
			if nearbyDate(lines, i) || nearbyLongLine(lines, i) {
				return i
			}
		}
	}
	if last >= 0 {
		return last
	}
	return 0
}

func economistTitle(lines []string, section, slug string) string {
	first := strings.TrimSpace(lines[0])
	if strings.Contains(first, " | ") {
		if value := nextNonDate(lines, 1, 8); value != "" {
			return value
		}
	}
	if section == "the-world-this-week" && strings.EqualFold(first, "the world this week") {
		for _, line := range lines[1:min(len(lines), 8)] {
			value := strings.TrimSpace(line)
			if value != "" && !dateLineRE.MatchString(value) && !strings.EqualFold(value, first) {
				return value
			}
		}
	}
	if first != "" {
		return first
	}
	return titleCaseSlug(slug)
}
