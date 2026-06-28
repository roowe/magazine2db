package summary

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

func TestSensitiveErrorFallsBack(t *testing.T) {
	primary := &fakeGenerator{err: errors.New(`500: {"message":"input new_sensitive (1026)"}`)}
	fallback := &fakeGenerator{content: "这是中文摘要。"}
	service := &Service{primary: primary, fallback: fallback}
	text, provider, err := service.Summarize(context.Background(), domain.StoredArticle{Title: "Title", Body: "Body"})
	if err != nil {
		t.Fatal(err)
	}
	if text != "这是中文摘要。" || provider != "fallback" || fallback.calls != 1 {
		t.Fatalf("unexpected fallback result: %q %q calls=%d", text, provider, fallback.calls)
	}
}

func TestOrdinaryErrorDoesNotFallBack(t *testing.T) {
	primary := &fakeGenerator{err: errors.New("rate limited")}
	fallback := &fakeGenerator{content: "must not be used"}
	service := &Service{primary: primary, fallback: fallback}
	_, _, err := service.Summarize(context.Background(), domain.StoredArticle{Title: "Title", Body: "Body"})
	if err == nil || fallback.calls != 0 {
		t.Fatalf("ordinary error should not fall back: err=%v calls=%d", err, fallback.calls)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestSensitiveTransportDisablesSDKRetryClassification(t *testing.T) {
	transport := sensitiveNoRetryTransport{base: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: 500,
			Status:     "500 Internal Server Error",
			Body:       io.NopCloser(strings.NewReader(`{"error":{"message":"input new_sensitive (1026)"}}`)),
			Header:     make(http.Header),
		}, nil
	})}
	response, err := transport.RoundTrip(&http.Request{})
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", response.StatusCode)
	}
}

func TestEinoAnthropicSensitiveResponseFallsBackImmediately(t *testing.T) {
	var primaryCalls, fallbackCalls atomic.Int32
	primary := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		primaryCalls.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusInternalServerError)
		_, _ = writer.Write([]byte(`{"error":{"message":"input new_sensitive (1026)","type":"api_error"},"type":"error"}`))
	}))
	defer primary.Close()
	fallback := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		fallbackCalls.Add(1)
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{
			"id":"msg_test","type":"message","role":"assistant","model":"minimax-test",
			"content":[{"type":"text","text":"备用服务生成的中文摘要。"}],
			"stop_reason":"end_turn","stop_sequence":null,
			"usage":{"input_tokens":10,"output_tokens":8}
		}`))
	}))
	defer fallback.Close()

	service, err := New(context.Background(), Config{
		PrimaryBaseURL:  primary.URL,
		PrimaryAPIKey:   "test-key",
		PrimaryModel:    "minimax-test",
		FallbackBaseURL: fallback.URL,
		FallbackAPIKey:  "fallback-key",
		FallbackModel:   "minimax-test",
		MaxTokens:       4096,
	})
	if err != nil {
		t.Fatal(err)
	}
	text, provider, err := service.Summarize(context.Background(), domain.StoredArticle{Title: "Title", Body: "Body"})
	if err != nil {
		t.Fatal(err)
	}
	if text != "备用服务生成的中文摘要。" || provider != "fallback" {
		t.Fatalf("unexpected result: %q via %q", text, provider)
	}
	if primaryCalls.Load() != 1 {
		t.Fatalf("sensitive primary request was retried %d times", primaryCalls.Load())
	}
	if fallbackCalls.Load() != 1 {
		t.Fatalf("fallback calls = %d, want 1", fallbackCalls.Load())
	}
}
