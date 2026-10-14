package main

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/truvaagents/truva-g3/core"
)

type offlineTransport struct{ calls int }

func TestFixtureHTTPPreflight(t *testing.T) {
	if err := verifyTemplateInclusions(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func (transport *offlineTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	transport.calls++
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`)), Request: request}, nil
}

// This example test protects the paid-call safety boundary, not an application
// business rule. Its transport cannot make any real network request.
func TestDiagnosticChainActuallyUsesGuard(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "synthetic-diagnostic-key")
	t.Setenv("OPENROUTER_BASE_URL", "https://openrouter.ai/api/v1")
	t.Setenv("TRUVAG3_OPENROUTER_MODEL_FAST", "openai/gpt-6-luna")
	t.Setenv("TRUVAG3_OPENROUTER_MODEL_DEFAULT", "deepseek/deepseek-v4.1-flash")
	next := &offlineTransport{}
	guard := &guardedTransport{next: next}
	chain, err := diagnosticChain(guard, &core.NoOpLogger{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, model := range []string{"fast", "fast", "default", "default"} {
		if _, err := chain.GenerateResponse(t.Context(), "offline fixture", &core.AIOptions{Model: model, MaxTokens: 3000}); err != nil {
			t.Fatal(err)
		}
	}
	if guard.calls != 4 || next.calls != 4 || len(guard.wire) != 4 {
		t.Fatalf("guard bypassed: guard=%d transport=%d", guard.calls, next.calls)
	}
	if _, err := chain.GenerateResponse(t.Context(), "blocked fifth call", &core.AIOptions{Model: "fast", MaxTokens: 3000}); err == nil {
		t.Fatal("fifth call was not rejected")
	}
	if guard.calls != 4 || next.calls != 4 {
		t.Fatal("fifth call reached underlying transport")
	}
}
