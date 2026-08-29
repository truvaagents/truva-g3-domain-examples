package knowledge

import (
	"context"
	"time"
)

// KnowledgeStore is the private, provider-neutral persistence boundary. HTTP
// handlers and retrieval policy depend on this contract, never on LadybugDB.
type KnowledgeStore interface {
	GetReleaseInfo(ctx context.Context, releaseID string) (ReleaseInfo, error)
	LexicalSearch(ctx context.Context, spec CandidateSpec) ([]Candidate, error)
	VectorSearch(ctx context.Context, spec VectorSpec) ([]Candidate, error)
	Traverse(ctx context.Context, spec TraversalSpec) ([]EvidencePath, error)
	ReadExact(ctx context.Context, ref SectionRef) (ExactSection, error)
	Close() error
}

type DomainPack struct {
	SchemaVersion   string            `json:"schema_version"`
	Domain          string            `json:"domain"`
	TenantID        string            `json:"tenant_id"`
	SemanticAliases map[string]string `json:"semantic_aliases"`
	Release         ReleaseInfo       `json:"release"`
	Sources         []Source          `json:"sources"`
	Sections        []Section         `json:"sections"`
	Relations       []Relation        `json:"relations"`
	GoldenCases     []GoldenCase      `json:"golden_cases,omitempty"`
}

type ReleaseInfo struct {
	ReleaseID    string    `json:"release_id"`
	Domain       string    `json:"domain"`
	TenantID     string    `json:"tenant_id"`
	PublishedAt  time.Time `json:"published_at"`
	ValidFrom    time.Time `json:"valid_from"`
	ValidTo      time.Time `json:"valid_to,omitempty"`
	ContentHash  string    `json:"content_hash"`
	SectionCount int       `json:"section_count"`
	SourceCount  int       `json:"source_count"`
}

type Source struct {
	SourceID         string    `json:"source_id"`
	Title            string    `json:"title"`
	Publisher        string    `json:"publisher"`
	URL              string    `json:"url"`
	SourceType       string    `json:"source_type"`
	RetrievedAt      time.Time `json:"retrieved_at"`
	ExtractionMethod string    `json:"extraction_method"`
	LicenseNote      string    `json:"license_note"`
}

type Section struct {
	SectionID     string         `json:"section_id"`
	DocumentID    string         `json:"document_id"`
	Title         string         `json:"title"`
	Content       string         `json:"content"`
	SourceQuote   string         `json:"source_quote,omitempty"`
	Jurisdiction  string         `json:"jurisdiction"`
	Channel       string         `json:"channel"`
	EffectiveFrom time.Time      `json:"effective_from"`
	EffectiveTo   time.Time      `json:"effective_to,omitempty"`
	Priority      int            `json:"priority"`
	Tags          []string       `json:"tags"`
	Facts         map[string]any `json:"facts,omitempty"`
}

type Relation struct {
	FromSectionID string `json:"from_section_id"`
	Type          string `json:"type"`
	ToSectionID   string `json:"to_section_id"`
}

type GoldenCase struct {
	CaseID              string    `json:"case_id"`
	Query               string    `json:"query"`
	AsOf                time.Time `json:"as_of"`
	Jurisdiction        string    `json:"jurisdiction"`
	Channel             string    `json:"channel"`
	ExpectedSectionIDs  []string  `json:"expected_section_ids"`
	ForbiddenSectionIDs []string  `json:"forbidden_section_ids,omitempty"`
}

type CandidateSpec struct {
	Query        string
	ReleaseID    string
	TenantID     string
	Jurisdiction string
	Channel      string
	AsOf         time.Time
	Tags         []string
	Limit        int
}

type VectorSpec = CandidateSpec

type Candidate struct {
	Section      Section  `json:"section"`
	Source       Source   `json:"source"`
	Score        float64  `json:"score"`
	Retrieval    string   `json:"retrieval"`
	MatchedTerms []string `json:"matched_terms,omitempty"`
}

type TraversalSpec struct {
	ReleaseID     string
	TenantID      string
	Jurisdiction  string
	Channel       string
	AsOf          time.Time
	SectionIDs    []string
	RelationTypes []string
	MaxDepth      int
	Limit         int
}

type EvidencePath struct {
	From         SectionRef `json:"from"`
	RelationType string     `json:"relation_type"`
	To           SectionRef `json:"to"`
	Depth        int        `json:"depth"`
}

type SectionRef struct {
	ReleaseID    string    `json:"release_id"`
	SectionID    string    `json:"section_id"`
	TenantID     string    `json:"-"`
	Jurisdiction string    `json:"-"`
	Channel      string    `json:"-"`
	AsOf         time.Time `json:"-"`
}

type ExactSection struct {
	Release ReleaseInfo `json:"release"`
	Section Section     `json:"section"`
	Source  Source      `json:"source"`
}

type SearchResult struct {
	Release    ReleaseInfo    `json:"release"`
	Candidates []Candidate    `json:"candidates"`
	Paths      []EvidencePath `json:"paths,omitempty"`
	Warnings   []string       `json:"warnings,omitempty"`
}
