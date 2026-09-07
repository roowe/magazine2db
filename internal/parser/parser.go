package parser

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"magazine2db/internal/domain"
)

var issueDateRE = regexp.MustCompile(`\d{4}\.\d{2}\.\d{2}`)

// Input describes an issue directory before parsing.
type Input struct {
	Path      string
	Publisher string
	IssueDate string
}

// InspectInput detects publisher and issue date from the directory path.
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

// Parse reads the EPUB inside an issue directory.
func Parse(input Input) (domain.Issue, error) {
	sourcePath, err := resolveSource(input.Path)
	if err != nil {
		return domain.Issue{}, err
	}
	return parseEPUB(input, sourcePath)
}

func resolveSource(path string) (string, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return "", fmt.Errorf("read issue directory: %w", err)
	}
	// ReadDir returns entries sorted by filename.
	for _, entry := range entries {
		if !entry.IsDir() && strings.EqualFold(filepath.Ext(entry.Name()), ".epub") {
			return filepath.Join(path, entry.Name()), nil
		}
	}
	return "", errors.New("issue directory contains no EPUB; TXT import is not supported")
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
