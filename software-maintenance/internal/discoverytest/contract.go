// Package discoverytest provides contract checks for the domain's component tests.
package discoverytest

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/truvaagents/truva-g3/core"
	"github.com/truvaagents/truva-g3/orchestration"
)

type discovery struct {
	core.Discovery
	service *core.ServiceInfo
}

func (d discovery) Discover(context.Context, core.DiscoveryFilter) ([]*core.ServiceInfo, error) {
	return []*core.ServiceInfo{d.service}, nil
}

// Catalog exercises the real HTTP catalog enrichment and planner rendering path.
func Catalog(t *testing.T, capabilities []core.Capability) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(capabilities); err != nil {
			t.Error(err)
		}
	}))
	t.Cleanup(server.Close)
	host, portText, err := net.SplitHostPort(server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	service := &core.ServiceInfo{ID: "contract-fixture", Name: "contract-fixture", Address: host, Port: port, Capabilities: capabilities}
	catalog := orchestration.NewAgentCatalog(discovery{service: service})
	if err := catalog.Refresh(t.Context()); err != nil {
		t.Fatal(err)
	}
	return catalog.FormatForLLM()
}

func Capability(t *testing.T, capabilities []core.Capability, name string) core.Capability {
	t.Helper()
	for _, capability := range capabilities {
		if capability.Name == name {
			return capability
		}
	}
	t.Fatalf("capability %q is not registered", name)
	return core.Capability{}
}

// Output checks advertised requiredness and wire types, and any fields the caller
// intends to compose. Undeclared implementation metadata is allowed.
func Output(t *testing.T, capability core.Capability, response any, consumed ...string) {
	t.Helper()
	body, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	if capability.OutputSummary == nil {
		t.Fatal("missing output summary")
	}
	declared := map[string]bool{}
	for _, group := range []struct {
		fields   []core.FieldHint
		required bool
	}{{capability.OutputSummary.RequiredFields, true}, {capability.OutputSummary.OptionalFields, false}} {
		for _, hint := range group.fields {
			if declared[hint.Name] {
				t.Errorf("duplicate output hint %s", hint.Name)
			}
			declared[hint.Name] = true
			value, exists := fields[hint.Name]
			if !exists {
				if group.required {
					t.Errorf("required output %s absent from %s", hint.Name, body)
				}
				continue
			}
			if value == nil { // Some existing receipts explicitly permit null projections.
				continue
			}
			actual := ""
			switch value.(type) {
			case string:
				actual = "string"
			case float64:
				actual = "number"
			case bool:
				actual = "boolean"
			case []any:
				actual = "array"
			case map[string]any:
				actual = "object"
			}
			if actual != hint.Type {
				t.Errorf("output %s has type %s, advertised %s", hint.Name, actual, hint.Type)
			}
		}
	}
	for _, field := range consumed {
		if !declared[field] {
			t.Errorf("consumed output %s is not advertised", field)
		}
	}
}
