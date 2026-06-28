package parser

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"magazine2db/internal/domain"
)

var (
	issueDateRE       = regexp.MustCompile(`\d{4}\.\d{2}\.\d{2}`)
	economistMarkerRE = regexp.MustCompile(`^This article was downloaded by zlibrary from https://www\.economist\.com//(.+)/(\d{4}/\d{2}/\d{2})/(.+)$`)
	wiredMarkerRE     = regexp.MustCompile(`^This article was downloaded by calibre from (https://www\.wired\.com/[^\s]+)$`)
	dateLineRE        = regexp.MustCompile(`(?i)^(Jan(?:uary)?|Feb(?:ruary)?|Mar(?:ch)?|Apr(?:il)?|May|Jun(?:e)?|Jul(?:y)?|Aug(?:ust)?|Sep(?:t(?:ember)?)?|Oct(?:ober)?|Nov(?:ember)?|Dec(?:ember)?)\s+\d{1,2}(?:st|nd|rd|th)?,?\s+\d{4}(?:\s+\d{1,2}:\d{2}\s+[AP]M)?$`)
)

var economistSections = map[string]string{
	"the-world-this-week":               "The World This Week",
	"leaders":                           "Leaders",
	"letters":                           "Letters",
	"by-invitation":                     "By Invitation",
	"briefing":                          "Briefing",
	"united-states":                     "United States",
	"interactive/united-states":         "United States",
	"interactive/europe":                "Europe",
	"the-americas":                      "The Americas",
	"international":                     "International",
	"asia":                              "Asia",
	"china":                             "China",
	"middle-east-and-africa":            "Middle East & Africa",
	"europe":                            "Europe",
	"britain":                           "Britain",
	"special-report":                    "Special Report",
	"business":                          "Business",
	"finance-and-economics":             "Finance & Economics",
	"science-and-technology":            "Science & Technology",
	"culture":                           "Culture",
	"economic-and-financial-indicators": "Economic & Financial Indicators",
	"essay":                             "Essay",
	"obituary":                          "Obituary",
}

// Input describes an issue directory or source file before parsing.
type Input struct {
	Path      string
	Publisher string
	IssueDate string
}

// InspectInput detects publisher and issue date without converting the source.
func InspectInput(path, publisher string) (Input, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Input{}, fmt.Errorf("resolve input path: %w", err)
	}
	if _, err := os.Stat(abs); err != nil {
		return Input{}, fmt.Errorf("stat input: %w", err)
	}

	if publisher == "" {
		lower := strings.ToLower(abs)
		switch {
		case strings.Contains(lower, "economist"):
			publisher = "economist"
		case strings.Contains(lower, "wired"):
			publisher = "wired"
		default:
			return Input{}, errors.New("cannot detect publisher; use --publisher economist|wired")
		}
	}
	if publisher != "economist" && publisher != "wired" {
		return Input{}, fmt.Errorf("unsupported publisher %q", publisher)
	}

	date := issueDateRE.FindString(abs)
	if date == "" {
		return Input{}, errors.New("cannot detect issue date (expected YYYY.MM.DD in path)")
	}
	return Input{Path: abs, Publisher: publisher, IssueDate: strings.ReplaceAll(date, ".", "-")}, nil
}

// Parse resolves TXT/EPUB input and extracts every article.
func Parse(input Input) (domain.Issue, error) {
	txtPath, cleanup, err := resolveText(input.Path)
	if err != nil {
		return domain.Issue{}, err
	}
	defer cleanup()

	data, err := os.ReadFile(txtPath)
	if err != nil {
		return domain.Issue{}, fmt.Errorf("read %s: %w", txtPath, err)
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")

	var articles []domain.Article
	switch input.Publisher {
	case "economist":
		articles, err = parseEconomist(text, input.IssueDate)
	case "wired":
		articles, err = parseWired(text, input.IssueDate)
	}
	if err != nil {
		return domain.Issue{}, err
	}
	if len(articles) == 0 {
		return domain.Issue{}, errors.New("no articles found")
	}
	return domain.Issue{
		Publisher:  input.Publisher,
		IssueDate:  input.IssueDate,
		SourcePath: input.Path,
		Articles:   articles,
	}, nil
}

func resolveText(path string) (string, func(), error) {
	info, err := os.Stat(path)
	if err != nil {
		return "", func() {}, err
	}
	if !info.IsDir() {
		switch strings.ToLower(filepath.Ext(path)) {
		case ".txt":
			return path, func() {}, nil
		case ".epub":
			return convertEPUB(path)
		default:
			return "", func() {}, fmt.Errorf("unsupported input file %s", path)
		}
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return "", func() {}, fmt.Errorf("read issue directory: %w", err)
	}
	var txts, epubs []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		full := filepath.Join(path, entry.Name())
		switch strings.ToLower(filepath.Ext(entry.Name())) {
		case ".txt":
			txts = append(txts, full)
		case ".epub":
			epubs = append(epubs, full)
		}
	}
	sort.Strings(txts)
	sort.Strings(epubs)
	if len(txts) > 0 {
		return txts[0], func() {}, nil
	}
	if len(epubs) > 0 {
		return convertEPUB(epubs[0])
	}
	return "", func() {}, errors.New("issue directory contains neither TXT nor EPUB")
}

func convertEPUB(epub string) (string, func(), error) {
	if _, err := exec.LookPath("ebook-convert"); err != nil {
		return "", func() {}, errors.New("TXT is missing and ebook-convert is not installed")
	}
	tmp, err := os.MkdirTemp("", "magazine-epub-")
	if err != nil {
		return "", func() {}, fmt.Errorf("create temp directory: %w", err)
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	out := filepath.Join(tmp, "issue.txt")
	cmd := exec.Command("ebook-convert", epub, out)
	if output, err := cmd.CombinedOutput(); err != nil {
		cleanup()
		return "", func() {}, fmt.Errorf("ebook-convert failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return out, cleanup, nil
}

type marker struct {
	line    int
	section string
	date    string
	slug    string
	url     string
}

func parseEconomist(text, issueDate string) ([]domain.Article, error) {
	lines := strings.Split(text, "\n")
	var markers []marker
	for i, line := range lines {
		match := economistMarkerRE.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			continue
		}
		markers = append(markers, marker{
			line: i, section: match[1], date: match[2], slug: match[3],
			url: "https://www.economist.com/" + match[1] + "/" + match[2] + "/" + match[3],
		})
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
		bodyStart := findEconomistBodyStart(block, m.section, m.slug)
		block = trimBlank(block[bodyStart:])
		if len(block) == 0 {
			continue
		}
		title := economistTitle(block, m.section, m.slug)
		section := economistSections[m.section]
		if section == "" {
			section = titleCaseSlug(strings.ReplaceAll(m.section, "/", "-"))
		}
		articles = append(articles, domain.Article{
			StableID: "economist:" + issueDate + ":" + stableSlug(m.slug),
			Slug:     stableSlug(m.slug), Title: title, Section: section,
			PublishedAt: strings.ReplaceAll(m.date, "/", "-"), SourceURL: m.url,
			Body: strings.TrimSpace(strings.Join(block, "\n")),
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

func parseWired(text, issueDate string) ([]domain.Article, error) {
	lines := strings.Split(text, "\n")
	var markers []marker
	for i, line := range lines {
		match := wiredMarkerRE.FindStringSubmatch(strings.TrimSpace(line))
		if match == nil {
			continue
		}
		slug := strings.Trim(strings.TrimPrefix(match[1], "https://www.wired.com/"), "/")
		parts := strings.Split(slug, "/")
		slug = parts[len(parts)-1]
		markers = append(markers, marker{line: i, slug: slug, url: match[1]})
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
		block := trimBlank(lines[start:m.line])
		article, ok := parseWiredBlock(block, issueDate, m, tocTitles)
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
		body := cutWiredFooter(lines[bodyStart:])
		return domain.Article{
			StableID:  "wired:" + issueDate + ":" + stableSlug(m.slug),
			Slug:      stableSlug(m.slug),
			Title:     wiredTitleFromTOC(m.slug, tocTitles),
			Section:   "The Big Story",
			SourceURL: m.url,
			Body:      strings.TrimSpace(strings.Join(body, "\n")),
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
	body := cutWiredFooter(lines[bodyStart:])

	return domain.Article{
		StableID: "wired:" + issueDate + ":" + stableSlug(m.slug),
		Slug:     stableSlug(m.slug), Title: title, Description: description,
		Author: author, Section: section, PublishedAt: strings.TrimSpace(lines[dateIndex]),
		SourceURL: m.url, Body: strings.TrimSpace(strings.Join(body, "\n")),
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

func cleanLines(lines, keywords []string) []string {
	result := make([]string, 0, len(lines))
	for _, line := range lines {
		skip := false
		for _, keyword := range keywords {
			if strings.Contains(line, keyword) {
				skip = true
				break
			}
		}
		if !skip {
			result = append(result, line)
		}
	}
	return trimBlank(result)
}

func trimBlank(lines []string) []string {
	for len(lines) > 0 && strings.TrimSpace(lines[0]) == "" {
		lines = lines[1:]
	}
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

func trimDecorativeTail(lines []string) []string {
	lines = trimBlank(lines)
	for len(lines) > 0 {
		last := strings.TrimSpace(lines[len(lines)-1])
		if last != "* * *" && last != "---" {
			break
		}
		lines = trimBlank(lines[:len(lines)-1])
	}
	return lines
}

func previousContent(lines []string, before, count int) []string {
	result := make([]string, 0, count)
	for i := before - 1; i >= 0 && len(result) < count; i-- {
		value := strings.TrimSpace(lines[i])
		if isContentLine(value) {
			result = append(result, value)
		}
	}
	return result
}

func nextContentIndex(lines []string, start int) int {
	for i := start; i < len(lines); i++ {
		if isContentLine(strings.TrimSpace(lines[i])) {
			return i
		}
	}
	return -1
}

func isContentLine(value string) bool {
	return value != "" && value != "* * *" && !strings.HasPrefix(value, "| ")
}

func nearbyDate(lines []string, start int) bool {
	seen := 0
	for i := start + 1; i < len(lines) && seen < 8; i++ {
		value := strings.TrimSpace(lines[i])
		if value == "" {
			continue
		}
		seen++
		if dateLineRE.MatchString(value) {
			return true
		}
	}
	return false
}

func nearbyLongLine(lines []string, start int) bool {
	seen := 0
	for i := start + 1; i < len(lines) && seen < 8; i++ {
		value := strings.TrimSpace(lines[i])
		if value == "" {
			continue
		}
		seen++
		if len([]rune(value)) > 120 {
			return true
		}
	}
	return false
}

func nextNonDate(lines []string, start, limit int) string {
	for i := start; i < min(len(lines), start+limit); i++ {
		value := strings.TrimSpace(lines[i])
		if value != "" && !dateLineRE.MatchString(value) {
			return value
		}
	}
	return ""
}

func normalizeTitle(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	space := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			builder.WriteRune(r)
			space = false
		} else if !space {
			builder.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(builder.String())
}

func stableSlug(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	var builder strings.Builder
	dash := false
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			builder.WriteRune(r)
			dash = false
		} else if !dash && builder.Len() > 0 {
			builder.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(builder.String(), "-")
}

func titleCaseSlug(value string) string {
	words := strings.Split(value, "-")
	for i, word := range words {
		if word != "" {
			words[i] = strings.ToUpper(word[:1]) + word[1:]
		}
	}
	return strings.Join(words, " ")
}
