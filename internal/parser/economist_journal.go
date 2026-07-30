package parser

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"magazine2db/internal/domain"
)

// economistJournalSections maps lower-cased section names as printed in the
// journal layout to their canonical display form.
var economistJournalSections = func() map[string]string {
	sections := map[string]string{"1843": "1843"}
	for _, name := range economistSections {
		sections[strings.ToLower(name)] = name
	}
	return sections
}()

var ordinalSuffixRE = regexp.MustCompile(`(\d)(st|nd|rd|th)\b`)

// parseEconomistJournal handles Economist issues whose EPUB lacks the
// "This article was downloaded by ... from <url>" footers. In that layout each
// article starts with a "Section | Rubric" header line (or a "The world this
// week" subsection header) followed by its title and standfirst, optionally a
// "* * *" separator, a date line and the body. Bare section-name lines with
// title lists sit between sections as navigation and must stay out of bodies.
func parseEconomistJournal(text, issueDate string) ([]domain.Article, error) {
	lines := strings.Split(text, "\n")
	anchors := economistJournalAnchors(lines)
	articles := make([]domain.Article, 0, len(anchors))
	seen := make(map[string]int)
	for i, anchor := range anchors {
		end := len(lines)
		if i+1 < len(anchors) {
			end = anchors[i+1].line
		}
		if article, ok := buildJournalArticle(lines[anchor.line:end], issueDate, anchor.section, seen); ok {
			articles = append(articles, article)
		}
	}
	if len(articles) == 0 {
		return nil, errors.New("no Economist article markers found")
	}
	return articles, nil
}

type journalAnchor struct {
	line    int
	section string
}

func economistJournalAnchors(lines []string) []journalAnchor {
	var anchors []journalAnchor
	for i, line := range lines {
		value := strings.TrimSpace(line)
		if section, ok := journalRubricSection(value); ok {
			anchors = append(anchors, journalAnchor{line: i, section: section})
			continue
		}
		if strings.EqualFold(value, "the world this week") && isJournalTWTWHeader(lines, i) {
			anchors = append(anchors, journalAnchor{line: i, section: "The World This Week"})
		}
	}
	return anchors
}

func journalRubricSection(value string) (string, bool) {
	prefix, _, ok := strings.Cut(value, " | ")
	if !ok {
		return "", false
	}
	section, ok := economistJournalSections[strings.ToLower(strings.TrimSpace(prefix))]
	return section, ok
}

// isJournalTWTWHeader distinguishes an article header ("The world this week"
// followed by one subsection line, an optional standfirst and "* * *") from
// look-alike table-of-contents entries that list several titles in a row.
func isJournalTWTWHeader(lines []string, index int) bool {
	content := 0
	for i := index + 1; i < len(lines) && i <= index+15; i++ {
		value := strings.TrimSpace(lines[i])
		if value == "" {
			continue
		}
		if value == "* * *" {
			return content > 0
		}
		content++
		if content > 2 {
			return false
		}
	}
	return false
}

func buildJournalArticle(block []string, issueDate, section string, seen map[string]int) (domain.Article, bool) {
	// block[0] is the header line; title and standfirst are the next content
	// lines, stopping at the "* * *" separator or the date line. Some headers
	// (image-led pieces) have neither separator nor body and are skipped.
	title, description := "", ""
	dateIndex := -1
	for i := 1; i < len(block); i++ {
		value := strings.TrimSpace(block[i])
		if value == "* * *" {
			break
		}
		if dateLineRE.MatchString(value) {
			dateIndex = i
			break
		}
		if value == "" {
			continue
		}
		if title == "" {
			title = value
		} else if description == "" {
			description = value
		}
	}
	if dateIndex < 0 {
		for i := 1; i < len(block); i++ {
			if dateLineRE.MatchString(strings.TrimSpace(block[i])) {
				dateIndex = i
				break
			}
		}
	}
	if title == "" || dateIndex < 0 {
		return domain.Article{}, false
	}

	body := make([]string, 0, len(block))
	for i := dateIndex + 1; i < len(block); i++ {
		if _, isNav := economistJournalSections[strings.ToLower(strings.TrimSpace(block[i]))]; isNav {
			break
		}
		body = append(body, block[i])
	}
	body = cleanLines(body, economistAdKeywords)
	if len(trimBlank(body)) == 0 {
		return domain.Article{}, false
	}

	slug := stableSlug(title)
	if slug == "" {
		return domain.Article{}, false
	}
	if count := seen[slug]; count > 0 {
		seen[slug] = count + 1
		slug = fmt.Sprintf("%s-%d", slug, count+1)
	} else {
		seen[slug] = 1
	}
	return domain.Article{
		StableID: "economist:" + issueDate + ":" + slug, Slug: slug,
		Title: title, Description: description, Section: section,
		PublishedAt: parseJournalDate(strings.TrimSpace(block[dateIndex])),
		Body:        strings.TrimSpace(strings.Join(body, "\n")),
	}, true
}

func parseJournalDate(value string) string {
	cleaned := ordinalSuffixRE.ReplaceAllString(value, "$1")
	for _, layout := range []string{"Jan 2 2006", "January 2 2006", "Jan 2, 2006", "January 2, 2006"} {
		if parsed, err := time.Parse(layout, cleaned); err == nil {
			return parsed.Format("2006-01-02")
		}
	}
	return value
}
