// m1-replay is an operator-only, bounded provider-contract diagnostic. It is not
// registered as an agent/tool, included in service images, or a repair workflow.
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
	"regexp"
	"strings"
	"time"

	"github.com/truvaagents/truva-g3/ai"
	_ "github.com/truvaagents/truva-g3/ai/providers/openai"
	"github.com/truvaagents/truva-g3/core"
)

type sample struct {
	Type        string  `json:"type"`
	Prompt      string  `json:"prompt"`
	System      string  `json:"system_prompt"`
	MaxTokens   int     `json:"max_tokens"`
	Temperature float32 `json:"temperature"`
}
type guard struct {
	next  http.RoundTripper
	live  bool
	calls int
	wire  map[string]any
	raw   map[string]any
}

func (g *guard) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.Host != "openrouter.ai" || r.URL.Scheme != "https" || r.URL.Path != "/api/v1/chat/completions" {
		return nil, errors.New("diagnostic destination rejected")
	}
	if g.calls >= 16 {
		return nil, errors.New("diagnostic provider-attempt ceiling reached")
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 2<<20))
	if err != nil {
		return nil, err
	}
	_ = r.Body.Close()
	var wire map[string]any
	if err = json.Unmarshal(body, &wire); err != nil {
		return nil, err
	}
	provider, _ := wire["provider"].(map[string]any)
	reasoning, _ := wire["reasoning"].(map[string]any)
	_, sampling := wire["temperature"]
	if wire["model"] != "openai/gpt-6-luna" || sampling || reasoning["effort"] != "none" || provider["zdr"] != true || provider["data_collection"] != "deny" || provider["require_parameters"] != true {
		return nil, errors.New("diagnostic effective policy rejected")
	}
	g.wire = map[string]any{"model": wire["model"], "reasoning": reasoning, "provider": provider, "max_tokens": wire["max_tokens"], "max_completion_tokens": wire["max_completion_tokens"], "temperature_present": sampling}
	if !g.live {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"choices":[{"message":{"content":"offline capture"},"finish_reason":"stop"}]}`)), Request: r}, nil
	}
	g.calls++
	r.Body = io.NopCloser(bytes.NewReader(body))
	response, err := g.next.RoundTrip(r)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	_ = response.Body.Close()
	if err != nil {
		return nil, err
	}
	response.Body = io.NopCloser(bytes.NewReader(data))
	var raw map[string]any
	if json.Unmarshal(data, &raw) == nil {
		g.raw = raw
	}
	return response, nil
}

func run() error {
	live := flag.Bool("live", false, "Send the eight reviewed samples; default captures offline")
	approved := flag.String("approved-fixture-sha256", "", "Operator-reviewed admission SHA-256 from offline capture; required for live")
	source := flag.String("fixture-source", "", "Credential-free source execution/export identifier; required for live")
	flag.Parse()
	inputBytes, digest, err := admitReplayInput(os.Stdin, *source, *approved, *live)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(os.Stdout).Encode(map[string]any{
		"type": "fixture_admission", "version": 1, "fixture_source": *source,
		"admission_sha256": digest, "live": *live, "samples": 8, "provider_attempt_ceiling": 16,
		"review_required": "Operator reviews and sanitizes the exact export before approving this digest; marker checks are not secret detection.",
	}); err != nil {
		return err
	}
	var input struct {
		Interactions []sample `json:"interactions"`
	}
	if err := json.Unmarshal(inputBytes, &input); err != nil {
		return err
	}
	var samples []sample
	for _, kind := range []string{"skill_activation_selection", "skill_resource_selection", "activity_compaction", "event_summarization"} {
		var found []sample
		for _, s := range input.Interactions {
			if s.Type == kind {
				found = append(found, s)
			}
		}
		if len(found) == 0 {
			return fmt.Errorf("missing replay category %s", kind)
		}
		// Repeated inputs are explicitly labelled, not represented as distinct runs.
		samples = append(samples, found[0], found[len(found)-1])
	}
	for _, s := range samples {
		if s.MaxTokens <= 0 || s.MaxTokens > 10000 || len(s.Prompt)+len(s.System) > 200000 {
			return errors.New("sample exceeds reviewed bounds")
		}
		// Application-owned diagnostic admission check, not framework redaction.
		for _, marker := range []string{"sk-or-v1-", "github_pat_", "Bearer ", "ghp_"} {
			if strings.Contains(s.Prompt+s.System, marker) {
				return errors.New("sample requires credential review")
			}
		}
	}
	if *live {
		if os.Getenv("KUBERNETES_SERVICE_HOST") == "" {
			return errors.New("live diagnostic must run in Kind pod")
		}
		if os.Getenv("OPENROUTER_API_KEY") == "" {
			return errors.New("OpenRouter credential missing")
		}
		// Prevent accidental reruns in this pod. Removing this marker is a separate
		// operator decision, not an automatic retry path.
		f, err := os.OpenFile("/tmp/truvag3-m1-replay-20260929.lock", os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return fmt.Errorf("exclusive replay marker: %w", err)
		}
		_ = f.Close()
	} else {
		if err := os.Setenv("OPENROUTER_API_KEY", "offline-diagnostic"); err != nil {
			return err
		}
	}
	if err := os.Setenv("OPENROUTER_BASE_URL", "https://openrouter.ai/api/v1"); err != nil {
		return err
	}
	if err := os.Setenv("TRUVAG3_OPENROUTER_MODEL_FAST", "openai/gpt-6-luna"); err != nil {
		return err
	}
	original := http.DefaultTransport
	g := &guard{next: original, live: *live}
	// Single-purpose serial diagnostic process; no application transport changed.
	http.DefaultTransport = g
	defer func() { http.DefaultTransport = original }()
	chain, err := ai.NewChainClient(ai.WithProviderChain("openai.openrouter"), ai.WithChainReasoningEffort("none"), ai.WithChainMaxRetries(0), ai.WithChainTimeout(180*time.Second))
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	for index, s := range samples {
		timeout := 180 * time.Second
		if s.Type == "skill_resource_selection" {
			timeout = 30 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		options := &core.AIOptions{Model: "fast", Temperature: s.Temperature, MaxTokens: s.MaxTokens, SystemPrompt: s.System}
		purpose := s.Type
		if purpose == "activity_compaction" || purpose == "event_summarization" {
			purpose = strings.ReplaceAll(purpose, "_", "-")
		}
		request := core.NewAIRequestFromLegacy(s.Prompt, purpose, options)
		start := time.Now()
		g.raw = nil
		g.wire = nil
		result, callErr := chain.Generate(ctx, request)
		cancel()
		output := map[string]any{"sample": index + 1, "type": s.Type, "live": *live, "duration_ms": time.Since(start).Milliseconds(), "attempts_total": g.calls, "wire": g.wire, "result": result, "routed_provider": g.raw["provider"], "raw_usage": g.raw["usage"]}
		if choices, ok := g.raw["choices"].([]any); ok && len(choices) > 0 {
			choice, _ := choices[0].(map[string]any)
			message, _ := choice["message"].(map[string]any)
			content, _ := message["content"].(string)
			output["finish_reason"] = choice["finish_reason"]
			output["raw_content_bytes"] = len(content)
			output["content_fidelity"] = result != nil && result.Response != nil && result.Response.Content == content
		}
		if callErr != nil {
			output["error"] = callErr.Error()
		}
		if err := encoder.Encode(output); err != nil {
			return err
		}
		fmt.Fprintf(os.Stderr, "sample %d/8 type=%s completed; attempts=%d\n", index+1, s.Type, g.calls)
		if callErr != nil {
			return errors.New("replay stopped on provider/preparation failure; no automatic retry")
		}
	}
	return nil
}

// Admission binds approval to the complete input bytes and source identifier,
// before credentials, transport construction or the one-run marker are touched.
// It is application-owned review, not a framework sanitizer or secret detector.
func admitReplayInput(reader io.Reader, source, approved string, live bool) ([]byte, string, error) {
	const maximumInputBytes = 4 << 20
	input, err := io.ReadAll(io.LimitReader(reader, maximumInputBytes+1))
	if err != nil {
		return nil, "", errors.New("cannot read replay fixture")
	}
	if len(input) > maximumInputBytes || !json.Valid(input) {
		return nil, "", errors.New("replay fixture must be one valid JSON document within 4 MiB")
	}
	if source != "" && !regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`).MatchString(source) {
		return nil, "", errors.New("fixture source must be a credential-free execution/export identifier")
	}
	if live && source == "" {
		return nil, "", errors.New("live replay requires a reviewed fixture source")
	}
	digest := fmt.Sprintf("%x", sha256.Sum256(append([]byte("m1-reviewed-fixture-v1\x00"+source+"\x00"), input...)))
	if live && approved != digest {
		return nil, "", errors.New("live replay requires the matching operator-reviewed fixture SHA-256 from offline capture")
	}
	return input, digest, nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
