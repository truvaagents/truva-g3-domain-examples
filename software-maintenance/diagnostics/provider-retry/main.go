// provider-retry is an operator-only diagnostic, never registered or included
// in agent images. It injects one labelled response before each real request.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/truvaagents/truva-g3/ai"
	_ "github.com/truvaagents/truva-g3/ai/providers/openai"
	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/telemetry"
)

type fixture struct{ Name, Model, Prompt, Failure string }

var fixtures = []fixture{
	{"selector_shape", "openai/gpt-6-luna", `Return exactly this JSON object: {"selected_skills":[]}`, `{"error":{"code":429,"metadata":{"error_type":"rate_limit_exceeded"}}}`},
	{"synthesis_shape", "deepseek/deepseek-v4.1-flash", `Return exactly this JSON object: {"status":"inconclusive","summary":"Synthetic diagnostic only."}`, `{"choices":[{"message":{"content":"discard this failed attempt"},"error":{"code":502,"metadata":{"error_type":"provider_unavailable"}}}],"usage":{"total_tokens":7}}`},
}

type guardedTransport struct {
	live        bool
	paid, calls int
	current     fixture
	digest      string
}

func (g *guardedTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	defer func() { _ = r.Body.Close() }()
	if r.URL.Scheme != "https" || r.URL.Host != "openrouter.ai" || r.URL.Path != "/api/v1/chat/completions" || g.calls >= 2 || g.paid >= 2 {
		return nil, errors.New("diagnostic route/attempt bound rejected")
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		return nil, err
	}
	var wire map[string]any
	if json.Unmarshal(data, &wire) != nil {
		return nil, errors.New("invalid diagnostic wire")
	}
	provider, _ := wire["provider"].(map[string]any)
	if wire["model"] != g.current.Model || provider["zdr"] != true || provider["data_collection"] != "deny" {
		return nil, errors.New("diagnostic privacy/model contract rejected")
	}
	limit, _ := wire["max_tokens"].(float64)
	if limit == 0 {
		limit, _ = wire["max_completion_tokens"].(float64)
	}
	if limit <= 0 || limit > 1024 {
		return nil, errors.New("diagnostic token bound rejected")
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	if g.calls == 0 {
		g.digest = digest
	} else if digest != g.digest {
		return nil, errors.New("retry altered prepared semantics")
	}
	g.calls++
	if g.calls == 1 {
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(bytes.NewBufferString(g.current.Failure)), Request: r}, nil
	}
	if !g.live {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(bytes.NewBufferString(`{"choices":[{"message":{"content":"offline"}}]}`)), Request: r}, nil
	}
	g.paid++
	r.Body = io.NopCloser(bytes.NewReader(data))
	return http.DefaultTransport.RoundTrip(r)
}

func run() error {
	live := flag.Bool("live", false, "Run once inside Kind; at most two paid requests and two injected responses")
	approved := flag.String("approved-fixture-sha256", "", "Exact synthetic fixture digest printed by offline run")
	flag.Parse()
	data, _ := json.Marshal(fixtures)
	digest := fmt.Sprintf("%x", sha256.Sum256(data))
	if *live {
		if *approved != digest || os.Getenv("KUBERNETES_SERVICE_HOST") == "" || os.Getenv("OPENROUTER_API_KEY") == "" {
			return errors.New("live requires Kind, OpenRouter key and approved fixture digest")
		}
		lock, err := os.OpenFile("/tmp/truvag3-a2-live-20260930.lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_ = lock.Close()
	}
	fmt.Printf("{\"fixture_sha256\":%q,\"live\":%t,\"paid_request_ceiling\":2}\n", digest, *live)
	cfg := telemetry.UseProfile(telemetry.ProfileDevelopment)
	cfg.ServiceName = "provider-retry-diagnostic"
	if endpoint := os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"); endpoint != "" {
		cfg.Endpoint = endpoint
	}
	if *live {
		if err := telemetry.Initialize(cfg); err != nil {
			return err
		}
		telemetry.EnableFrameworkIntegration(nil)
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = telemetry.Shutdown(ctx)
		}()
	}
	g := &guardedTransport{live: *live}
	defer func() { fmt.Printf("{\"paid_requests_used\":%d}\n", g.paid) }()
	key := "offline"
	if *live {
		key = os.Getenv("OPENROUTER_API_KEY")
	}
	client, err := ai.NewRequestClient(ai.WithProviderAlias("openai.openrouter"), ai.WithAPIKey(key), ai.WithMaxRetries(1), ai.WithReasoningEffort("none"), ai.WithTimeout(60*time.Second), ai.WithHTTPClient(&http.Client{Transport: g}), ai.WithTelemetry(telemetry.GetTelemetryProvider()))
	if err != nil {
		return err
	}
	for _, f := range fixtures {
		g.current, g.calls, g.digest = f, 0, ""
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		ctx = telemetry.WithBaggage(ctx, "request_id", "a2-diagnostic-"+f.Name)
		result, err := client.Generate(ctx, core.NewAIRequestFromLegacy(f.Prompt, "diagnostic_provider_retry", &core.AIOptions{Model: f.Model, MaxTokens: 128, ResponseFormat: "json"}))
		cancel()
		if err != nil {
			return err
		}
		if result == nil || result.Response == nil || result.RequestReport == nil || g.calls != 2 {
			return errors.New("missing successful result/report or unexpected attempts")
		}
		if *live {
			var output map[string]json.RawMessage
			if json.Unmarshal([]byte(result.Response.Content), &output) != nil {
				return errors.New("live result is not JSON")
			}
			if f.Name == "selector_shape" {
				var selected []any
				if json.Unmarshal(output["selected_skills"], &selected) != nil || len(selected) != 0 {
					return errors.New("selector shape mismatch")
				}
			} else {
				var status string
				if json.Unmarshal(output["status"], &status) != nil || status != "inconclusive" {
					return errors.New("synthesis shape mismatch")
				}
			}
		}
		_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"fixture": f.Name, "model": f.Model, "wire_attempts": g.calls, "injected_attempts": 1, "prepared_body_sha256": g.digest, "usage": result.Response.Usage, "result": "passed", "acceptance": "provider boundary only, not an agent workflow"})
	}
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
