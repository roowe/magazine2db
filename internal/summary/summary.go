package summary

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	einoopenai "github.com/cloudwego/eino-ext/components/model/openai"
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

type Generator interface {
	Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error)
}

// Service calls MyAI over OpenAI Chat Completions, then Ollama Cloud if MyAI fails.
type Service struct {
	primary          Generator
	fallback         Generator
	primaryProvider  string
	fallbackProvider string
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

func New(ctx context.Context, cfg Config) (*Service, error) {
	if cfg.PrimaryBaseURL == "" || cfg.PrimaryAPIKey == "" || cfg.PrimaryModel == "" ||
		cfg.FallbackBaseURL == "" || cfg.FallbackAPIKey == "" || cfg.FallbackModel == "" || cfg.MaxTokens < 1 {
		return nil, errors.New("incomplete summary configuration")
	}
	primaryBaseURL, err := normalizeBaseURL(cfg.PrimaryBaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid MyAI base URL: %w", err)
	}
	fallbackBaseURL, err := normalizeBaseURL(cfg.FallbackBaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid Ollama Cloud base URL: %w", err)
	}
	temperature := float32(0.2)
	httpClient := &http.Client{Timeout: 3 * time.Minute}
	primary, err := einoopenai.NewChatModel(ctx, &einoopenai.ChatModelConfig{
		BaseURL: primaryBaseURL, APIKey: cfg.PrimaryAPIKey, Model: cfg.PrimaryModel,
		MaxTokens: &cfg.MaxTokens, Temperature: &temperature, HTTPClient: httpClient,
	})
	if err != nil {
		return nil, fmt.Errorf("create MyAI Eino OpenAI model: %w", err)
	}
	// Use OpenAI-compatible /v1 on ollama.com with Bearer API key.
	// The native Ollama Go client always signs ollama.com with ~/.ollama/id_ed25519,
	// which fails on servers that only have OLLAMA_API_KEY.
	fallback, err := einoopenai.NewChatModel(ctx, &einoopenai.ChatModelConfig{
		BaseURL: fallbackBaseURL, APIKey: cfg.FallbackAPIKey, Model: cfg.FallbackModel,
		MaxTokens: &cfg.MaxTokens, Temperature: &temperature, HTTPClient: httpClient,
	})
	if err != nil {
		return nil, fmt.Errorf("create Ollama Cloud Eino OpenAI model: %w", err)
	}
	return &Service{
		primary:          primary,
		fallback:         fallback,
		primaryProvider:  "myai/" + cfg.PrimaryModel,
		fallbackProvider: "ollama-cloud/" + cfg.FallbackModel,
	}, nil
}

// Summarize returns the Chinese summary and the provider/model that produced it.
func (s *Service) Summarize(ctx context.Context, article domain.StoredArticle) (string, string, error) {
	messages := []*schema.Message{
		schema.SystemMessage(systemPrompt),
		schema.UserMessage(formatArticle(article)),
	}
	response, primaryErr := s.primary.Generate(ctx, messages)
	if primaryErr == nil {
		return cleanSummary(response.Content), s.primaryProvider, nil
	}
	response, fallbackErr := s.fallback.Generate(ctx, messages)
	if fallbackErr != nil {
		return "", s.fallbackProvider, errors.Join(
			fmt.Errorf("primary provider: %w", primaryErr),
			fmt.Errorf("fallback provider: %w", fallbackErr),
		)
	}
	return cleanSummary(response.Content), s.fallbackProvider, nil
}

func normalizeBaseURL(value string) (string, error) {
	value = strings.TrimRight(strings.TrimSpace(value), "/")
	parsed, err := url.Parse(value)
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("URL must include scheme and host")
	}
	return value, nil
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
