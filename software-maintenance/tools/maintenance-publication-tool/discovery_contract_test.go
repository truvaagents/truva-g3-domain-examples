package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/discoverytest"
	"github.com/truvaagents/truva-g3/core"
)

func TestPublicationDiscoveryContract(t *testing.T) {
	profile, _ := remediationFixture()
	profile.BaseRef = "main"
	profile.Limits.MaximumTitleBytes = 8
	profile.Limits.MaximumBodyBytes = 32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || !strings.HasSuffix(r.URL.Path, "/pulls") {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	origin, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	operations := &publicationOperations{github: &githubPublicationClient{baseURL: origin, httpClient: server.Client()}, now: time.Now}
	listing, err := operations.listOpenRemediations(t.Context(), profile)
	if err != nil {
		t.Fatal(err)
	}
	if listing.PublicationLimits.MaximumTitleBytes != 8 || listing.PublicationLimits.MaximumBodyBytes != 32 {
		t.Fatalf("wrong publication limits: %+v", listing.PublicationLimits)
	}
	tool := &maintenancePublicationTool{BaseTool: core.NewTool(publicationToolName)}
	tool.registerCapabilities()
	discoverytest.Output(t, discoverytest.Capability(t, tool.GetCapabilities(), "list_open_remediations"), listing, "publication_limits")
	for _, tc := range []struct {
		title, body string
		valid       bool
	}{
		{strings.Repeat("é", 4), "line1\nline2\tend", true},
		{strings.Repeat("é", 5), "body", false},
		{"title", strings.Repeat("é", 16), true},
		{"title", strings.Repeat("é", 17), false},
		{"line\nbreak", "body", false},
	} {
		_, _, err := validatePublicationText(profile, tc.title, tc.body)
		if (err == nil) != tc.valid {
			t.Errorf("text validation: title=%q body=%q error=%v", tc.title, tc.body, err)
		}
	}
	for _, name := range []string{"get_publication", "publish_draft_pr"} {
		capability := discoverytest.Capability(t, tool.GetCapabilities(), name)
		for _, status := range []string{"pending", "draft_open", "failed"} {
			response := publicationRecord{SchemaVersion: 1, Status: status, Limitations: []string{}}
			if status == "draft_open" {
				response.PullRequestNumber = 7
				response.PullRequestID = 42
				response.PullRequestURL = "https://github.invalid/fixture/service/pull/7"
			}
			discoverytest.Output(t, capability, response, "snapshot_id", "test_artifact_id", "workspace_profile_sha256", "source_profile_sha256", "review_profile_sha256")
		}
	}
	catalog := discoverytest.Catalog(t, tool.GetCapabilities())
	for _, expected := range []string{"publication_limits:object", "list_open_remediations.publication_limits.maximum_title_bytes", "list_open_remediations.publication_limits.maximum_body_bytes", "snapshot_id:string", "test_artifact_id:string"} {
		if !strings.Contains(catalog, expected) {
			t.Errorf("planner catalog lacks %q", expected)
		}
	}
}
