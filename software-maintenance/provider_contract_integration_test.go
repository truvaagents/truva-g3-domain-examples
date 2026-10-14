//go:build integration

package maintenance_test

import (
	"encoding/json"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/truvaagents/truva-g3/ai"
	_ "github.com/truvaagents/truva-g3/ai/providers/openai"
	"github.com/truvaagents/truva-g3/core"
)

// This bounded probe deliberately isolates the configured fallback provider.
// It proves the real chain/adapter route, not maintenance workload reliability.
func TestExpandedBudgetOpenRouterContract(t *testing.T) {
	if os.Getenv("MAINTENANCE_PROVIDER_PROBE") != "1" {
		t.Skip("explicit live probe opt-in required")
	}
	if os.Getenv("OPENROUTER_API_KEY") == "" {
		t.Fatal("OPENROUTER_API_KEY required")
	}
	client, err := ai.NewChainClient(ai.WithProviderChain("openai.openrouter"), ai.WithChainLogger(&core.NoOpLogger{}), ai.WithChainTimeout(3*time.Minute), ai.WithChainMaxRetries(0))
	if err != nil {
		t.Fatalf("construct chain: %T", err)
	}
	for _, tc := range []struct {
		purpose, model string
		tokens         int
		format         bool
	}{
		{"planning", "deepseek/deepseek-v4.1-flash", 30000, true},
		{"synthesis", "deepseek/deepseek-v4.1-flash", 30000, true},
		{"skill-activation", "z-ai/glm-5.3-flash", 5000, false},
		{"skill-resources", "z-ai/glm-5.3-flash", 5000, false},
	} {
		t.Run(tc.purpose, func(t *testing.T) {
			request := core.NewAIRequestFromLegacy(`Return only a JSON object {"status":"inconclusive","summary":"Synthetic evidence is insufficient"}. This is a transport probe.`, tc.purpose, &core.AIOptions{Model: tc.model, MaxTokens: tc.tokens})
			if tc.format {
				request.Generation.ResponseFormat = core.SetAIParameter("json")
			}
			started := time.Now()
			result, err := core.GenerateAI(t.Context(), client, request)
			if err != nil {
				t.Fatalf("provider invocation failed: %T", err)
			}
			if result == nil || result.Response == nil || result.RequestReport == nil {
				t.Fatal("missing provider evidence")
			}
			if result.RequestReport.EffectiveMaxTokens.Mode != core.AIParameterSet || result.RequestReport.EffectiveMaxTokens.Value != tc.tokens {
				t.Fatal("effective token budget differs from the requested diagnostic budget")
			}
			var decision struct{ Status, Summary string }
			if json.Unmarshal([]byte(result.Response.Content), &decision) != nil || decision.Status != "inconclusive" || decision.Summary == "" {
				var fields map[string]json.RawMessage
				_ = json.Unmarshal([]byte(result.Response.Content), &fields)
				var keys []string
				for key := range fields {
					keys = append(keys, key)
				}
				sort.Strings(keys)
				t.Fatalf("invalid synthetic decision: bytes=%d json_valid=%v fenced=%v status_matches=%v summary_present=%v keys=%v usage=%+v", len(result.Response.Content), json.Valid([]byte(result.Response.Content)), strings.HasPrefix(result.Response.Content, "```"), decision.Status == "inconclusive", decision.Summary != "", keys, result.Response.Usage)
			}
			t.Logf("purpose=%s model=%s tokens=%d effective=%+v duration_ms=%d content_bytes=%d", tc.purpose, result.RequestReport.ResolvedModel, tc.tokens, result.RequestReport.EffectiveMaxTokens, time.Since(started).Milliseconds(), len(result.Response.Content))
		})
	}
}
