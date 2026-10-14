package serviceevidence

import (
	"os"
	"time"

	"github.com/truvaagents/truva-g3/core"
)

const toolName = "maintenance-evidence-tool"

const ToolName = toolName

// NewFromEnvironment constructs the registered evidence service. Its data
// adapter is also reusable in-process without registry discovery or delegation.
func NewFromEnvironment() (*maintenanceEvidenceTool, error) {
	return newMaintenanceEvidenceToolFromEnvironment()
}

func (t *maintenanceEvidenceTool) ProfileCount() int { return len(t.profiles.profiles) }

// Close releases the adapter's idle connections after HTTP handlers have drained.
func (t *maintenanceEvidenceTool) Close() {
	if client, ok := t.client.(*serviceClient); ok {
		client.httpClient.CloseIdleConnections()
	}
}

type maintenanceEvidenceTool struct {
	*core.BaseTool
	profiles *profileStore
	auth     *authorizer
	client   evidenceClient
	now      func() time.Time
}

func newMaintenanceEvidenceToolFromEnvironment() (*maintenanceEvidenceTool, error) {
	base := core.NewTool(toolName)
	profiles, err := loadProfileStore(os.Getenv("MAINTENANCE_EVIDENCE_PROFILES_FILE"))
	if err != nil {
		return nil, err
	}
	auth, err := newAuthorizer(map[string]string{
		"maintenance-agent":        os.Getenv("MAINTENANCE_EVIDENCE_COORDINATOR_TOKEN"),
		"maintenance-repair-agent": os.Getenv("MAINTENANCE_EVIDENCE_REPAIR_TOKEN"),
		"maintenance-review-agent": os.Getenv("MAINTENANCE_EVIDENCE_REVIEW_TOKEN"),
	})
	if err != nil {
		return nil, err
	}
	return newMaintenanceEvidenceTool(base, profiles, auth, newServiceClient()), nil
}

func newMaintenanceEvidenceTool(base *core.BaseTool, profiles *profileStore, auth *authorizer, client evidenceClient) *maintenanceEvidenceTool {
	tool := &maintenanceEvidenceTool{
		BaseTool: base,
		profiles: profiles,
		auth:     auth,
		client:   client,
		now:      func() time.Time { return time.Now().UTC() },
	}
	tool.registerCapabilities()
	return tool
}

func (t *maintenanceEvidenceTool) registerCapabilities() {
	profileField := core.FieldHint{Name: "profile_id", Type: "string", Example: "retail-order-service", Description: "Server-owned maintenance profile identifier"}
	t.RegisterCapability(core.Capability{
		Name:         "get_service_profile",
		Description:  "Retrieves an approved maintenance profile, evidence limits, and pinned contract references. Use before interpreting service evidence; a profile that is not assessment-ready must not be used for autonomous correctness decisions.",
		InputTypes:   []string{"json"},
		OutputTypes:  []string{"json"},
		Handler:      t.handleGetServiceProfile,
		InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{profileField}},
		OutputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			{Name: "profile_id", Type: "string"},
			{Name: "profile_version", Type: "string"},
			{Name: "assessment_ready", Type: "boolean"},
			{Name: "service_name", Type: "string"},
			{Name: "repository", Type: "object"},
			{Name: "contract_references", Type: "array"},
			{Name: "supported_evidence", Type: "array"},
			{Name: "observation_constraints", Type: "object"},
			{Name: "limitations", Type: "array"},
		}},
	})
	t.RegisterCapability(core.Capability{
		Name:        "list_service_observations",
		Description: "Lists candidate observations from one bounded time window with explicit coverage limits. The feed is advisory evidence and does not diagnose a defect or establish completeness.",
		InputTypes:  []string{"json"},
		OutputTypes: []string{"json"},
		Handler:     t.handleListServiceObservations,
		InputSummary: &core.SchemaSummary{
			RequiredFields: []core.FieldHint{
				profileField,
				{Name: "start", Type: "string", Example: "2026-09-17T15:00:00Z", Description: "Inclusive RFC3339 window start"},
				{Name: "end", Type: "string", Example: "2026-09-17T15:05:00Z", Description: "RFC3339 window end"},
			},
			OptionalFields: []core.FieldHint{{Name: "limit", Type: "number", Example: "50", Description: "Maximum candidates, from 1 through the profile maximum"}},
		},
		OutputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			{Name: "profile_id", Type: "string"}, {Name: "profile_version", Type: "string"},
			{Name: "window", Type: "object"}, {Name: "observations", Type: "array"},
			{Name: "returned_count", Type: "number"}, {Name: "truncated", Type: "boolean"},
			{Name: "coverage", Type: "object"}, {Name: "collected_at", Type: "string"},
		}},
	})
	t.RegisterCapability(core.Capability{
		Name:        "get_service_evidence",
		Description: "Retrieves one recorded observation, serving revision, target trace identifier, pricing facts, and independently available ledger facts. observation.requested_promotion_code is the submitted promotion when captured: empty means none; absent means unavailable. Accepted request and pricing remain separate in get_business_outcome. The result is evidence, not a diagnosis.",
		InputTypes:  []string{"json"},
		OutputTypes: []string{"json"},
		Handler:     t.handleGetServiceEvidence,
		InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			profileField,
			{Name: "observation_id", Type: "string", Example: "observation-123", Description: "Observation identifier returned by list_service_observations"},
		}},
		OutputSummary: &core.SchemaSummary{
			RequiredFields: []core.FieldHint{
				{Name: "profile_id", Type: "string"}, {Name: "profile_version", Type: "string"},
				{Name: "observation", Type: "object"}, {Name: "provenance", Type: "object"},
				{Name: "limitations", Type: "array"},
			},
			OptionalFields: []core.FieldHint{{Name: "outcome_reference", Type: "object", Description: "Present for an accepted business outcome; outcome_id is its string identifier and kind describes the outcome"}},
		},
	})
	t.RegisterCapability(core.Capability{
		Name:        "get_business_outcome",
		Description: "Retrieves an immutable accepted order, request identity, pricing snapshot, and ledger result referenced by service evidence.",
		InputTypes:  []string{"json"},
		OutputTypes: []string{"json"},
		Handler:     t.handleGetBusinessOutcome,
		InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			profileField,
			{Name: "outcome_id", Type: "string", Example: "order-2026-0001", Description: "Exact string at get_service_evidence.outcome_reference.outcome_id; identifies the accepted business outcome"},
		}},
		OutputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			{Name: "profile_id", Type: "string"}, {Name: "profile_version", Type: "string"},
			{Name: "outcome_id", Type: "string"}, {Name: "request", Type: "object"},
			{Name: "request_sha256", Type: "string"}, {Name: "pricing_receipt", Type: "object"},
			{Name: "ledger", Type: "object"}, {Name: "provenance", Type: "object"},
		}},
	})
	t.RegisterCapability(core.Capability{
		Name:        "get_release_context",
		Description: "Retrieves current service-reported provenance: release.revision is the serving build ID, release.git_revision is the source commit, and release.source_matches_commit reports source consistency. Optionally compares an expected serving build ID. Build IDs and Git SHAs are distinct identity types; this is not a signed deployment attestation.",
		InputTypes:  []string{"json"},
		OutputTypes: []string{"json"},
		Handler:     t.handleGetReleaseContext,
		InputSummary: &core.SchemaSummary{
			RequiredFields: []core.FieldHint{profileField},
			OptionalFields: []core.FieldHint{{Name: "expected_revision", Type: "string", Example: "build-<64-hex-digest>", Description: "Use observation.serving_revision returned by get_service_evidence (build-<64-hex-digest>). git_revision identifies repository source separately. Omit for a current-release read without comparison."}},
		},
		OutputSummary: &core.SchemaSummary{
			RequiredFields: []core.FieldHint{
				{Name: "profile_id", Type: "string"}, {Name: "profile_version", Type: "string"},
				{Name: "release", Type: "object"}, {Name: "collected_at", Type: "string"},
				{Name: "limitations", Type: "array"},
			},
			OptionalFields: []core.FieldHint{{Name: "expected_revision", Type: "string"}, {Name: "revision_matches", Type: "boolean"}},
		},
	})
	t.RegisterCapability(core.Capability{
		Name:         "get_runtime_configuration",
		Description:  "Retrieves current non-secret retail catalog configuration only when two build reads prove a stable serving revision. It does not establish historical applicability.",
		InputTypes:   []string{"json"},
		OutputTypes:  []string{"json"},
		Handler:      t.handleGetRuntimeConfiguration,
		InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{profileField}},
		OutputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			{Name: "profile_id", Type: "string"}, {Name: "profile_version", Type: "string"},
			{Name: "configuration_type", Type: "string"}, {Name: "serving_revision", Type: "string"},
			{Name: "configuration", Type: "object"}, {Name: "collected_at", Type: "string"},
			{Name: "applicability", Type: "string"}, {Name: "limitations", Type: "array"},
		}},
	})
}
