package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/lifecycle"
)

func (agent *assessmentTool) assessmentRule(checkID string) (lifecycle.Rule, error) {
	if agent.profile.AssessmentPolicy != nil {
		for _, rule := range agent.profile.AssessmentPolicy.Rules {
			if rule.CheckID == checkID {
				return rule, nil
			}
		}
	}
	return lifecycle.Rule{}, fmt.Errorf("%w: configured assessment check is unavailable; related history does not authorize reuse", errAssessmentOrigin)
}

// Read-only receipt validation is application admission/evidence policy. This
// does not choose the planner's steps or merge outputs from unrelated tools.
func (agent *assessmentTool) assessmentFacts(ctx context.Context, checkID, observationID string) (lifecycle.Facts, error) {
	rule, err := agent.assessmentRule(checkID)
	if err != nil {
		return lifecycle.Facts{}, err
	}
	if !observationIDPattern.MatchString(observationID) {
		return lifecycle.Facts{}, fmt.Errorf("%w: observation reference is invalid", errAssessmentOrigin)
	}
	document := make(map[string]any)
	var evidence []lifecycle.EvidenceReference
	read := func(capability, root string, parameters map[string]string) error {
		raw, err := agent.evidence.Read(ctx, capability, parameters)
		if err != nil {
			return err
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var data map[string]any
		if err := decoder.Decode(&data); err != nil {
			return err
		}
		if data["profile_id"] != agent.profile.ServiceProfileID || data["profile_version"] != agent.profile.ExpectedServiceProfileVersion {
			return fmt.Errorf("%w: assessment evidence profile does not match its authorized pin", errAssessmentOrigin)
		}
		collectedValue := data["collected_at"]
		if provenance, ok := data["provenance"].(map[string]any); ok {
			collectedValue = provenance["collected_at"]
		}
		collectedText, _ := collectedValue.(string)
		collectedAt, parseErr := time.Parse(time.RFC3339Nano, collectedText)
		// The immutable profile response has no observation timestamp. Its
		// verified content pin is provenance, not a fabricated collection time.
		if capability != "get_service_profile" && (parseErr != nil || collectedAt.IsZero() || collectedAt.After(agent.now().Add(time.Minute)) || collectedAt.Before(agent.now().Add(-agent.lifecycleLimits.RemediationMaxAge))) {
			return fmt.Errorf("%w: assessment evidence collection time is invalid or stale", errAssessmentOrigin)
		}
		document[root] = data
		evidence = append(evidence, lifecycle.EvidenceReference{Capability: capability, ProfileID: agent.profile.ServiceProfileID, ProfileVersion: agent.profile.ExpectedServiceProfileVersion, PayloadSHA256: lifecycle.Digest(raw), CollectedAt: collectedAt})
		return nil
	}
	parameters := map[string]string{"profile_id": agent.profile.ServiceProfileID}
	if err := read("get_service_profile", "service", parameters); err != nil {
		return lifecycle.Facts{}, err
	}
	service := document["service"].(map[string]any)
	contracts, _ := service["contract_references"].([]any)
	if service["assessment_ready"] != true || len(contracts) == 0 {
		return lifecycle.Facts{}, fmt.Errorf("%w: service evidence is not assessment-ready", errAssessmentOrigin)
	}
	if err := read("get_service_evidence", "observation", map[string]string{"profile_id": agent.profile.ServiceProfileID, "observation_id": observationID}); err != nil {
		return lifecycle.Facts{}, err
	}
	observationEnvelope := document["observation"].(map[string]any)
	observation, ok := observationEnvelope["observation"].(map[string]any)
	if !ok || observation["observation_id"] != observationID || observation["operation"] != rule.Operation {
		return lifecycle.Facts{}, fmt.Errorf("%w: observation identity/operation does not match the assessment rule", errAssessmentOrigin)
	}
	document["observation"] = observation
	observedText, _ := observation["observed_at"].(string)
	observedAt, err := time.Parse(time.RFC3339Nano, observedText)
	if err != nil || observedAt.IsZero() || observedAt.After(agent.now().Add(time.Minute)) {
		return lifecycle.Facts{}, fmt.Errorf("%w: observation occurrence time is invalid", errAssessmentOrigin)
	}
	servingRevision, _ := observation["serving_revision"].(string)
	if servingRevision == "" {
		return lifecycle.Facts{}, fmt.Errorf("%w: observation serving revision is unknown", errAssessmentOrigin)
	}
	if reference, ok := observationEnvelope["outcome_reference"].(map[string]any); ok {
		outcomeID, _ := reference["outcome_id"].(string)
		if outcomeID == "" {
			return lifecycle.Facts{}, fmt.Errorf("%w: business outcome reference is unknown", errAssessmentOrigin)
		}
		if err := read("get_business_outcome", "outcome", map[string]string{"profile_id": agent.profile.ServiceProfileID, "outcome_id": outcomeID}); err != nil {
			return lifecycle.Facts{}, err
		}
		if document["outcome"].(map[string]any)["outcome_id"] != outcomeID {
			return lifecycle.Facts{}, fmt.Errorf("%w: business outcome identity differs from the observation reference", errAssessmentOrigin)
		}
	}
	if err := read("get_release_context", "release", map[string]string{"profile_id": agent.profile.ServiceProfileID, "expected_revision": servingRevision}); err != nil {
		return lifecycle.Facts{}, err
	}
	releaseEnvelope := document["release"].(map[string]any)
	release, ok := releaseEnvelope["release"].(map[string]any)
	if !ok || release["revision"] != servingRevision || releaseEnvelope["revision_matches"] != true {
		return lifecycle.Facts{}, fmt.Errorf("%w: current release differs from the observed serving revision", errAssessmentOrigin)
	}
	document["release"] = release
	if err := read("get_runtime_configuration", "configuration", parameters); err != nil {
		return lifecycle.Facts{}, err
	}
	configuration := document["configuration"].(map[string]any)
	if configuration["serving_revision"] != servingRevision {
		return lifecycle.Facts{}, fmt.Errorf("%w: runtime configuration does not match the observed serving revision", errAssessmentOrigin)
	}
	mandatory := make(map[string]json.RawMessage)
	for _, path := range []string{"/service/profile_version", "/service/contract_references", "/release/revision", "/release/source_sha256", "/release/git_revision", "/release/source_matches_commit", "/configuration/configuration", "/configuration/serving_revision"} {
		value, err := lifecycle.Select(document, path)
		if err != nil {
			return lifecycle.Facts{}, fmt.Errorf("%w: %v", errAssessmentOrigin, err)
		}
		mandatory[path] = value
	}
	scope := lifecycle.Scope{CoordinatorProfileID: agent.profile.CoordinatorProfileID, ServiceProfileID: agent.profile.ServiceProfileID, RepositoryID: agent.profile.RepositoryID, EnvironmentID: agent.profile.AssessmentPolicy.EnvironmentID}
	facts, err := lifecycle.BuildFacts(scope, agent.profile.AssessmentPolicy, rule, document, mandatory, observationID, observedAt, evidence, agent.lifecycleLimits.FactMaxBytes)
	if err != nil {
		return lifecycle.Facts{}, fmt.Errorf("%w: %v", errAssessmentOrigin, err)
	}
	return facts, nil
}

func (agent *assessmentTool) recordAssessment(ctx context.Context, input assessmentWriteInput) (lifecycle.Assessment, error) {
	record, err := agent.cases.Get(ctx, input.CaseID)
	if err != nil {
		return lifecycle.Assessment{}, err
	}
	if record.CoordinatorProfileID != agent.profile.CoordinatorProfileID || record.CoordinatorProfileSHA256 != agent.profile.ProfileSHA256 || record.Status != "completed" || record.Result == nil || record.Result.ExecutionID == "" || !record.Result.ExecutionSuccess {
		return lifecycle.Assessment{}, fmt.Errorf("%w: completed original case in current profile required", errAssessmentOrigin)
	}
	if input.ExpectedOriginExecutionID == "" || input.ExpectedOriginExecutionID != record.Result.ExecutionID {
		return lifecycle.Assessment{}, errAssessmentIntent
	}
	switch record.Result.Status {
	case "benign", "draft_pr_opened":
	default:
		return lifecycle.Assessment{}, errAssessmentOrigin
	}
	var belongs bool
	for _, reference := range record.ObservationRefs {
		if reference == input.ObservationID {
			belongs = true
		}
	}
	if !belongs {
		return lifecycle.Assessment{}, errAssessmentOrigin
	}
	facts, err := agent.assessmentFacts(ctx, input.CheckID, input.ObservationID)
	if err != nil {
		return lifecycle.Assessment{}, err
	}
	if err := validateAssessmentWriteIntent(input, record.Result.ExecutionID, facts); err != nil {
		return lifecycle.Assessment{}, err
	}
	var remediations []lifecycle.RemediationReference
	ids := make([]string, 0, len(record.PublicationEffects))
	for id := range record.PublicationEffects {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	for _, id := range ids {
		effect := record.PublicationEffects[id]
		p := effect.Publication
		remediations = append(remediations, lifecycle.RemediationReference{PublicationID: p.PublicationID, OriginCaseID: p.CaseID, OriginExecutionID: effect.OriginExecutionID, PublicationProfileID: p.ProfileID, PublicationProfileSHA256: p.ProfileSHA256, PullRequestURL: p.PullRequestURL})
	}
	if record.Result.Status == "draft_pr_opened" && len(remediations) == 0 {
		return lifecycle.Assessment{}, fmt.Errorf("%w: publication disposition has no retained effect reference", errAssessmentOrigin)
	}
	now := agent.now()
	assessment := lifecycle.Assessment{SchemaVersion: 1, OriginCaseID: record.CaseID, OriginExecutionID: record.Result.ExecutionID, Disposition: record.Result.Status, Summary: record.Result.Summary, Facts: facts, Remediations: remediations, AssessedAt: now, RevalidateAfter: now.Add(agent.lifecycleLimits.RevalidateAfter)}
	if input.ExpectedIssueVersion == nil {
		return lifecycle.Assessment{}, errAssessmentOrigin
	}
	return agent.assessments.Put(ctx, assessment, *input.ExpectedIssueVersion)
}

func validateAssessmentWriteIntent(input assessmentWriteInput, executionID string, facts lifecycle.Facts) error {
	if input.ExpectedOriginExecutionID == "" || input.ExpectedOriginExecutionID != executionID || input.ExpectedIssueID != lifecycle.IssueID(facts) || input.ExpectedFactsDigest == "" || input.ExpectedFactsDigest != facts.ComparedFactsDigest {
		return errAssessmentIntent
	}
	return nil
}

func (agent *assessmentTool) findAssessments(ctx context.Context, input assessmentLookupInput) (lifecycle.Lookup, error) {
	records, truncated, err := agent.assessments.List(ctx, input.CheckID, agent.lifecycleLimits.PageLimit)
	if err != nil {
		return lifecycle.Lookup{}, err
	}
	result := lifecycle.Lookup{CoordinatorProfileID: agent.profile.CoordinatorProfileID, CoordinatorProfileSHA256: agent.profile.ProfileSHA256,
		ObservationID: input.ObservationID, CheckID: input.CheckID, PolicyConfigured: agent.profile.AssessmentPolicy != nil,
		Candidates: []lifecycle.Match{}, Truncated: truncated, CheckedAt: agent.now(), Limitations: []string{"Related history alone does not authorize admission suppression or transfer another case's review/publication approval."}}
	var facts lifecycle.Facts
	var factErr error
	if input.ObservationID != "" && input.CheckID != "" && result.PolicyConfigured {
		facts, factErr = agent.assessmentFacts(ctx, input.CheckID, input.ObservationID)
	}
	if factErr != nil {
		result.Limitations = append(result.Limitations, "Current comparison facts are unavailable: "+factErr.Error())
	}
	if factErr == nil && facts.ObservationID != "" {
		version, err := agent.assessments.IssueVersion(ctx, facts)
		if err != nil {
			return lifecycle.Lookup{}, err
		}
		result.CurrentIssueID, result.CurrentIssueVersion = lifecycle.IssueID(facts), &version
		result.CurrentFactsDigest = facts.ComparedFactsDigest
	}
	for _, record := range records {
		match := lifecycle.Compare(record, facts, agent.profile.AssessmentPolicy, result.CheckedAt, agent.lifecycleLimits.RevalidateAfter)
		if factErr != nil || facts.ObservationID == "" {
			match.Eligible, match.Reason = false, "facts_unknown"
		}
		if agent.profile.AssessmentPolicy == nil {
			match.Reason = "policy_unconfigured"
		}
		if match.Eligible {
			current, err := agent.assessments.Current(ctx, record)
			if err != nil {
				return lifecycle.Lookup{}, fmt.Errorf("read current assessment version: %w", err)
			}
			if !current {
				match.Eligible, match.Reason = false, "assessment_superseded"
			}
		}
		result.Candidates = append(result.Candidates, match)
		encoded, err := json.Marshal(result)
		if err != nil {
			return lifecycle.Lookup{}, err
		}
		// The derived lookup is whole-record bounded. Raw immutable records
		// remain available through get_assessment; no partial facts are trusted.
		checks, _ := json.Marshal(agent.assessmentChecks())
		if len(encoded) > agent.lifecycleLimits.LookupMaxBytes-len(checks)-1024 {
			result.Candidates = result.Candidates[:len(result.Candidates)-1]
			result.Truncated = true
			result.Limitations = append(result.Limitations, "Lookup byte limit reached; use get_assessment for exact records and narrower check_id for history.")
			break
		}
	}
	return result, nil
}
