package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/truvaagents/truva-g3/core"
)

type GatewayTool struct {
	*core.BaseTool
	store      *switchableKnowledgeStore
	controlKey []byte
}

func NewGatewayTool() (*GatewayTool, error) {
	controlKey := os.Getenv("CONTROL_PROOF_HMAC_KEY")
	if len(controlKey) < 32 {
		return nil, fmt.Errorf("CONTROL_PROOF_HMAC_KEY must contain at least 32 characters")
	}
	packPath := os.Getenv("DOMAIN_PACK_PATH")
	if packPath == "" {
		packPath = "/app/domain-packs/commerce-demo/pack.json"
	}
	packRoot := os.Getenv("DOMAIN_PACK_ROOT")
	if packRoot == "" {
		packRoot = filepath.Dir(filepath.Dir(filepath.Clean(packPath)))
	}
	initialPack := filepath.Base(filepath.Dir(filepath.Clean(packPath)))
	store, err := newSwitchableKnowledgeStore(packRoot, os.Getenv("KNOWLEDGE_STORE_PATH"), initialPack)
	if err != nil {
		return nil, fmt.Errorf("initialize knowledge store: %w", err)
	}
	tool := &GatewayTool{BaseTool: core.NewTool("domain-knowledge-gateway"), store: store, controlKey: []byte(controlKey)}
	tool.registerCapabilities()
	return tool, nil
}

func (t *GatewayTool) Close() error { return t.store.Close() }

func (t *GatewayTool) registerCapabilities() {
	t.RegisterCapability(core.Capability{
		Name:        "search_evidence",
		Description: "Retrieves policy or operational evidence from the active immutable domain-pack release. Applies tenant, jurisdiction, channel, and effective-date filters before hybrid BM25-style and semantic ranking, then returns source URLs and exact section identifiers. Use before making a domain decision; do not infer policy when candidates are empty.",
		InputTypes:  []string{"json"}, OutputTypes: []string{"json"}, Handler: t.handleSearchEvidence,
		InputSummary: &core.SchemaSummary{
			RequiredFields: []core.FieldHint{{Name: "query", Type: "string", Example: "late shipment customer did not consent to delay", Description: "Natural-language evidence need"}, {Name: "tenant_id", Type: "string", Example: "commerce-demo", Description: "Tenant scope"}, {Name: "jurisdiction", Type: "string", Example: "US", Description: "Jurisdiction hard filter"}, {Name: "channel", Type: "string", Example: "online", Description: "Evidence-applicability channel hard filter; choose the activity being evaluated, which may differ from the request intake or transaction channel"}, {Name: "as_of", Type: "string", Example: "2026-08-20T18:00:00Z", Description: "RFC3339 decision time"}},
			OptionalFields: []core.FieldHint{
				{Name: "release_id", Type: "string", Example: "commerce-us-2026-08-20", Description: "Immutable release; omit for active release"},
				{Name: "tags", Type: "array", Example: `["shipping-delay","refund"]`, Description: "Optional OR-match controlled-tag filter. Spaces, underscores, and hyphens are normalized; omit when the pack vocabulary is unknown"},
				{Name: "limit", Type: "integer", Example: "8", Description: "Maximum evidence candidates, 1-20"},
			},
		},
		OutputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			{Name: "release", Type: "object", Description: "Release ID, hash, validity, source count, and section count"},
			{Name: "candidates", Type: "array", Description: "Ranked evidence with section, source URL, retrieval method, score, and matched terms"},
		}, OptionalFields: []core.FieldHint{
			{Name: "paths", Type: "array", Description: "One-hop relations from ranked sections to exceptions, authority, or procedures; omitted when none exist"},
			{Name: "warnings", Type: "array", Description: "Retrieval warnings, including an empty scoped result"},
		}},
	})

	t.RegisterCapability(core.Capability{
		Name:        "traverse_relations",
		Description: "Traverses explicit relations between already-identified evidence sections. Use to find exceptions, authority, prerequisites, or implementing procedures after search_evidence. This does not perform open-ended graph queries.",
		InputTypes:  []string{"json"}, OutputTypes: []string{"json"}, Handler: t.handleTraverseRelations,
		InputSummary: &core.SchemaSummary{
			RequiredFields: []core.FieldHint{{Name: "section_ids", Type: "array", Example: `["ftc-delay-consent"]`, Description: "Starting exact section identifiers"}, {Name: "tenant_id", Type: "string", Description: "Tenant scope"}, {Name: "jurisdiction", Type: "string", Description: "Jurisdiction scope"}, {Name: "channel", Type: "string", Description: "Channel scope"}, {Name: "as_of", Type: "string", Description: "RFC3339 decision time"}},
			OptionalFields: []core.FieldHint{{Name: "release_id", Type: "string", Description: "Immutable release ID"}, {Name: "relation_types", Type: "array", Example: `["REQUIRES","OVERRIDES"]`, Description: "Optional relation allow-list"}, {Name: "max_depth", Type: "integer", Example: "1", Description: "Depth 1-3"}, {Name: "limit", Type: "integer", Example: "20", Description: "Maximum paths"}},
		},
		OutputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{{Name: "paths", Type: "array", Description: "Typed from/relation/to evidence paths with depth"}}},
	})

	t.RegisterCapability(core.Capability{
		Name:        "read_exact_section",
		Description: "Reads one exact, effective, in-scope section from one immutable release and returns a signed evidence_receipt. Commerce Core accepts a citation only when this receipt proves the exact read occurred.",
		InputTypes:  []string{"json"}, OutputTypes: []string{"json"}, Handler: t.handleReadExactSection,
		InputSummary:  &core.SchemaSummary{RequiredFields: []core.FieldHint{{Name: "section_id", Type: "string", Example: "ftc-delay-consent", Description: "Exact section identifier"}, {Name: "tenant_id", Type: "string", Description: "Tenant scope"}, {Name: "jurisdiction", Type: "string", Description: "Jurisdiction scope"}, {Name: "channel", Type: "string", Description: "Channel scope"}, {Name: "as_of", Type: "string", Description: "RFC3339 decision time"}}, OptionalFields: []core.FieldHint{{Name: "release_id", Type: "string", Description: "Immutable release ID"}}},
		OutputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{{Name: "release", Type: "object", Description: "Release provenance"}, {Name: "section", Type: "object", Description: "Exact normalized section"}, {Name: "source", Type: "object", Description: "Authoritative source metadata and URL"}, {Name: "evidence_receipt", Type: "string", Description: "Signed proof of this scoped exact read"}}},
	})

	t.RegisterCapability(core.Capability{
		Name:        "get_release_info",
		Description: "Returns the active or requested immutable domain-pack release, including its SHA-256 content hash, validity interval, domain, tenant, and corpus counts. Use for provenance and freshness checks.",
		InputTypes:  []string{"json"}, OutputTypes: []string{"json"}, Handler: t.handleGetReleaseInfo,
		InputSummary: &core.SchemaSummary{OptionalFields: []core.FieldHint{{Name: "release_id", Type: "string", Description: "Release to inspect; omit for active"}}},
		OutputSummary: &core.SchemaSummary{
			RequiredFields: []core.FieldHint{
				{Name: "release_id", Type: "string", Description: "Immutable identifier"},
				{Name: "domain", Type: "string", Description: "Domain identifier supplied by the pack"},
				{Name: "tenant_id", Type: "string", Description: "Tenant scope supplied by the pack"},
				{Name: "published_at", Type: "string", Description: "Release publication timestamp"},
				{Name: "valid_from", Type: "string", Description: "Release validity start"},
				{Name: "content_hash", Type: "string", Description: "SHA-256 of canonical pack"},
				{Name: "section_count", Type: "integer", Description: "Normalized evidence sections"},
				{Name: "source_count", Type: "integer", Description: "Source documents represented by the release"},
			},
			OptionalFields: []core.FieldHint{{Name: "valid_to", Type: "string", Description: "Release validity end when bounded"}},
		},
	})

	t.RegisterCapability(core.Capability{
		Name:        "manage_demo_domain_pack",
		Description: "Development-only control for inspecting or activating an allow-listed immutable domain pack. Hidden from LLM planning.",
		Internal:    true,
		InputTypes:  []string{"json"}, OutputTypes: []string{"json"}, Handler: t.handleManageDemoDomainPack,
		InputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			{Name: "action", Type: "string", Example: "status", Description: "status or activate"},
		}, OptionalFields: []core.FieldHint{{Name: "pack_name", Type: "string", Example: "it-access-demo", Description: "Allow-listed pack to activate"}}},
		OutputSummary: &core.SchemaSummary{RequiredFields: []core.FieldHint{
			{Name: "action", Type: "string", Description: "Applied control action"},
			{Name: "active_pack", Type: "string", Description: "Current pack name"},
			{Name: "release", Type: "object", Description: "Current immutable release identity"},
			{Name: "available_packs", Type: "array", Description: "Allow-listed pack names"},
		}},
	})
}
