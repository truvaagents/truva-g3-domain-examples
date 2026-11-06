package main

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/discoverytest"
	"github.com/truvaagents/truva-g3/core"
)

func TestSourcePaginationDiscoveryContract(t *testing.T) {
	revision, treeSHA := strings.Repeat("a", 40), strings.Repeat("b", 40)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "/commits/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"sha": revision, "commit": map[string]any{"tree": map[string]string{"sha": treeSHA}}})
		case strings.Contains(r.URL.Path, "/git/trees/"):
			_ = json.NewEncoder(w).Encode(map[string]any{"sha": treeSHA, "truncated": false, "tree": []map[string]any{
				{"path": "a.go", "type": "blob", "mode": "100644", "sha": strings.Repeat("c", 40), "size": 10},
				{"path": "b.go", "type": "blob", "mode": "100644", "sha": strings.Repeat("d", 40), "size": 10},
			}})
		default:
			t.Errorf("unexpected source request %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	endpoint, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	operations := sourceOperations{client: &githubSourceClient{baseURL: endpoint, httpClient: server.Client()}, now: time.Now}
	profile := sourceProfile{RepositoryID: "repository", Owner: "owner", Repository: "repository", Limits: sourceLimits{MaximumTreeResults: 1, MaximumTreeEntries: 10, MaximumAPIResponseBytes: 4096, TimeoutMilliseconds: 1000}}
	tool := &maintenanceSourceTool{BaseTool: core.NewTool(sourceToolName)}
	tool.registerCapabilities()
	capability := discoverytest.Capability(t, tool.GetCapabilities(), "list_source_tree")
	first, err := operations.listTree(t.Context(), profile, listTreeRequest{RepositoryID: profile.RepositoryID, RevisionSHA: revision})
	if err != nil {
		t.Fatal(err)
	}
	if first.NextAfter != "a.go" || !first.Truncated {
		t.Fatalf("first page: %+v", first)
	}
	discoverytest.Output(t, capability, first, "next_after")
	second, err := operations.listTree(t.Context(), profile, listTreeRequest{RepositoryID: profile.RepositoryID, RevisionSHA: revision, After: first.NextAfter})
	if err != nil {
		t.Fatal(err)
	}
	if second.NextAfter != "" || second.Truncated || len(second.Entries) != 1 || second.Entries[0].Path != "b.go" {
		t.Fatalf("second page: %+v", second)
	}
	discoverytest.Output(t, capability, second)
	catalog := discoverytest.Catalog(t, tool.GetCapabilities())
	for _, expected := range []string{"next_after:string", "Copy next_after", "2 through 128 UTF-8 bytes"} {
		if !strings.Contains(catalog, expected) {
			t.Errorf("planner catalog lacks %q", expected)
		}
	}
}
