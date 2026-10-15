package main

import (
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/truvaagents/truva-g3/core"
)

const publicationToolName = "maintenance-publication-tool"

type maintenancePublicationTool struct {
	*core.BaseTool
	profiles    *publicationProfileStore
	auth        *publicationAuthorizer
	operations  *publicationOperations
	redisClient redis.UniversalClient
}

func newMaintenancePublicationToolFromEnvironment() (*maintenancePublicationTool, error) {
	errorMessageMaxBytes, err := githubErrorMessageLimitFromEnvironment()
	if err != nil {
		return nil, err
	}
	profiles, err := loadPublicationProfiles(os.Getenv("MAINTENANCE_PUBLICATION_PROFILES_FILE"))
	if err != nil {
		return nil, err
	}
	auth, err := newPublicationAuthorizer("maintenance-agent", os.Getenv("MAINTENANCE_PUBLICATION_COORDINATOR_TOKEN"))
	if err != nil {
		return nil, err
	}
	redisConfig, err := core.ResolveRedisConnectionConfig(nil, os.LookupEnv)
	if err != nil {
		return nil, err
	}
	redisClient, err := core.NewRedisUniversalClient(redisConfig)
	if err != nil {
		return nil, err
	}
	keyspace, err := core.NewRedisKeyspace(os.Getenv("TRUVAG3_REDIS_NAMESPACE"))
	if err != nil {
		_ = redisClient.Close()
		return nil, err
	}
	var issuers *trustedCapabilityClient
	var github *githubPublicationClient
	// Repository reads use the same credential and profile authority, independently
	// of draft-publication readiness. An absent credential leaves reads unavailable.
	if token := os.Getenv("GITHUB_WRITE_TOKEN"); token != "" {
		github, err = newGitHubPublicationClient(environmentOrDefault("GITHUB_API_BASE_URL", "https://api.github.com"), token)
		if err != nil {
			_ = redisClient.Close()
			return nil, err
		}
		github.errorMessageMaxBytes = errorMessageMaxBytes
	}
	for _, profile := range profiles.profiles {
		if !profile.PublicationReady {
			continue
		}
		if os.Getenv("MAINTENANCE_PUBLICATION_ENABLE_WRITES") != "true" {
			_ = redisClient.Close()
			return nil, errors.New("ready publication profiles require MAINTENANCE_PUBLICATION_ENABLE_WRITES=true")
		}
		issuers, err = newTrustedCapabilityClient(
			environmentOrDefault("MAINTENANCE_WORKSPACE_TOOL_URL", "http://maintenance-workspace-tool.software-maintenance.svc.cluster.local"),
			os.Getenv("MAINTENANCE_WORKSPACE_PUBLICATION_TOKEN"),
			environmentOrDefault("MAINTENANCE_REVIEW_AGENT_URL", "http://maintenance-review-agent.software-maintenance.svc.cluster.local"),
			os.Getenv("MAINTENANCE_REVIEW_PUBLICATION_TOKEN"),
		)
		if err != nil {
			_ = redisClient.Close()
			return nil, err
		}
		if github == nil {
			_ = redisClient.Close()
			return nil, errors.New("ready publication profiles require GITHUB_WRITE_TOKEN")
		}
		break
	}
	tool := &maintenancePublicationTool{
		BaseTool: core.NewTool(publicationToolName), profiles: profiles, auth: auth, redisClient: redisClient,
	}
	tool.operations = &publicationOperations{
		profiles: profiles, store: &publicationStore{client: redisClient, keyspace: keyspace},
		issuers: issuers, github: github, now: func() time.Time { return time.Now().UTC() },
	}
	tool.registerCapabilities()
	return tool, nil
}

func (tool *maintenancePublicationTool) registerCapabilities() {
	tool.RegisterCapability(core.Capability{
		Name: "reconcile_publication", Description: "Reconciles an original pending publication without GitHub writes or new approval. Use the exact origin_case_id as case_id and publication_id from PUBLICATION_COORDINATION_PENDING. Releases an abandoned pre-PR attempt or an exactly identified existing open/closed PR; unknown effects remain blocked. A resolution is external-effect evidence, not proof of issue applicability, verification, merge or deployment.",
		InputTypes: []string{"json"}, OutputTypes: []string{"json"}, Handler: tool.handleReconcilePublication,
		InputSummary:  &core.SchemaSummary{RequiredFields: []core.FieldHint{{Name: "profile_id", Type: "string"}, {Name: "case_id", Type: "string", Description: "Original pending publication's origin_case_id, not the current investigation's case."}, {Name: "publication_id", Type: "string"}}},
		OutputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{{Name: "publication_id", Type: "string"}, {Name: "case_id", Type: "string"}, {Name: "disposition", Type: "string", Description: "abandoned_before_write, abandoned_before_pr, observed_open_pr, observed_closed_pr, or not_pending. not_pending establishes no publication outcome."}, {Name: "observed_at", Type: "string"}}, OptionalFields: []core.FieldHint{{Name: "pull_request_number", Type: "number"}, {Name: "pull_request_url", Type: "string"}}},
	})
	tool.RegisterCapability(core.Capability{
		Name: "list_open_remediations", Description: "Discovers open PRs, including drafts and manually created work, for the authorized repository/base. Use to assess existing work before deciding whether new publication is warranted. Complete coverage includes repository_view_id: pass that exact value to publish_draft_pr; changed PR state requires fresh assessment. Returns PR numbers, titles, base/head identities, completeness and limitations. Required: profile_id.",
		InputTypes: []string{"json"}, OutputTypes: []string{"json"}, Handler: func(w http.ResponseWriter, r *http.Request) { tool.handleRemediationRead(w, r, false) },
		InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{{Name: "profile_id", Type: "string", Description: "Authorized publication profile selecting repository and base branch."}}},
		OutputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			{Name: "profile_id", Type: "string"}, {Name: "repository_id", Type: "string"}, {Name: "base_ref", Type: "string"},
			{Name: "pull_requests", Type: "array", Description: "Entries include pull_request_number, pull_request_url, title, base_sha, head_sha and optional draft."},
			{Name: "complete", Type: "boolean"}, {Name: "observed_at", Type: "string"}, {Name: "limitations", Type: "array"},
		}, OptionalFields: []core.FieldHint{{Name: "repository_view_id", Type: "string", Description: "Present for complete coverage. Repository-state precondition for publication; it certifies observed state, not issue applicability."}}},
	})
	tool.RegisterCapability(core.Capability{
		Name: "get_remediation_changes", Description: "Reads a pull request's current state, explanation and changed files with bounded text patches. Use to assess whether discovered prior work addresses current evidence. Returns remediation with state, base_sha and head_sha, plus files, files_complete and limitations; this is repository evidence, not approval or proof of deployment. Required: profile_id, pull_request_number. Optional: expected_head_sha from discovery to detect changed work.",
		InputTypes: []string{"json"}, OutputTypes: []string{"json"}, Handler: func(w http.ResponseWriter, r *http.Request) { tool.handleRemediationRead(w, r, true) },
		InputSummary:  &core.SchemaSummary{RequiredFields: []core.FieldHint{{Name: "profile_id", Type: "string"}, {Name: "pull_request_number", Type: "number", Description: "Exact positive pull_requests[].pull_request_number from discovery, or an attributable existing reference."}}, OptionalFields: []core.FieldHint{{Name: "expected_head_sha", Type: "string", Description: "Exact pull_requests[].head_sha from discovery; a mismatch returns REMEDIATION_CHANGED."}}},
		OutputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{{Name: "remediation", Type: "object", Description: "Fresh state, pull_request_number, URL, base_sha, head_sha and observed_at."}, {Name: "title", Type: "string"}, {Name: "body", Type: "string"}, {Name: "body_complete", Type: "boolean"}, {Name: "files", Type: "array", Description: "Entries include path, status, blob_sha, additions, deletions, optional patch and patch_complete."}, {Name: "files_complete", Type: "boolean"}, {Name: "limitations", Type: "array"}}},
	})
	tool.RegisterCapability(core.Capability{
		Name: "get_remediation_state", Description: "Reads fresh GitHub pull-request state in an authorized repository profile. Supply either pull_request_number or publication_id with origin_case_id. Historical receipts retain their original attribution; this read does not prove issue applicability, deployment, or resolution and performs no writes.",
		InputTypes: []string{"json"}, OutputTypes: []string{"json"}, Handler: tool.handleGetRemediationState,
		InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			{Name: "profile_id", Type: "string", Description: "Authorized publication profile selecting the repository and read scope."},
		}, OptionalFields: []core.FieldHint{
			{Name: "pull_request_number", Type: "number", Description: "Positive PR number in the profile repository; use this form without publication_id or origin_case_id."},
			{Name: "publication_id", Type: "string", Description: "Exact stored publication receipt handle; use with its original origin_case_id instead of pull_request_number."},
			{Name: "origin_case_id", Type: "string", Description: "Original case from that publication receipt, including when different from the current case."},
		}},
		OutputSummary: remediationOutputSummary(),
	})
	tool.RegisterCapability(core.Capability{
		Name: "get_publication", Description: "Returns the durable outcome for one case-scoped publication reservation, including the exact candidate, commit, branch, and draft pull-request identities.",
		InputTypes: []string{"json"}, OutputTypes: []string{"json"}, Handler: tool.handleGetPublication,
		InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			{Name: "profile_id", Type: "string", Example: "retail-order-publication"}, {Name: "case_id", Type: "string"}, {Name: "publication_id", Type: "string"},
		}},
		OutputSummary: publicationOutputSummary(),
	})
	tool.RegisterCapability(core.Capability{
		Name: "publish_draft_pr", Description: "Publishes a verified and independently reviewed candidate as a draft PR when assessed open-PR state still holds. Required repository_view_id comes from complete list_open_remediations evidence used for this decision. REPOSITORY_VIEW_CHANGED requires fresh PR inspection and reassessment; PUBLICATION_COORDINATION_PENDING requires reconciling the recorded publication. These refusals establish no issue applicability. It cannot merge, approve or deploy.",
		InputTypes: []string{"json"}, OutputTypes: []string{"json"}, Handler: tool.handlePublishDraftPR,
		InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			{Name: "profile_id", Type: "string", Example: "retail-order-publication"}, {Name: "case_id", Type: "string"},
			{Name: "repository_view_id", Type: "string", Description: "Exact repository_view_id from complete open-PR discovery assessed for this decision. Preserve case/artifact identities when reconsidering changed state."},
			{Name: "candidate_id", Type: "string"}, {Name: "verification_id", Type: "string"}, {Name: "review_id", Type: "string"},
			{Name: "title", Type: "string"}, {Name: "body", Type: "string"},
		}},
		OutputSummary: publicationOutputSummary(),
	})
}

func publicationOutputSummary() *core.SchemaSummary {
	return &core.SchemaSummary{RequiredFields: []core.FieldHint{
		{Name: "publication_id", Type: "string"}, {Name: "profile_id", Type: "string"}, {Name: "case_id", Type: "string"},
		{Name: "repository_id", Type: "string"}, {Name: "workspace_id", Type: "string"}, {Name: "candidate_id", Type: "string"},
		{Name: "verification_id", Type: "string"}, {Name: "review_id", Type: "string"}, {Name: "base_ref", Type: "string"},
		{Name: "base_sha", Type: "string"}, {Name: "base_tree_sha", Type: "string"}, {Name: "branch", Type: "string"},
		{Name: "draft", Type: "boolean"}, {Name: "status", Type: "string"}, {Name: "created_at", Type: "string"},
		{Name: "updated_at", Type: "string"}, {Name: "expires_at", Type: "string"}, {Name: "limitations", Type: "array"},
	}, OptionalFields: []core.FieldHint{
		{Name: "repository_view_id", Type: "string", Description: "Repository view accepted before this publication's Git writes; absent from older receipts."},
		{Name: "candidate_tree_sha", Type: "string"}, {Name: "commit_sha", Type: "string"},
		{Name: "pull_request_id", Type: "number"}, {Name: "pull_request_number", Type: "number"},
		{Name: "pull_request_url", Type: "string"}, {Name: "failure_code", Type: "string"},
	}}
}

func environmentOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
