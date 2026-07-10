package summary

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/cloudwego/eino/components/model"
	"github.com/cloudwego/eino/schema"

	"magazine2db/internal/domain"
)

type fakeGenerator struct {
	content string
	err     error
	calls   int
}

func (f *fakeGenerator) Generate(context.Context, []*schema.Message, ...model.Option) (*schema.Message, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return schema.AssistantMessage(f.content, nil), nil
}

func TestAnyPrimaryErrorFallsBack(t *testing.T) {
	for _, message := range []string{
		`500: {"message":"request rejected"}`,
		"rate limited",
		"unknown provider error",
	} {
		t.Run(message, func(t *testing.T) {
			primary := &fakeGenerator{err: errors.New(message)}
			fallback := &fakeGenerator{content: "这是中文摘要。"}
			service := &Service{
				primary: primary, fallback: fallback,
				primaryProvider: "myai/test", fallbackProvider: "ollama-cloud/test",
			}
			text, provider, err := service.Summarize(context.Background(), domain.StoredArticle{Title: "Title", Body: "Body"})
			if err != nil {
				t.Fatal(err)
			}
			if text != "这是中文摘要。" || provider != "ollama-cloud/test" || fallback.calls != 1 {
				t.Fatalf("unexpected fallback result: %q %q calls=%d", text, provider, fallback.calls)
			}
		})
	}
}

func TestProviderProtocolsAndErrorFallback(t *testing.T) {
	var primaryCalls, fallbackCalls atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		primaryCalls.Add(1)
		if request.URL.Path != "/v1/chat/completions" {
			t.Errorf("MyAI path = %q", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer myai-key" {
			t.Errorf("MyAI authorization = %q", request.Header.Get("Authorization"))
		}
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content any    `json:"content"`
			} `json:"messages"`
			MaxTokens int `json:"max_tokens"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != "deepseek-v4-pro" || body.MaxTokens != 4096 || len(body.Messages) != 2 {
			t.Errorf("unexpected MyAI request: %+v", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = writer.Write([]byte(`{"error":{"message":"upstream unavailable"}}`))
	}))
	defer primary.Close()

	fallback := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fallbackCalls.Add(1)
		if request.URL.Path != "/api/chat" {
			t.Errorf("Ollama path = %q", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer ollama-key" {
			t.Errorf("Ollama authorization = %q", request.Header.Get("Authorization"))
		}
		var body struct {
			Model   string `json:"model"`
			Stream  bool   `json:"stream"`
			Options struct {
				NumPredict int `json:"num_predict"`
			} `json:"options"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		if body.Model != "gemma4" || body.Stream || body.Options.NumPredict != 4096 {
			t.Errorf("unexpected Ollama request: %+v", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"model":"gemma4","message":{"role":"assistant","content":"备用服务生成的中文摘要。"},"done":true}`))
	}))
	defer fallback.Close()

	service, err := New(context.Background(), Config{
		PrimaryBaseURL:  primary.URL + "/v1",
		PrimaryAPIKey:   "myai-key",
		PrimaryModel:    "deepseek-v4-pro",
		FallbackBaseURL: fallback.URL,
		FallbackAPIKey:  "ollama-key",
		FallbackModel:   "gemma4",
		MaxTokens:       4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	text, provider, err := service.Summarize(context.Background(), domain.StoredArticle{Title: "Title", Body: "Body"})
	if err != nil {
		t.Fatal(err)
	}
	if text != "备用服务生成的中文摘要。" || provider != "ollama-cloud/gemma4" {
		t.Fatalf("unexpected result: %q via %q", text, provider)
	}
	if primaryCalls.Load() != 1 || fallbackCalls.Load() != 1 {
		t.Fatalf("calls: primary=%d fallback=%d", primaryCalls.Load(), fallbackCalls.Load())
	}
}

func TestMyAISuccessDoesNotUseFallback(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"主服务摘要。"}}]}`))
	}))
	defer primary.Close()
	service, err := New(context.Background(), Config{
		PrimaryBaseURL: primary.URL, PrimaryAPIKey: "key", PrimaryModel: "model",
		FallbackBaseURL: "https://ollama.com", FallbackAPIKey: "key", FallbackModel: "model",
		MaxTokens: 100,
	})
	if err != nil {
		t.Fatal(err)
	}
	text, provider, err := service.Summarize(context.Background(), domain.StoredArticle{Title: "Title", Body: "Body"})
	if err != nil {
		t.Fatal(err)
	}
	if text != "主服务摘要。" || provider != "myai/model" {
		t.Fatalf("unexpected result: %q via %q", text, provider)
	}
}
