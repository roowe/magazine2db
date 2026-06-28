package summary

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/cloudwego/eino-ext/components/model/claude"
	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"magazine2db/internal/domain"
)

const systemPrompt = `你是阅读助手。请根据文章原文生成一段的中文摘要。
要求：
1、不超过300 字；
2、准确覆盖文章主题、关键事实和核心结论；
3、不要添加原文没有的信息；
4、不要输出标题、列表、标签、投资建议或任何前后缀，`

// Generator is the Eino ChatModel surface used by the summarizer.
type Generator interface {
	Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error)
}

// Service calls a primary Anthropic-compatible provider and a sensitive-content fallback.
type Service struct {
	primary  Generator
	fallback Generator
}

type Config struct {
	PrimaryBaseURL  string
	PrimaryAPIKey   string
	PrimaryModel    string
	FallbackBaseURL string
	FallbackAPIKey  string
	FallbackModel   string
	MaxTokens       int
}

// New builds both MiniMax Anthropic-protocol providers.
func New(ctx context.Context, cfg Config) (*Service, error) {
	if cfg.PrimaryBaseURL == "" || cfg.PrimaryAPIKey == "" || cfg.PrimaryModel == "" ||
		cfg.FallbackBaseURL == "" || cfg.FallbackAPIKey == "" || cfg.FallbackModel == "" || cfg.MaxTokens < 1 {
		return nil, errors.New("incomplete summary configuration")
	}

	temperature := float32(0.2)
	primary, err := claude.NewChatModel(ctx, &claude.Config{
		BaseURL: &cfg.PrimaryBaseURL, APIKey: cfg.PrimaryAPIKey, Model: cfg.PrimaryModel,
		MaxTokens: cfg.MaxTokens, Temperature: &temperature,
		HTTPClient: &http.Client{
			Transport: sensitiveNoRetryTransport{base: http.DefaultTransport},
			Timeout:   3 * time.Minute,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("create primary Eino Claude model: %w", err)
	}
	fallback, err := claude.NewChatModel(ctx, &claude.Config{
		BaseURL: &cfg.FallbackBaseURL, APIKey: cfg.FallbackAPIKey, Model: cfg.FallbackModel,
		MaxTokens: cfg.MaxTokens, Temperature: &temperature,
		HTTPClient: &http.Client{Timeout: 3 * time.Minute},
	})
	if err != nil {
		return nil, fmt.Errorf("create fallback Eino Claude model: %w", err)
	}
	return &Service{primary: primary, fallback: fallback}, nil
}

// Summarize returns the Chinese summary and the provider that produced it.
func (s *Service) Summarize(ctx context.Context, article domain.StoredArticle) (string, string, error) {
	messages := []*schema.Message{
		schema.SystemMessage(systemPrompt),
		schema.UserMessage(formatArticle(article)),
	}
	response, err := s.primary.Generate(ctx, messages)
	if err == nil {
		return cleanSummary(response.Content), "primary", nil
	}
	if !IsSensitiveError(err) {
		return "", "primary", fmt.Errorf("primary provider: %w", err)
	}
	response, err = s.fallback.Generate(ctx, messages)
	if err != nil {
		return "", "fallback", fmt.Errorf("fallback provider after sensitive-content rejection: %w", err)
	}
	return cleanSummary(response.Content), "fallback", nil
}

// IsSensitiveError recognizes MiniMax sensitive responses that must not be retried.
func IsSensitiveError(err error) bool {
	if err == nil {
		return false
	}
	return isSensitiveText(err.Error())
}

func isSensitiveText(value string) bool {
	value = strings.ToLower(value)
	inputRejected := strings.Contains(value, "input new_sensitive") && strings.Contains(value, "1026")
	outputRejected := strings.Contains(value, "output new_sensitive") && strings.Contains(value, "1027")
	return inputRejected || outputRejected
}

func formatArticle(article domain.StoredArticle) string {
	var builder strings.Builder
	builder.WriteString("标题：")
	builder.WriteString(article.Title)
	builder.WriteString("\n")
	if article.Description != "" {
		builder.WriteString("副标题：")
		builder.WriteString(article.Description)
		builder.WriteString("\n")
	}
	builder.WriteString("栏目：")
	builder.WriteString(article.Section)
	builder.WriteString("\n\n原文：\n")
	builder.WriteString(article.Body)
	return builder.String()
}

func cleanSummary(value string) string {
	value = strings.TrimSpace(value)
	value = strings.TrimPrefix(value, "```text")
	value = strings.TrimPrefix(value, "```markdown")
	value = strings.TrimPrefix(value, "```")
	value = strings.TrimSuffix(value, "```")
	return strings.TrimSpace(value)
}

type sensitiveNoRetryTransport struct {
	base http.RoundTripper
}

func (t sensitiveNoRetryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	response, err := t.base.RoundTrip(request)
	if err != nil || response == nil || response.StatusCode < 500 || response.Body == nil {
		return response, err
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	response.Body = io.NopCloser(bytes.NewReader(body))
	if isSensitiveText(string(body)) {
		// Anthropic SDK retries 5xx by default. Reclassifying this known deterministic
		// rejection as 400 makes it return immediately so the fallback can run.
		response.StatusCode = http.StatusBadRequest
		response.Status = "400 Bad Request"
	}
	return response, nil
}
