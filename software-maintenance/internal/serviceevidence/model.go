package serviceevidence

import "time"

type profileRequest struct {
	ProfileID string `json:"profile_id"`
}

type observationsRequest struct {
	ProfileID string `json:"profile_id"`
	Start     string `json:"start"`
	End       string `json:"end"`
	Limit     int    `json:"limit,omitempty"`
}

type evidenceRequest struct {
	ProfileID     string `json:"profile_id"`
	ObservationID string `json:"observation_id"`
}

type outcomeRequest struct {
	ProfileID string `json:"profile_id"`
	OutcomeID string `json:"outcome_id"`
}

type releaseRequest struct {
	ProfileID        string `json:"profile_id"`
	ExpectedRevision string `json:"expected_revision,omitempty"`
}

type repositoryReference struct {
	Name          string `json:"name"`
	DefaultBranch string `json:"default_branch"`
}

type contractReference struct {
	Kind     string `json:"kind"`
	Revision string `json:"revision"`
	Path     string `json:"path"`
}

type observationConstraints struct {
	MaximumWindowSeconds int  `json:"maximum_window_seconds"`
	RetentionSeconds     int  `json:"retention_seconds"`
	MaximumItems         int  `json:"maximum_items"`
	CompleteStream       bool `json:"complete_stream"`
}

type serviceProfileResponse struct {
	ProfileID              string                 `json:"profile_id"`
	ProfileVersion         string                 `json:"profile_version"`
	AssessmentReady        bool                   `json:"assessment_ready"`
	ServiceName            string                 `json:"service_name"`
	Repository             repositoryReference    `json:"repository"`
	ContractReferences     []contractReference    `json:"contract_references"`
	SupportedEvidence      []string               `json:"supported_evidence"`
	ObservationConstraints observationConstraints `json:"observation_constraints"`
	Limitations            []string               `json:"limitations"`
}

type observationWindow struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
}

type upstreamCandidate struct {
	ObservationID string    `json:"observation_id"`
	Operation     string    `json:"operation"`
	At            time.Time `json:"at"`
	HTTPStatus    int       `json:"http_status"`
	ErrorCode     string    `json:"error_code,omitempty"`
}

type upstreamFeed struct {
	Window     observationWindow   `json:"window"`
	Candidates []upstreamCandidate `json:"candidates"`
	Truncated  bool                `json:"truncated"`
}

type observationSummary struct {
	ObservationID string    `json:"observation_id"`
	Operation     string    `json:"operation"`
	ObservedAt    time.Time `json:"observed_at"`
	HTTPStatus    int       `json:"http_status"`
	ErrorCode     string    `json:"error_code,omitempty"`
}

type coverage struct {
	Source           string `json:"source"`
	Complete         bool   `json:"complete"`
	RetentionSeconds int    `json:"retention_seconds"`
}

type observationsResponse struct {
	ProfileID      string               `json:"profile_id"`
	ProfileVersion string               `json:"profile_version"`
	Window         observationWindow    `json:"window"`
	Observations   []observationSummary `json:"observations"`
	ReturnedCount  int                  `json:"returned_count"`
	Truncated      bool                 `json:"truncated"`
	Coverage       coverage             `json:"coverage"`
	CollectedAt    time.Time            `json:"collected_at"`
}

type upstreamReceipt struct {
	RequestedPromotionCode *string   `json:"requested_promotion_code,omitempty"`
	ObservationID          string    `json:"observation_id"`
	OrderID                string    `json:"order_id"`
	Operation              string    `json:"operation"`
	At                     time.Time `json:"at"`
	Revision               string    `json:"revision"`
	OrderRevision          string    `json:"order_revision,omitempty"`
	TraceID                string    `json:"target_trace_id"`
	HTTPStatus             int       `json:"http_status"`
	ErrorCode              string    `json:"error_code,omitempty"`
	SKU                    string    `json:"sku"`
	UnitPrice              int       `json:"unit_price_cents"`
	Quantity               int       `json:"quantity"`
	Discount               int       `json:"discount_cents"`
	Currency               string    `json:"currency"`
	LedgerTotal            *int      `json:"ledger_total_cents,omitempty"`
	LedgerWrites           *int      `json:"ledger_writes,omitempty"`
}

type evidenceObservation struct {
	RequestedPromotionCode *string   `json:"requested_promotion_code,omitempty"`
	ObservationID          string    `json:"observation_id"`
	OrderID                string    `json:"order_id"`
	Operation              string    `json:"operation"`
	ObservedAt             time.Time `json:"observed_at"`
	ServingRevision        string    `json:"serving_revision"`
	OrderRevision          string    `json:"order_revision,omitempty"`
	TargetTraceID          string    `json:"target_trace_id"`
	HTTPStatus             int       `json:"http_status"`
	ErrorCode              string    `json:"error_code,omitempty"`
	SKU                    string    `json:"sku"`
	UnitPrice              int       `json:"unit_price_cents"`
	Quantity               int       `json:"quantity"`
	Discount               int       `json:"discount_cents"`
	Currency               string    `json:"currency"`
	LedgerTotal            *int      `json:"ledger_total_cents,omitempty"`
	LedgerWrites           *int      `json:"ledger_writes,omitempty"`
}

type outcomeReference struct {
	OutcomeID string `json:"outcome_id"`
	Kind      string `json:"kind"`
}

type provenance struct {
	Source      string    `json:"source"`
	CollectedAt time.Time `json:"collected_at"`
}

type evidenceResponse struct {
	ProfileID        string              `json:"profile_id"`
	ProfileVersion   string              `json:"profile_version"`
	Observation      evidenceObservation `json:"observation"`
	OutcomeReference *outcomeReference   `json:"outcome_reference,omitempty"`
	Provenance       provenance          `json:"provenance"`
	Limitations      []string            `json:"limitations"`
}

type checkoutRequest struct {
	OrderID       string `json:"order_id"`
	SKU           string `json:"sku"`
	Quantity      int    `json:"quantity"`
	PromotionCode string `json:"promotion_code,omitempty"`
}

type upstreamLedger struct {
	Total  int `json:"total"`
	Writes int `json:"writes"`
}

type upstreamOrder struct {
	Request       checkoutRequest `json:"request"`
	RequestSHA256 string          `json:"request_sha256"`
	Receipt       upstreamReceipt `json:"receipt"`
	Ledger        upstreamLedger  `json:"ledger"`
}

type pricingReceipt struct {
	OrderRevision string `json:"order_revision"`
	UnitPrice     int    `json:"unit_price_cents"`
	Discount      int    `json:"discount_cents"`
	Currency      string `json:"currency"`
}

type outcomeLedger struct {
	TotalCents int `json:"total_cents"`
	Writes     int `json:"writes"`
}

type outcomeResponse struct {
	ProfileID      string          `json:"profile_id"`
	ProfileVersion string          `json:"profile_version"`
	OutcomeID      string          `json:"outcome_id"`
	Request        checkoutRequest `json:"request"`
	RequestSHA256  string          `json:"request_sha256"`
	PricingReceipt pricingReceipt  `json:"pricing_receipt"`
	Ledger         outcomeLedger   `json:"ledger"`
	Provenance     provenance      `json:"provenance"`
}

type buildInfo struct {
	SchemaVersion       int    `json:"schema_version"`
	Repository          string `json:"repository"`
	Revision            string `json:"revision"`
	SourceSHA256        string `json:"source_sha256"`
	GitRevision         string `json:"git_revision"`
	SourceMatchesCommit bool   `json:"source_matches_commit"`
	FrameworkVersion    string `json:"framework_version"`
	PodName             string `json:"pod_name,omitempty"`
	Namespace           string `json:"namespace,omitempty"`
	PodUID              string `json:"pod_uid,omitempty"`
}

type releaseResponse struct {
	ProfileID        string    `json:"profile_id"`
	ProfileVersion   string    `json:"profile_version"`
	Release          buildInfo `json:"release"`
	ExpectedRevision string    `json:"expected_revision,omitempty"`
	RevisionMatches  *bool     `json:"revision_matches,omitempty"`
	CollectedAt      time.Time `json:"collected_at"`
	Limitations      []string  `json:"limitations"`
}

type product struct {
	SKU              string `json:"sku"`
	Name             string `json:"name"`
	UnitPrice        int    `json:"unit_price_cents"`
	Currency         string `json:"currency"`
	MaxOrderQuantity int    `json:"max_order_quantity"`
}

type runtimeConfiguration struct {
	Products []product `json:"products"`
}

type runtimeConfigurationResponse struct {
	ProfileID         string               `json:"profile_id"`
	ProfileVersion    string               `json:"profile_version"`
	ConfigurationType string               `json:"configuration_type"`
	ServingRevision   string               `json:"serving_revision"`
	Configuration     runtimeConfiguration `json:"configuration"`
	CollectedAt       time.Time            `json:"collected_at"`
	Applicability     string               `json:"applicability"`
	Limitations       []string             `json:"limitations"`
}
