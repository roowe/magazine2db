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
	issueDateRE = regexp.MustCompile(`\d{4}\.\d{2}\.\d{2}`)
	dateLineRE  = regexp.MustCompile(`(?i)^(Jan(?:uary)?|Feb(?:ruary)?|Mar(?:ch)?|Apr(?:il)?|May|Jun(?:e)?|Jul(?:y)?|Aug(?:ust)?|Sep(?:t(?:ember)?)?|Oct(?:ober)?|Nov(?:ember)?|Dec(?:ember)?)\s+\d{1,2}(?:st|nd|rd|th)?,?\s+\d{4}(?:\s+\d{1,2}:\d{2}\s+[AP]M)?$`)
)

const downloadMarker = "This article was downloaded by "

// Input describes an issue directory before parsing.
type Input struct {
	Path      string
	Publisher string
	IssueDate string
}

// InspectInput detects publisher and issue date without converting the source.
func InspectInput(path string) (Input, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return Input{}, fmt.Errorf("resolve input path: %w", err)
	}
	info, err := os.Stat(abs)
	if err != nil {
		return Input{}, fmt.Errorf("stat input: %w", err)
	}
	if !info.IsDir() {
		return Input{}, errors.New("input must be an issue directory")
	}

	var publisher string
	lower := strings.ToLower(abs)
	switch {
	case strings.Contains(lower, "economist"):
		publisher = "economist"
	case strings.Contains(lower, "wired"):
		publisher = "wired"
	default:
		return Input{}, errors.New("cannot detect publisher from path (expected economist or wired)")
	}

	date := issueDateRE.FindString(abs)
	if date == "" {
		return Input{}, errors.New("cannot detect issue date (expected YYYY.MM.DD in path)")
	}
	return Input{Path: abs, Publisher: publisher, IssueDate: strings.ReplaceAll(date, ".", "-")}, nil
}

// Parse resolves the TXT/EPUB inside an issue directory and extracts every article.
func Parse(input Input) (domain.Issue, error) {
	txtPath, err := resolveText(input.Path)
	if err != nil {
		return domain.Issue{}, err
	}
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
		Publisher: input.Publisher, IssueDate: input.IssueDate,
		SourcePath: input.Path, Articles: articles,
	}, nil
}

func resolveText(path string) (string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return "", fmt.Errorf("read issue directory: %w", err)
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
		return txts[0], nil
	}
	if len(epubs) > 0 {
		return convertEPUB(epubs[0])
	}
	return "", errors.New("issue directory contains neither TXT nor EPUB")
}

func convertEPUB(epub string) (string, error) {
	if _, err := exec.LookPath("ebook-convert"); err != nil {
		return "", errors.New("TXT is missing and ebook-convert is not installed")
	}
	// 持久化为 EPUB 同目录、同 basename 的 .txt，便于查看和后续复用。
	out := strings.TrimSuffix(epub, filepath.Ext(epub)) + ".txt"
	cmd := exec.Command("ebook-convert", epub, out)
	if output, err := cmd.CombinedOutput(); err != nil {
		_ = os.Remove(out) // 清理转换失败留下的半成品
		return "", fmt.Errorf("ebook-convert failed: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return out, nil
}

type marker struct {
	line    int
	section string
	date    string
	slug    string
	url     string
}

func sourceMarkers(lines []string) []marker {
	var markers []marker
	for lineNumber, line := range lines {
		rest, ok := strings.CutPrefix(strings.TrimSpace(line), downloadMarker)
		if !ok {
			continue
		}
		_, sourceURL, ok := strings.Cut(rest, " from ")
		if !ok || strings.TrimSpace(sourceURL) == "" {
			continue
		}
		markers = append(markers, marker{line: lineNumber, url: strings.TrimSpace(sourceURL)})
	}
	return markers
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
