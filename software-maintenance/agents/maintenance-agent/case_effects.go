package main

import (
	"context"
	"errors"
	"time"

	"github.com/truvaagents/truva-g3/orchestration"
)

func retainPublicationEffect(record *caseRecord, publication publicationStepResponse, executionID string, now time.Time) error {
	if publication.PublicationID == "" || publication.CaseID != record.CaseID || publication.Status != "succeeded" || !publication.Draft || publication.PullRequestURL == "" || executionID == "" {
		return errors.New("publication effect lacks its original case/execution evidence")
	}
	if previous, ok := record.PublicationEffects[publication.PublicationID]; ok {
		comparable := previous.Publication
		// Legacy locators lack profile authority. Enrich only those absent
		// fields, preserving every previously retained publication fact.
		if comparable.ProfileID == "" {
			comparable.ProfileID = publication.ProfileID
		}
		if comparable.ProfileSHA256 == "" {
			comparable.ProfileSHA256 = publication.ProfileSHA256
		}
		if publication.ProfileID == "" {
			publication.ProfileID = comparable.ProfileID
		}
		if publication.ProfileSHA256 == "" {
			publication.ProfileSHA256 = comparable.ProfileSHA256
		}
		if comparable != publication {
			return errors.New("publication effect identity conflicts with retained evidence")
		}
		previous.Publication = comparable
		for _, id := range previous.ObservedExecutionIDs {
			if id == executionID {
				return nil
			}
		}
		previous.ObservedExecutionIDs = append(previous.ObservedExecutionIDs, executionID)
		record.PublicationEffects[publication.PublicationID] = previous
		return nil
	}
	if record.PublicationEffects == nil {
		record.PublicationEffects = make(map[string]casePublicationEffect)
	}
	record.PublicationEffects[publication.PublicationID] = casePublicationEffect{
		Publication: publication, OriginExecutionID: executionID, ObservedExecutionIDs: []string{executionID}, RecordedAt: now.UTC(),
	}
	return nil
}

func retainLegacyPublicationEffects(record *caseRecord, now time.Time) error {
	if evidence := record.ValidationEvidence; evidence != nil && evidence.Publication != nil {
		if err := retainPublicationEffect(record, *evidence.Publication, evidence.ExecutionID, now); err != nil {
			return err
		}
	}
	if result := record.Result; result != nil && result.PublicationID != "" && result.ExecutionID != "" {
		publication := publicationStepResponse{
			PublicationID: result.PublicationID, CaseID: record.CaseID,
			Status: "succeeded", Draft: true,
			PullRequestURL: result.PullRequestURL,
		}
		// The old result did not retain the publication profile ID/hash. Do not
		// invent them from the coordinator hash. A minimal legacy locator stays
		// explicitly incomplete until an attributable successful response exists.
		if _, already := record.PublicationEffects[publication.PublicationID]; !already {
			return retainPublicationEffect(record, publication, result.ExecutionID, now)
		}
	}
	return nil
}

func (store *caseStore) retainExecutionEffects(ctx context.Context, caseID, taskID, executionID string, publications []publicationStepResponse, now time.Time) error {
	if len(publications) == 0 {
		return nil
	}
	_, err := store.updateForTask(ctx, caseID, taskID, func(record *caseRecord) error {
		for _, publication := range publications {
			if err := retainPublicationEffect(record, publication, executionID, now); err != nil {
				return err
			}
		}
		return nil
	})
	return err
}

func (agent *maintenanceAgent) confirmedPublications(input investigationInput, execution *orchestration.ExecutionResult) []publicationStepResponse {
	if execution == nil {
		return nil
	}
	var result []publicationStepResponse
	for _, step := range execution.Steps {
		one := &orchestration.ExecutionResult{Steps: []orchestration.StepResult{step}}
		if publication := agent.confirmedPublication(input, one); publication != nil {
			result = append(result, *publication)
		}
	}
	return result
}
