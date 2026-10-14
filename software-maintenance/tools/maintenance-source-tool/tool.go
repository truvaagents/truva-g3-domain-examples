package main

import (
	"os"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/truvaagents/truva-g3/core"
)

const sourceToolName = "maintenance-source-tool"

type maintenanceSourceTool struct {
	*core.BaseTool
	profiles          *sourceProfileStore
	auth              *sourceAuthorizer
	operations        *sourceOperations
	redisClient       redis.UniversalClient
	workspaceIdentity string
}

func newMaintenanceSourceToolFromEnvironment() (*maintenanceSourceTool, error) {
	base := core.NewTool(sourceToolName)
	profiles, err := loadSourceProfileStore(os.Getenv("MAINTENANCE_SOURCE_PROFILES_FILE"))
	if err != nil {
		return nil, err
	}
	auth, err := newSourceAuthorizer(map[string]string{
		"maintenance-agent":          os.Getenv("MAINTENANCE_SOURCE_COORDINATOR_TOKEN"),
		"maintenance-repair-agent":   os.Getenv("MAINTENANCE_SOURCE_REPAIR_TOKEN"),
		"maintenance-review-agent":   os.Getenv("MAINTENANCE_SOURCE_REVIEW_TOKEN"),
		"maintenance-workspace-tool": os.Getenv("MAINTENANCE_SOURCE_WORKSPACE_TOKEN"),
	})
	if err != nil {
		return nil, err
	}
	githubClient, err := newGitHubSourceClient(environmentOrDefault("GITHUB_API_BASE_URL", "https://api.github.com"), os.Getenv("GITHUB_TOKEN"))
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
	store := &redisSnapshotStore{client: redisClient, keyspace: keyspace}
	tool := &maintenanceSourceTool{
		BaseTool: base, profiles: profiles, auth: auth, redisClient: redisClient,
		workspaceIdentity: "maintenance-workspace-tool",
	}
	tool.operations = &sourceOperations{client: githubClient, store: store, now: func() time.Time { return time.Now().UTC() }}
	tool.registerCapabilities()
	return tool, nil
}

func (tool *maintenanceSourceTool) registerCapabilities() {
	repositoryField := core.FieldHint{Name: "repository_id", Type: "string", Example: "retail-order-service", Description: "Server-owned repository profile identifier supplied by the authorized agent profile context; distinct from repository display name, URL or Git revision"}
	revisionField := core.FieldHint{Name: "revision_sha", Type: "string", Example: "40-hex commit SHA", Description: "Exact commit returned by resolve_source_revision"}
	tool.RegisterCapability(core.Capability{
		Name: "resolve_source_revision", Description: "Resolves an allowlisted named ref or exact commit to immutable commit and tree SHAs. It also reports whether the repository profile is ready for autonomous assessment.",
		InputTypes: []string{"json"}, OutputTypes: []string{"json"}, Handler: tool.handleResolveSourceRevision,
		InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{repositoryField}, OptionalFields: []core.FieldHint{{Name: "revision", Type: "string", Example: "main", Description: "Allowlisted named ref or exact 40-hex commit; defaults to the profile ref"}}},
		OutputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			{Name: "repository_id", Type: "string"}, {Name: "repository", Type: "string"},
			{Name: "requested_revision", Type: "string"}, {Name: "revision_sha", Type: "string"},
			{Name: "tree_sha", Type: "string"}, {Name: "source_profile_sha256", Type: "string"}, {Name: "assessment_ready", Type: "boolean"},
			{Name: "resolved_at", Type: "string"}, {Name: "limitations", Type: "array"},
			{Name: "limits", Type: "object", Description: "Repository-specific numeric bounds, including maximum_read_bytes, maximum_tree_results, and maximum_search_results; keep later calls within these limits"},
		}},
	})
	tool.RegisterCapability(core.Capability{
		Name: "list_source_tree", Description: "Lists one bounded sorted page of the authorized repository tree at an exact commit. Denied credential and policy paths are excluded and coverage is explicit.",
		InputTypes: []string{"json"}, OutputTypes: []string{"json"}, Handler: tool.handleListSourceTree,
		InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{repositoryField, revisionField}, OptionalFields: []core.FieldHint{
			{Name: "path_prefix", Type: "string", Example: "internal/order"}, {Name: "after", Type: "string", Description: "Last path from the previous page"}, {Name: "limit", Type: "number", Description: "Omit to use the repository default; otherwise 1 through limits.maximum_tree_results returned by resolve_source_revision"},
		}},
		OutputSummary: sourceOutputSummary("tree_sha", "entries", "returned_count", "truncated", "coverage", "collected_at"),
	})
	tool.RegisterCapability(core.Capability{
		Name: "read_source_file", Description: "Reads one bounded UTF-8 source slice from an allowed path at an exact commit and returns full-file content identity. Binary and denied files are rejected.",
		InputTypes: []string{"json"}, OutputTypes: []string{"json"}, Handler: tool.handleReadSourceFile,
		InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{repositoryField, revisionField, {Name: "path", Type: "string", Example: "service.go"}}, OptionalFields: []core.FieldHint{
			{Name: "byte_start", Type: "number", Example: "0"}, {Name: "byte_limit", Type: "number", Description: "Omit to use the repository default; otherwise 1 through limits.maximum_read_bytes returned by resolve_source_revision"},
		}},
		OutputSummary: sourceOutputSummary("tree_sha", "path", "blob_sha", "content_sha256", "byte_start", "byte_end", "total_bytes", "content", "truncated", "collected_at"),
	})
	tool.RegisterCapability(core.Capability{
		Name: "search_source", Description: "Performs bounded literal search across likely text files at an exact commit. It reports files/bytes scanned and whether coverage was complete.",
		InputTypes: []string{"json"}, OutputTypes: []string{"json"}, Handler: tool.handleSearchSource,
		InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{repositoryField, revisionField, {Name: "query", Type: "string", Example: "order_id"}}, OptionalFields: []core.FieldHint{
			{Name: "path_prefix", Type: "string"}, {Name: "case_sensitive", Type: "boolean", Example: "false"}, {Name: "limit", Type: "number", Description: "Omit to use the repository default; otherwise 1 through limits.maximum_search_results returned by resolve_source_revision"},
		}},
		OutputSummary: sourceOutputSummary("tree_sha", "matches", "returned_count", "truncated", "coverage", "collected_at"),
	})
	tool.RegisterCapability(core.Capability{
		Name: "create_source_snapshot", Description: "Creates a deterministic sanitized gzip tar snapshot from allowed regular files at an exact commit and returns a content-derived handle. It never executes repository content.",
		InputTypes: []string{"json"}, OutputTypes: []string{"json"}, Handler: tool.handleCreateSourceSnapshot,
		InputSummary:  &core.SchemaSummary{RequiredFields: []core.FieldHint{repositoryField, revisionField}},
		OutputSummary: sourceOutputSummary("snapshot_id", "repository", "source_profile_sha256", "assessment_ready", "tree_sha", "archive_sha256", "archive_bytes", "uncompressed_bytes", "file_count", "excluded_entries", "format", "created_at", "expires_at", "limitations"),
	})
	tool.RegisterCapability(core.Capability{
		Name: "get_source_snapshot", Description: "Internal workspace-only bounded retrieval of a previously materialized source snapshot.",
		Internal: true, InputTypes: []string{"json"}, OutputTypes: []string{"json"}, Handler: tool.handleGetSourceSnapshot,
		InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			{Name: "snapshot_id", Type: "string"}, {Name: "byte_start", Type: "number"}, {Name: "byte_limit", Type: "number"},
		}},
		OutputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			{Name: "snapshot_id", Type: "string"}, {Name: "repository_id", Type: "string"},
			{Name: "source_profile_sha256", Type: "string"}, {Name: "assessment_ready", Type: "boolean"},
			{Name: "revision_sha", Type: "string"}, {Name: "tree_sha", Type: "string"},
			{Name: "archive_sha256", Type: "string"},
			{Name: "format", Type: "string"}, {Name: "byte_start", Type: "number"},
			{Name: "byte_end", Type: "number"}, {Name: "total_bytes", Type: "number"},
			{Name: "content_base64", Type: "string"}, {Name: "truncated", Type: "boolean"},
		}},
	})
}

func sourceOutputSummary(fields ...string) *core.SchemaSummary {
	hints := []core.FieldHint{{Name: "repository_id", Type: "string"}, {Name: "revision_sha", Type: "string"}}
	for _, field := range fields {
		fieldType := "string"
		description := ""
		switch field {
		case "entries", "matches", "limitations":
			fieldType = "array"
		case "returned_count", "archive_bytes", "uncompressed_bytes", "file_count", "excluded_entries", "byte_start", "byte_end", "total_bytes":
			fieldType = "number"
		case "truncated", "assessment_ready":
			fieldType = "boolean"
		case "coverage":
			fieldType = "object"
		case "content_sha256":
			description = "SHA-256 of the complete file content; use this exact value as stage_candidate base_sha256 for this existing path"
		}
		hints = append(hints, core.FieldHint{Name: field, Type: fieldType, Description: description})
	}
	return &core.SchemaSummary{RequiredFields: hints}
}

func environmentOrDefault(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}
