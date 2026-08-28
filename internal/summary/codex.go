package summary

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"magazine2db/internal/domain"
)

const (
	preflightTimeout = 30 * time.Second
	maxErrorChars    = 500
)

var runSequence atomic.Uint64

type Config struct {
	CodexBin string
	WorkDir  string
	Timeout  time.Duration
}

type Service struct {
	codexBin string
	version  string
	timeout  time.Duration
	runsRoot string
}

type modelCatalog struct {
	Models []struct {
		Slug                     string `json:"slug"`
		SupportedReasoningLevels []struct {
			Effort string `json:"effort"`
		} `json:"supported_reasoning_levels"`
	} `json:"models"`
}

type articleMetadata struct {
	ID          int64  `json:"id"`
	StableID    string `json:"stable_id"`
	Publisher   string `json:"publisher"`
	IssueDate   string `json:"issue_date"`
	Title       string `json:"title"`
	Description string `json:"description"`
	Author      string `json:"author"`
	Section     string `json:"section"`
	PublishedAt string `json:"published_at"`
	SourceURL   string `json:"source_url"`
}

type runRecord struct {
	SchemaVersion int    `json:"schema_version"`
	Runner        string `json:"runner"`
	RunnerVersion string `json:"runner_version"`
	Model         string `json:"model"`
	Reasoning     string `json:"reasoning"`
	StartedAt     int64  `json:"started_at"`
	DurationMS    int64  `json:"duration_ms"`
	ExitCode      *int   `json:"exit_code"`
	Status        string `json:"status"`
	Error         string `json:"error,omitempty"`
}

func New(ctx context.Context, cfg Config) (*Service, error) {
	if cfg.CodexBin == "" || cfg.WorkDir == "" || cfg.Timeout <= 0 {
		return nil, fmt.Errorf("incomplete Codex summary configuration")
	}
	codexBin, err := exec.LookPath(cfg.CodexBin)
	if err != nil {
		return nil, fmt.Errorf("locate Codex CLI %q: %w", cfg.CodexBin, err)
	}
	workDir, err := filepath.Abs(cfg.WorkDir)
	if err != nil {
		return nil, fmt.Errorf("resolve summary work directory: %w", err)
	}
	versionOutput, err := capture(ctx, codexBin, workDir, "--version")
	if err != nil {
		return nil, err
	}
	version := strings.TrimSpace(string(versionOutput))
	if version == "" {
		return nil, fmt.Errorf("codex --version returned empty output")
	}
	modelsOutput, err := capture(ctx, codexBin, workDir, "debug", "models")
	if err != nil {
		return nil, err
	}
	var catalog modelCatalog
	if err := json.Unmarshal(modelsOutput, &catalog); err != nil {
		return nil, fmt.Errorf("parse codex debug models output: %w", err)
	}
	if err := requireModel(catalog); err != nil {
		return nil, err
	}
	runsRoot := filepath.Join(workDir, ".agent-runs", "summary")
	if err := os.MkdirAll(runsRoot, 0o700); err != nil {
		return nil, fmt.Errorf("create summary runs directory %s: %w", runsRoot, err)
	}
	return &Service{
		codexBin: codexBin,
		version:  version,
		timeout:  cfg.Timeout,
		runsRoot: runsRoot,
	}, nil
}

func (s *Service) Summarize(ctx context.Context, article domain.StoredArticle) (Output, error) {
	if strings.TrimSpace(article.Body) == "" {
		return Output{}, fmt.Errorf("article %d has empty body", article.ID)
	}
	runDir, err := s.createRunDir(article.StableID)
	if err != nil {
		return Output{}, err
	}
	metadata := articleMetadata{
		ID: article.ID, StableID: article.StableID, Publisher: article.Publisher,
		IssueDate: article.IssueDate, Title: article.Title, Description: article.Description,
		Author: article.Author, Section: article.Section, PublishedAt: article.PublishedAt,
		SourceURL: article.SourceURL,
	}
	if err := writeJSON(filepath.Join(runDir, "metadata.json"), metadata); err != nil {
		return Output{}, fmt.Errorf("write summary metadata: %w", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "article.txt"), []byte(article.Body), 0o600); err != nil {
		return Output{}, fmt.Errorf("write summary article: %w", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "prompt.md"), []byte(agentPrompt+"\n"), 0o600); err != nil {
		return Output{}, fmt.Errorf("write summary prompt: %w", err)
	}
	schemaPath := filepath.Join(runDir, "output-schema.json")
	if err := os.WriteFile(schemaPath, []byte(outputSchema), 0o600); err != nil {
		return Output{}, fmt.Errorf("write summary output schema: %w", err)
	}
	finalPath := filepath.Join(runDir, "final.json")
	args := commandArgs(runDir, schemaPath, finalPath)
	manifest := make([]string, 0, len(args)+2)
	manifest = append(manifest, s.codexBin)
	manifest = append(manifest, args...)
	manifest = append(manifest, "<PROMPT>")
	if err := writeJSON(filepath.Join(runDir, "command.json"), manifest); err != nil {
		return Output{}, fmt.Errorf("write summary command manifest: %w", err)
	}

	startedAt := time.Now()
	runCtx, cancel := context.WithTimeout(ctx, s.timeout)
	defer cancel()
	command := exec.CommandContext(runCtx, s.codexBin, append(args, agentPrompt)...)
	command.Dir = runDir
	command.Env = append(os.Environ(), "NO_COLOR=1", "TZ=Asia/Shanghai")
	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	runErr := command.Run()
	duration := time.Since(startedAt)
	if err := os.WriteFile(filepath.Join(runDir, "stdout.jsonl"), stdout.Bytes(), 0o600); err != nil {
		return Output{}, fmt.Errorf("write Codex stdout log: %w", err)
	}
	if err := os.WriteFile(filepath.Join(runDir, "stderr.log"), stderr.Bytes(), 0o600); err != nil {
		return Output{}, fmt.Errorf("write Codex stderr log: %w", err)
	}
	record := runRecord{
		SchemaVersion: 1,
		Runner:        "codex",
		RunnerVersion: s.version,
		Model:         Model,
		Reasoning:     Reasoning,
		StartedAt:     startedAt.Unix(),
		DurationMS:    duration.Milliseconds(),
		ExitCode:      processExitCode(command.ProcessState),
	}
	switch runCtx.Err() {
	case context.DeadlineExceeded:
		return Output{}, s.failRun(runDir, record, fmt.Sprintf("Codex timed out after %s", s.timeout))
	case context.Canceled:
		return Output{}, s.failRun(runDir, record, "Codex run canceled")
	}
	if runErr != nil {
		detail := failureDetail(stdout.String(), stderr.String())
		return Output{}, s.failRun(runDir, record, fmt.Sprintf("Codex failed: %v: %s", runErr, detail))
	}
	finalMessage, err := os.ReadFile(finalPath)
	if err != nil {
		return Output{}, s.failRun(runDir, record, fmt.Sprintf("read Codex final output: %v", err))
	}
	text, err := parseSummary(finalMessage)
	if err != nil {
		return Output{}, s.failRun(runDir, record, fmt.Sprintf("invalid Codex summary output: %v", err))
	}
	record.Status = "succeeded"
	if err := writeJSON(filepath.Join(runDir, "run.json"), record); err != nil {
		return Output{}, fmt.Errorf("write successful Codex run record: %w", err)
	}
	return Output{Text: text, Provider: ProviderLabel, RunDir: runDir}, nil
}

func (s *Service) Smoke(ctx context.Context) (Output, error) {
	return s.Summarize(ctx, domain.StoredArticle{
		StableID: "smoke",
		Title:    "Codex 摘要连通性测试",
		Body:     "这是用于验证 Codex CLI 认证、模型调用和结构化输出是否正常的合成文章。文章没有其他事实。",
	})
}

func (s *Service) Version() string {
	return s.version
}

func capture(ctx context.Context, codexBin, workDir string, args ...string) ([]byte, error) {
	commandCtx, cancel := context.WithTimeout(ctx, preflightTimeout)
	defer cancel()
	command := exec.CommandContext(commandCtx, codexBin, args...)
	command.Dir = workDir
	command.Env = append(os.Environ(), "NO_COLOR=1", "TZ=Asia/Shanghai")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	switch commandCtx.Err() {
	case context.DeadlineExceeded:
		return nil, fmt.Errorf("codex %s timed out after %s", strings.Join(args, " "), preflightTimeout)
	case context.Canceled:
		return nil, fmt.Errorf("codex %s canceled", strings.Join(args, " "))
	}
	if err != nil {
		return nil, fmt.Errorf("codex %s failed: %w: %s", strings.Join(args, " "), err, failureDetail(string(output), stderr.String()))
	}
	return output, nil
}

func requireModel(catalog modelCatalog) error {
	for _, model := range catalog.Models {
		if model.Slug != Model {
			continue
		}
		for _, level := range model.SupportedReasoningLevels {
			if level.Effort == Reasoning {
				return nil
			}
		}
		return fmt.Errorf("Codex model %s does not support %s reasoning", Model, Reasoning)
	}
	return fmt.Errorf("Codex model %s is unavailable", Model)
}

func commandArgs(runDir, schemaPath, finalPath string) []string {
	return []string{
		"exec",
		"--ephemeral",
		"--sandbox", "read-only",
		"--skip-git-repo-check",
		"--color", "never",
		"-C", runDir,
		"--model", Model,
		"-c", fmt.Sprintf("model_reasoning_effort=\"%s\"", Reasoning),
		"--output-schema", schemaPath,
		"--json",
		"-o", finalPath,
	}
}

func (s *Service) createRunDir(stableID string) (string, error) {
	label := sanitizeRunID(stableID)
	sequence := runSequence.Add(1)
	runDir := filepath.Join(s.runsRoot, fmt.Sprintf("%d-%d-%d-%s", time.Now().UnixNano(), os.Getpid(), sequence, label))
	if err := os.Mkdir(runDir, 0o700); err != nil {
		return "", fmt.Errorf("create Codex run directory %s: %w", runDir, err)
	}
	return runDir, nil
}

func (s *Service) failRun(runDir string, record runRecord, message string) error {
	record.Status = "failed"
	record.Error = message
	if err := writeJSON(filepath.Join(runDir, "run.json"), record); err != nil {
		return fmt.Errorf("%s; write failed Codex run record: %w; artifacts: %s", message, err, runDir)
	}
	return fmt.Errorf("%s; artifacts: %s", message, runDir)
}

func sanitizeRunID(value string) string {
	var builder strings.Builder
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '-' || character == '_' {
			builder.WriteRune(character)
		} else {
			builder.WriteByte('-')
		}
		if builder.Len() == 80 {
			break
		}
	}
	if builder.Len() == 0 {
		return "run"
	}
	return builder.String()
}

func failureDetail(stdout, stderr string) string {
	detail := strings.TrimSpace(stderr)
	if detail == "" {
		detail = strings.TrimSpace(stdout)
	}
	if detail == "" {
		return "no output"
	}
	runes := []rune(detail)
	if len(runes) > maxErrorChars {
		runes = runes[:maxErrorChars]
	}
	return string(runes)
}

func processExitCode(state *os.ProcessState) *int {
	if state == nil {
		return nil
	}
	code := state.ExitCode()
	return &code
}

func writeJSON(path string, value any) error {
	var data bytes.Buffer
	encoder := json.NewEncoder(&data)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(value); err != nil {
		return fmt.Errorf("encode %s: %w", path, err)
	}
	if err := os.WriteFile(path, data.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
