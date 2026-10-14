package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/truvaagents/truva-g3/core"
)

const workspaceToolName = "maintenance-workspace-tool"

type maintenanceWorkspaceTool struct {
	*core.BaseTool
	profiles            *workspaceProfileStore
	auth                *workspaceAuthorizer
	operations          *workspaceOperations
	redisClient         redis.UniversalClient
	publicationIdentity string
	reviewIdentity      string
	maxJobWait          time.Duration
	jobPollInterval     time.Duration
}

func newMaintenanceWorkspaceToolFromEnvironment() (*maintenanceWorkspaceTool, error) {
	base := core.NewTool(workspaceToolName)
	maxWait, err := time.ParseDuration(environmentOrDefault("MAINTENANCE_WORKSPACE_MAX_JOB_WAIT", "30s"))
	if err != nil || maxWait < 0 || maxWait > 10*time.Minute {
		return nil, errors.New("MAINTENANCE_WORKSPACE_MAX_JOB_WAIT must be between 0 and 10m")
	}
	poll, err := time.ParseDuration(environmentOrDefault("MAINTENANCE_WORKSPACE_JOB_POLL_INTERVAL", "1s"))
	if err != nil || poll < 100*time.Millisecond || poll > time.Minute {
		return nil, errors.New("MAINTENANCE_WORKSPACE_JOB_POLL_INTERVAL must be between 100ms and 1m")
	}
	profiles, err := loadWorkspaceProfiles(os.Getenv("MAINTENANCE_WORKSPACE_PROFILES_FILE"))
	if err != nil {
		return nil, err
	}
	isolationAttested := os.Getenv("MAINTENANCE_NETWORK_ISOLATION_ATTESTED") == "true"
	functionalAcceptance := os.Getenv("APP_ENV") == "development" && os.Getenv("MAINTENANCE_FUNCTIONAL_ACCEPTANCE_MODE") == "true"
	for _, profile := range profiles.profiles {
		if profile.VerificationReady && !isolationAttested && !functionalAcceptance {
			return nil, errors.New("ready workspace profiles require network-isolation attestation or explicit development functional-acceptance mode")
		}
	}
	auth, err := newWorkspaceAuthorizer(map[string]string{
		"maintenance-agent":            os.Getenv("MAINTENANCE_WORKSPACE_COORDINATOR_TOKEN"),
		"maintenance-repair-agent":     os.Getenv("MAINTENANCE_WORKSPACE_REPAIR_TOKEN"),
		"maintenance-review-agent":     os.Getenv("MAINTENANCE_WORKSPACE_REVIEW_TOKEN"),
		"maintenance-publication-tool": os.Getenv("MAINTENANCE_WORKSPACE_PUBLICATION_TOKEN"),
	})
	if err != nil {
		return nil, err
	}
	sourceClient, err := newSourceSnapshotClient(environmentOrDefault("MAINTENANCE_SOURCE_TOOL_URL", "http://maintenance-source-tool.software-maintenance.svc.cluster.local"), os.Getenv("MAINTENANCE_SOURCE_WORKSPACE_TOKEN"))
	if err != nil {
		return nil, err
	}
	runner, err := newKubernetesRunnerFromEnvironment(functionalAcceptance && !isolationAttested)
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
	root := environmentOrDefault("MAINTENANCE_WORKSPACE_ROOT", "/var/lib/maintenance-workspaces")
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == "/" {
		_ = redisClient.Close()
		return nil, errors.New("MAINTENANCE_WORKSPACE_ROOT must be a normalized absolute non-root path")
	}
	if err := os.MkdirAll(root, 0o750); err != nil {
		_ = redisClient.Close()
		return nil, errors.New("create workspace root")
	}
	store := &workspaceStore{client: redisClient, keyspace: keyspace}
	tool := &maintenanceWorkspaceTool{
		BaseTool: base, profiles: profiles, auth: auth, redisClient: redisClient,
		maxJobWait: maxWait, jobPollInterval: poll,
		publicationIdentity: "maintenance-publication-tool",
		reviewIdentity:      "maintenance-review-agent",
	}
	tool.operations = &workspaceOperations{
		root: root, profiles: profiles, store: store, sourceClient: sourceClient, runner: runner,
		now: func() time.Time { return time.Now().UTC() },
	}
	tool.registerCapabilities()
	return tool, nil
}

func (tool *maintenanceWorkspaceTool) registerCapabilities() {
	tool.RegisterCapability(core.Capability{
		Name: "create_workspace", Description: "Creates an isolated immutable workspace from a source-tool snapshot handle and binds it to one maintenance case and runner profile.",
		InputTypes: []string{"json"}, OutputTypes: []string{"json"}, Handler: tool.handleCreateWorkspace,
		InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			{Name: "profile_id", Type: "string", Description: "Use the workspace runner profile supplied in the repair request's workspace_profile_id as profile_id.", Example: "retail-order-go"}, {Name: "case_id", Type: "string"}, {Name: "snapshot_id", Type: "string"},
		}},
		OutputSummary: workspaceOutputSummary("workspace_id", "profile_id", "profile_sha256", "source_profile_sha256", "case_id", "repository_id", "snapshot_id", "revision_sha", "tree_sha", "archive_sha256", "verification_ready", "created_at", "expires_at", "limitations"),
	})
	tool.RegisterCapability(core.Capability{
		Name: "stage_candidate", Description: `Stages bounded source changes and regression tests after exact base-identity checks. Existing paths require their exact SHA-256. New paths require base_sha256 to be the literal string "absent", not an empty-content digest. Returns immutable candidate and test artifact identities without executing code.`,
		InputTypes: []string{"json"}, OutputTypes: []string{"json"}, Handler: tool.handleStageCandidate,
		InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			{Name: "workspace_id", Type: "string", Example: "ws_<64 lowercase hex>", Description: "Exact workspace_id returned by create_workspace; never a repository, profile, snapshot, or revision identifier"}, {Name: "case_id", Type: "string"}, {Name: "expected_test", Type: "string", Example: "TestOrderTotal", Description: "Exact Go test function name only (Test...); do not include a sentence, command, package, or explanation"},
			{Name: "changes", Type: "array", Description: `Objects with path, base_sha256, and exactly one explicitly present content_text (UTF-8 source directly) or content_base64, or delete:true without content. Omitted, null, or dual encodings are invalid; an explicit empty string intentionally creates an empty file. Preserve exact source bytes; no shell encoding is needed. base_sha256 is the exact existing-file digest or literal "absent" for a new path`},
			{Name: "tests", Type: "array", Description: `New or changed *_test.go files with path, base_sha256, and exactly one explicitly present content_text or content_base64. Omitted, null, or dual encodings are invalid; empty strings are intentional empty content. Use the exact existing-file digest or literal "absent" for a new path`},
		}},
		OutputSummary: workspaceOutputSummary("candidate_id", "test_artifact_id", "workspace_id", "profile_id", "profile_sha256", "case_id", "repository_id", "snapshot_id", "revision_sha", "expected_test", "change_count", "test_count", "candidate_bytes", "proposed_by", "created_at", "expires_at"),
	})
	tool.RegisterCapability(core.Capability{
		Name: "run_experiment", Description: "Submits one profile-approved command against an immutable baseline, reproduction, or candidate tree. Returns a Job handle, not a success claim.",
		InputTypes: []string{"json"}, OutputTypes: []string{"json"}, Handler: tool.handleRunExperiment,
		InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			{Name: "workspace_id", Type: "string"}, {Name: "case_id", Type: "string"}, {Name: "target", Type: "string", Example: "candidate"},
			{Name: "command_id", Type: "string", Example: "go-test-all"}, {Name: "idempotency_key", Type: "string"},
		}, OptionalFields: []core.FieldHint{{Name: "candidate_id", Type: "string", Description: "Required for reproduction or candidate targets"}}},
		OutputSummary: jobOutputSummary(),
	})
	tool.RegisterCapability(core.Capability{
		Name: "run_verification", Description: "Submits the owner-configured baseline reproduction and protected candidate suite for an exact candidate. Caller cannot supply commands or images.",
		InputTypes: []string{"json"}, OutputTypes: []string{"json"}, Handler: tool.handleRunVerification,
		InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			{Name: "candidate_id", Type: "string"}, {Name: "case_id", Type: "string"},
		}},
		OutputSummary: workspaceOutputSummary("verification_id", "candidate_id", "profile_id", "profile_sha256", "reproduction_job_id", "candidate_job_ids", "status", "expected_test_observed", "suite_passed", "publication_eligible", "created_at", "updated_at"),
	})
	tool.RegisterCapability(core.Capability{
		Name: "get_job", Description: "Returns reconciled Kubernetes state, actual exit code, timeout state, and immutable output artifact identity for a sandbox Job. Optional bounded waiting observes the existing Job; wait expiry leaves its state unchanged and returns the last reconciled record.",
		InputTypes: []string{"json"}, OutputTypes: []string{"json"}, Handler: tool.handleGetJob,
		InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			{Name: "job_id", Type: "string"}, {Name: "case_id", Type: "string"},
		}, OptionalFields: []core.FieldHint{{Name: "wait_seconds", Type: "number", Description: fmt.Sprintf("Integer seconds from 0 through %d. Omit or zero for immediate reconciliation; waiting does not cancel the Job.", int64(tool.maxJobWait/time.Second))}}},
		OutputSummary: jobOutputSummary(),
	})
	tool.RegisterCapability(core.Capability{
		Name: "read_job_artifact", Description: "Reads an exact bounded byte slice of immutable Job output. Request format=text for readable UTF-8; binary or split UTF-8 slices return base64. Absent format preserves the legacy base64 response. sha256 identifies the whole artifact, not this slice; byte_end is the next offset.",
		InputTypes: []string{"json"}, OutputTypes: []string{"json"}, Handler: tool.handleReadJobArtifact,
		InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			{Name: "artifact_id", Type: "string"}, {Name: "case_id", Type: "string"},
		}, OptionalFields: []core.FieldHint{{Name: "byte_start", Type: "number"}, {Name: "byte_limit", Type: "number"}, {Name: "format", Type: "string", Description: "text or base64; absent preserves base64"}}},
		OutputSummary: artifactOutputSummary(),
	})
	tool.RegisterCapability(core.Capability{
		Name: "get_review_bundle", Description: "Returns an exact candidate, bounded text previews of its immutable files, and current trusted verification receipts for independent review. Preview digests bind the readable text to the candidate content; the result does not assert approval. Only the review-agent service identity may call it.",
		InputTypes: []string{"json"}, OutputTypes: []string{"json"}, Handler: tool.handleGetReviewBundle,
		InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			{Name: "case_id", Type: "string"}, {Name: "candidate_id", Type: "string"}, {Name: "verification_id", Type: "string"},
		}},
		OutputSummary: workspaceOutputSummary("workspace", "candidate", "candidate_file_previews", "verification", "jobs"),
	})
	tool.RegisterCapability(core.Capability{
		Name: "get_publication_bundle", Description: "Internal publication-tool retrieval of an exact eligible candidate and its trusted workspace verification receipts.",
		Internal: true, InputTypes: []string{"json"}, OutputTypes: []string{"json"}, Handler: tool.handleGetPublicationBundle,
		InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			{Name: "case_id", Type: "string"}, {Name: "candidate_id", Type: "string"}, {Name: "verification_id", Type: "string"},
		}},
		OutputSummary: workspaceOutputSummary("workspace", "candidate", "verification", "jobs"),
	})
}

func artifactOutputSummary() *core.SchemaSummary {
	summary := workspaceOutputSummary("artifact_id", "sha256", "media_type", "byte_start", "byte_end", "total_bytes", "truncated")
	summary.OptionalFields = append(summary.OptionalFields,
		core.FieldHint{Name: "content_text", Type: "string", Description: "Present only for text mode with a valid UTF-8 slice; exactly one content field is returned"},
		core.FieldHint{Name: "content_base64", Type: "string", Description: "Legacy mode or binary/split-character fallback"},
		core.FieldHint{Name: "encoding", Type: "string", Description: "Explicit text mode only: utf-8 or base64"})
	return summary
}

func workspaceOutputSummary(fields ...string) *core.SchemaSummary {
	hints := make([]core.FieldHint, 0, len(fields))
	for _, field := range fields {
		fieldType := "string"
		description := ""
		switch field {
		case "limitations", "candidate_job_ids", "candidate_file_previews", "jobs":
			fieldType = "array"
		case "workspace", "candidate", "verification":
			fieldType = "object"
		case "verification_ready", "expected_test_observed", "suite_passed", "publication_eligible", "timed_out", "truncated", "artifact_truncated":
			fieldType = "boolean"
		case "change_count", "test_count", "candidate_bytes", "exit_code", "artifact_bytes", "byte_start", "byte_end", "total_bytes":
			fieldType = "number"
		case "workspace_id":
			description = "Immutable workspace handle beginning with ws_; copy this exact value into later workspace capabilities"
		}
		hints = append(hints, core.FieldHint{Name: field, Type: fieldType, Description: description})
	}
	return &core.SchemaSummary{RequiredFields: hints}
}

func jobOutputSummary() *core.SchemaSummary {
	summary := workspaceOutputSummary("job_id", "kubernetes_job", "workspace_id", "profile_id", "profile_sha256", "case_id", "target", "command_id", "command_sha256", "runner_image", "status", "artifact_truncated", "timed_out", "created_at", "expires_at")
	optional := workspaceOutputSummary("candidate_id", "verification_id", "exit_code", "artifact_id", "artifact_sha256", "artifact_bytes", "started_at", "completed_at")
	for _, field := range optional.RequiredFields {
		field.Description = "State-dependent: may be absent or unset while pending/running or when not applicable. Read status first; missing exit/artifact fields do not imply success or an available artifact."
		summary.OptionalFields = append(summary.OptionalFields, field)
	}
	return summary
}

func environmentOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
