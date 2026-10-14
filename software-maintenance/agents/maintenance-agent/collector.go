package main

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"sort"
	"time"

	requestobs "github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/observation"
	"github.com/truvaagents/truva-g3/core"
)

func (agent *maintenanceAgent) handleCollectionTask(ctx context.Context, task *core.Task, reporter core.ProgressReporter) error {
	if task == nil || task.Type != core.ScheduledTaskType || task.TargetAgent != maintenanceAgentName {
		return errors.New("scheduled collection task identity is invalid")
	}
	flow, _ := task.Input["flow"].(string)
	coordinatorProfile, _ := task.Input["coordinator_profile_id"].(string)
	coordinatorSHA256, _ := task.Input["coordinator_profile_sha256"].(string)
	serviceProfile, _ := task.Input["service_profile_id"].(string)
	if flow != collectionFlow || coordinatorProfile != agent.profile.CoordinatorProfileID ||
		coordinatorSHA256 != agent.profile.ProfileSHA256 || serviceProfile != agent.profile.ServiceProfileID {
		return errors.New("scheduled collection task scope is invalid")
	}
	if !agent.profile.Collection.Enabled {
		task.Result = collectionResult{
			SchemaVersion: 1, ProfileID: agent.profile.CoordinatorProfileID,
			Limitations: []string{"owner-configured automatic collection is disabled"},
		}
		return nil
	}
	_ = reporter.Report(&core.TaskProgress{CurrentStep: 1, TotalSteps: 3, StepName: "validate_profile", Percentage: 10})
	var service serviceProfileResponse
	calls := 1
	if err := agent.callEvidenceCapability(ctx, "get_service_profile", map[string]string{"profile_id": agent.profile.ServiceProfileID}, &service); err != nil {
		return err
	}
	if err := agent.validateCollectionServiceProfile(service); err != nil {
		return err
	}
	if !service.AssessmentReady {
		task.Result = collectionResult{
			SchemaVersion: 1, ProfileID: agent.profile.CoordinatorProfileID, Requests: calls,
			AssessmentReady: false,
			Limitations: normalizeCoordinatorStrings(
				append([]string{"automatic admission is blocked because the service profile is not assessment-ready"}, service.Limitations...), 32,
			),
		}
		return nil
	}
	now := agent.now()
	window, limitations, err := agent.collectionWindow(ctx, service, now)
	if err != nil {
		return err
	}
	_ = reporter.Report(&core.TaskProgress{CurrentStep: 2, TotalSteps: 3, StepName: "collect_observations", Percentage: 35})
	observations, enumerable, sourceComplete, err := agent.collectObservationRange(ctx, service, window.Start, window.End, &calls)
	if err != nil {
		return err
	}
	selected := make([]observationSummary, 0, len(observations))
	for _, observation := range observations {
		if agent.selectObservation(observation) {
			selected = append(selected, observation)
		}
	}
	result := collectionResult{
		SchemaVersion: 1, ProfileID: agent.profile.CoordinatorProfileID, Window: window,
		Requests: calls, Observed: len(observations), Selected: len(selected),
		CoverageComplete: sourceComplete && enumerable, AssessmentReady: true,
		Limitations: limitations,
	}
	result.SampledOut = len(observations) - len(selected)
	result.RunID = agent.admission.RunID
	result.Deferred = map[string]string{}
	if !sourceComplete {
		result.Limitations = append(result.Limitations, "the source declares the observation feed advisory rather than complete")
	}
	if !enumerable {
		result.Limitations = append(result.Limitations, "the bounded request budget could not fully enumerate a truncated window; the watermark was not advanced")
	}
	_ = reporter.Report(&core.TaskProgress{CurrentStep: 3, TotalSteps: 3, StepName: "admit_cases", Percentage: 70})
	admissionFailed := false
	for _, observation := range selected {
		input := investigationInput{
			ProfileID: agent.profile.CoordinatorProfileID,
			CaseID:    observationCaseID(agent.profile.ProfileSHA256, observation.ObservationID),
			Objective: observationObjective(observation.ObservationID), ObservationRefs: []string{observation.ObservationID},
		}
		admission, err := agent.admitInvestigation(ctx, input, "scheduled_collection", observation)
		if err != nil {
			admissionFailed = true
			var deferred *admissionDeferred
			if errors.As(err, &deferred) {
				result.Deferred[observation.ObservationID] = deferred.Reason
				continue
			}
			agent.Logger.WarnWithContext(ctx, "Observation admission failed", map[string]interface{}{
				"operation": "observation_admission", "request_id": requestobs.ContextRequestID(ctx), "error_type": "admission_failed", "observation_id": observation.ObservationID,
				"case_id": input.CaseID, "error": core.RedactSensitiveError(err).Error(),
			})
			continue
		}
		if admission.Reused {
			result.Reused++
		} else {
			result.Admitted++
		}
	}
	if agent.Logger != nil {
		agent.Logger.InfoWithContext(ctx, "Observation collection disposition recorded", map[string]interface{}{
			"operation": "observation_collection", "request_id": requestobs.ContextRequestID(ctx), "run_id": result.RunID, "observed": result.Observed,
			"selected": result.Selected, "admitted": result.Admitted, "reused": result.Reused,
			"sampled_out": result.SampledOut, "deferred": len(result.Deferred), "coverage_complete": result.CoverageComplete,
		})
	}
	if admissionFailed {
		result.Limitations = append(result.Limitations, "one or more selected observations could not be durably admitted; the watermark was not advanced")
	}
	if enumerable && !admissionFailed {
		watermarkIdentity := agent.profile.CoordinatorProfileID + ":" + agent.profile.ProfileSHA256
		if err := agent.cases.setWatermark(ctx, watermarkIdentity, window.End); err != nil {
			return err
		}
		result.WatermarkAdvanced = true
		result.NextWatermark = window.End
	}
	result.Limitations = normalizeCoordinatorStrings(result.Limitations, 32)
	task.Result = result
	return nil
}

func (agent *maintenanceAgent) validateCollectionServiceProfile(service serviceProfileResponse) error {
	policy := agent.profile.Collection
	if service.ProfileID != agent.profile.ServiceProfileID || service.ProfileVersion != agent.profile.ExpectedServiceProfileVersion {
		return errors.New("evidence service profile identity does not match coordinator policy")
	}
	if service.AssessmentReady && len(service.ContractReferences) == 0 {
		return errors.New("assessment-ready evidence profile has no authoritative contract references")
	}
	constraints := service.ObservationConstraints
	if constraints.MaximumWindowSeconds < policy.WindowSeconds || constraints.MaximumItems < policy.ItemLimit || constraints.RetentionSeconds < policy.WindowSeconds {
		return errors.New("evidence service profile cannot satisfy the bounded collection policy")
	}
	return nil
}

func (agent *maintenanceAgent) collectionWindow(ctx context.Context, service serviceProfileResponse, now time.Time) (observationWindow, []string, error) {
	watermarkIdentity := agent.profile.CoordinatorProfileID + ":" + agent.profile.ProfileSHA256
	watermark, err := agent.cases.getWatermark(ctx, watermarkIdentity)
	if err != nil {
		return observationWindow{}, nil, err
	}
	policy := agent.profile.Collection
	end := now
	start := now.Add(-time.Duration(policy.WindowSeconds) * time.Second)
	if !watermark.IsZero() {
		start = watermark.Add(-time.Duration(policy.OverlapSeconds) * time.Second)
	}
	if agent.admission.Enabled {
		budget, err := agent.updateAdmissionBudget(ctx, func(*admissionBudget) error { return nil })
		if err != nil {
			return observationWindow{}, nil, err
		}
		if start.Before(budget.StartedAt) {
			start = budget.StartedAt
		}
	}
	limitations := []string{}
	retentionFloor := now.Add(-time.Duration(service.ObservationConstraints.RetentionSeconds) * time.Second)
	if start.Before(retentionFloor) {
		start = retentionFloor
		limitations = append(limitations, "collection watermark preceded source retention; the unavailable interval is recorded as a coverage gap")
	}
	// Apply the query bound after both lower-bound adjustments. A held watermark
	// must catch up in bounded windows rather than expand the run-start window.
	if boundedEnd := start.Add(time.Duration(policy.WindowSeconds) * time.Second); boundedEnd.Before(end) {
		end = boundedEnd
	}
	if !start.Before(end) {
		return observationWindow{}, nil, errors.New("collection window is empty after applying retention and watermark bounds")
	}
	return observationWindow{Start: start, End: end}, limitations, nil
}

func (agent *maintenanceAgent) collectObservationRange(ctx context.Context, service serviceProfileResponse, start, end time.Time, calls *int) ([]observationSummary, bool, bool, error) {
	if *calls >= agent.profile.Limits.MaximumCollectionCalls {
		return []observationSummary{}, false, false, nil
	}
	*calls = *calls + 1
	var response observationsResponse
	err := agent.callEvidenceCapability(ctx, "list_service_observations", map[string]interface{}{
		"profile_id": agent.profile.ServiceProfileID,
		"start":      start.UTC().Format(time.RFC3339Nano), "end": end.UTC().Format(time.RFC3339Nano),
		"limit": agent.profile.Collection.ItemLimit,
	}, &response)
	if err != nil {
		return nil, false, false, err
	}
	if err := validateObservationsResponse(agent.profile, service, response, start, end); err != nil {
		return nil, false, false, err
	}
	if !response.Truncated {
		return deduplicateObservations(response.Observations), true, response.Coverage.Complete, nil
	}
	if end.Sub(start) < 2*time.Second || *calls+2 > agent.profile.Limits.MaximumCollectionCalls {
		return deduplicateObservations(response.Observations), false, response.Coverage.Complete, nil
	}
	midpoint := start.Add(end.Sub(start) / 2)
	left, leftEnumerable, leftComplete, err := agent.collectObservationRange(ctx, service, start, midpoint, calls)
	if err != nil {
		return nil, false, false, err
	}
	right, rightEnumerable, rightComplete, err := agent.collectObservationRange(ctx, service, midpoint, end, calls)
	if err != nil {
		return nil, false, false, err
	}
	return deduplicateObservations(append(left, right...)), leftEnumerable && rightEnumerable, leftComplete && rightComplete, nil
}

func validateObservationsResponse(profile coordinatorProfile, service serviceProfileResponse, response observationsResponse, start, end time.Time) error {
	if response.ProfileID != profile.ServiceProfileID || response.ProfileVersion != service.ProfileVersion ||
		response.ReturnedCount != len(response.Observations) || response.ReturnedCount > profile.Collection.ItemLimit ||
		response.Window.Start.After(response.Window.End) || !response.Window.Start.Equal(start) || !response.Window.End.Equal(end) ||
		response.CollectedAt.IsZero() || response.CollectedAt.Before(end) || response.CollectedAt.After(time.Now().UTC().Add(time.Minute)) {
		return errors.New("observation response identity or bounds are invalid")
	}
	for _, observation := range response.Observations {
		if !observationIDPattern.MatchString(observation.ObservationID) || observation.Operation == "" || len(observation.Operation) > 256 || !validCoordinatorText(observation.Operation) ||
			observation.ObservedAt.Before(start) || observation.ObservedAt.After(end) || observation.HTTPStatus < 100 || observation.HTTPStatus > 599 ||
			(observation.ErrorCode != "" && (len(observation.ErrorCode) > 256 || !validCoordinatorText(observation.ErrorCode))) {
			return errors.New("observation response contains invalid evidence")
		}
	}
	return nil
}

func deduplicateObservations(values []observationSummary) []observationSummary {
	byID := make(map[string]observationSummary, len(values))
	for _, value := range values {
		byID[value.ObservationID] = value
	}
	result := make([]observationSummary, 0, len(byID))
	for _, value := range byID {
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ObservedAt.Equal(result[j].ObservedAt) {
			return result[i].ObservationID < result[j].ObservationID
		}
		return result[i].ObservedAt.Before(result[j].ObservedAt)
	})
	return result
}

func (agent *maintenanceAgent) selectObservation(observation observationSummary) bool {
	if observation.HTTPStatus < 200 || observation.HTTPStatus >= 300 || observation.ErrorCode != "" {
		return true
	}
	digest := sha256.Sum256([]byte(observation.ObservationID))
	value := binary.BigEndian.Uint64(digest[:8])
	return value%agent.profile.Collection.SuccessfulSampleModulus == 0
}

func observationObjective(observationID string) string {
	return "Assess recorded service observation " + observationID +
		" against current owner-approved contracts and evidence without assuming whether it is benign or defective. " +
		"Seek an evidence-supported disposition and, when warranted and authorized, a safe draft change. Report unresolved uncertainty and blockers honestly."
}
