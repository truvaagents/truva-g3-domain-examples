package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/agentfailure"
	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/caseorigin"
	"github.com/truvaagents/truva-g3/core"
)

var (
	errCaseNotFound = errors.New("maintenance case not found")
	errCaseConflict = errors.New("maintenance case identity conflicts with its durable record")
)

type caseStore struct {
	client   redis.UniversalClient
	keyspace core.RedisKeyspace
}

func newCaseStore(client redis.UniversalClient, keyspace core.RedisKeyspace) (*caseStore, error) {
	if client == nil {
		return nil, errors.New("case store Redis client is required")
	}
	return &caseStore{client: client, keyspace: keyspace}, nil
}

func observationCaseID(profileSHA256, observationID string) string {
	digest := sha256.Sum256([]byte(profileSHA256 + "\x00" + observationID))
	return "case_" + hex.EncodeToString(digest[:])
}

func investigationTaskID(caseID string, attempt int) string {
	digest := sha256.Sum256([]byte(caseID))
	return "investigation_" + hex.EncodeToString(digest[:]) + "_" + strconv.Itoa(attempt)
}

func (store *caseStore) caseKey(caseID string) string {
	return caseorigin.Key(store.keyspace, caseID)
}

func (store *caseStore) watermarkKey(profileID string) string {
	digest := sha256.Sum256([]byte(profileID))
	return store.keyspace.Plain("maintenance-watermarks", hex.EncodeToString(digest[:]))
}

func (store *caseStore) get(ctx context.Context, caseID string) (caseRecord, error) {
	value, err := store.client.Get(ctx, store.caseKey(caseID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return caseRecord{}, errCaseNotFound
	}
	if err != nil {
		return caseRecord{}, fmt.Errorf("read maintenance case: %w", err)
	}
	var record caseRecord
	if err := decodeCoordinatorBytes(value, &record); err != nil {
		return caseRecord{}, errors.New("stored maintenance case is invalid")
	}
	if record.CaseID != caseID {
		return caseRecord{}, errors.New("stored maintenance case identity mismatch")
	}
	return record, nil
}

func (store *caseStore) reserveObservation(ctx context.Context, profile coordinatorProfile, input investigationInput, source string, now time.Time) (caseRecord, bool, error) {
	key := store.caseKey(input.CaseID)
	for attempt := 0; attempt < 8; attempt++ {
		var output caseRecord
		var dispatch bool
		err := store.client.Watch(ctx, func(tx *redis.Tx) error {
			value, err := tx.Get(ctx, key).Bytes()
			switch {
			case errors.Is(err, redis.Nil):
				output = caseRecord{
					SchemaVersion: 1, CaseID: input.CaseID, CoordinatorProfileID: profile.CoordinatorProfileID,
					CoordinatorProfileSHA256: profile.ProfileSHA256, Objective: input.Objective,
					ObservationRefs: append([]string(nil), input.ObservationRefs...), Source: source,
					AdmissionAttempts: 1, OccurrenceCount: 1, Status: "pending_admission",
					CreatedAt: now, UpdatedAt: now, LastSeenAt: now,
				}
				output.CurrentTaskID = investigationTaskID(input.CaseID, output.AdmissionAttempts)
				dispatch = true
			case err != nil:
				return err
			default:
				if err := decodeCoordinatorBytes(value, &output); err != nil {
					return errors.New("stored maintenance case is invalid")
				}
				if !sameCaseIdentity(output, profile, input) {
					return errCaseConflict
				}
				if err := retainLegacyPublicationEffects(&output, now); err != nil {
					return err
				}
				output.OccurrenceCount++
				output.LastSeenAt = now
				output.UpdatedAt = now
				if output.Status == "admission_failed" && output.AdmissionAttempts < profile.Limits.MaximumAdmissionAttempts {
					output.AdmissionAttempts++
					output.CurrentTaskID = investigationTaskID(input.CaseID, output.AdmissionAttempts)
					output.Status = "pending_admission"
					output.LastErrorCode = ""
					output.BoundaryFailure = nil
					output.ValidationEvidence = nil
					dispatch = true
				}
			}
			encoded, err := json.Marshal(output)
			if err != nil {
				return err
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, key, encoded, 0)
				return nil
			})
			return err
		}, key)
		if errors.Is(err, redis.TxFailedErr) {
			continue
		}
		if err != nil {
			return caseRecord{}, false, fmt.Errorf("reserve maintenance case: %w", err)
		}
		return output, dispatch, nil
	}
	return caseRecord{}, false, errors.New("maintenance case reservation was contended")
}

func (store *caseStore) prepareManualExecution(ctx context.Context, profile coordinatorProfile, input investigationInput, taskID string, now time.Time) (caseRecord, bool, error) {
	key := store.caseKey(input.CaseID)
	for attempt := 0; attempt < 8; attempt++ {
		var output caseRecord
		var execute bool
		err := store.client.Watch(ctx, func(tx *redis.Tx) error {
			value, err := tx.Get(ctx, key).Bytes()
			switch {
			case errors.Is(err, redis.Nil):
				output = caseRecord{
					SchemaVersion: 1, CaseID: input.CaseID, CoordinatorProfileID: profile.CoordinatorProfileID,
					CoordinatorProfileSHA256: profile.ProfileSHA256, Objective: input.Objective,
					ObservationRefs: append([]string(nil), input.ObservationRefs...), Source: "manual",
					CurrentTaskID: taskID, AdmissionAttempts: 1, OccurrenceCount: 1, Status: "running",
					CreatedAt: now, UpdatedAt: now, LastSeenAt: now,
				}
				execute = true
			case err != nil:
				return err
			default:
				if err := decodeCoordinatorBytes(value, &output); err != nil {
					return errors.New("stored maintenance case is invalid")
				}
				if !sameCaseIdentity(output, profile, input) {
					return errCaseConflict
				}
				if err := retainLegacyPublicationEffects(&output, now); err != nil {
					return err
				}
				output.OccurrenceCount++
				output.LastSeenAt = now
				output.UpdatedAt = now
				if output.CurrentTaskID == taskID && (output.Status == "queued" || output.Status == "pending_admission") {
					output.Status = "running"
					execute = true
				} else if output.Status == "admission_failed" && output.AdmissionAttempts < profile.Limits.MaximumAdmissionAttempts {
					output.AdmissionAttempts++
					output.CurrentTaskID = taskID
					output.Status = "running"
					output.LastErrorCode = ""
					output.BoundaryFailure = nil
					output.ValidationEvidence = nil
					execute = true
				}
			}
			encoded, err := json.Marshal(output)
			if err != nil {
				return err
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, key, encoded, 0)
				return nil
			})
			return err
		}, key)
		if errors.Is(err, redis.TxFailedErr) {
			continue
		}
		if err != nil {
			return caseRecord{}, false, fmt.Errorf("prepare manual maintenance case: %w", err)
		}
		return output, execute, nil
	}
	return caseRecord{}, false, errors.New("manual maintenance case update was contended")
}

func sameCaseIdentity(record caseRecord, profile coordinatorProfile, input investigationInput) bool {
	if record.SchemaVersion != 1 || record.CaseID != input.CaseID ||
		record.CoordinatorProfileID != profile.CoordinatorProfileID ||
		record.CoordinatorProfileSHA256 != profile.ProfileSHA256 || record.Objective != input.Objective ||
		len(record.ObservationRefs) != len(input.ObservationRefs) {
		return false
	}
	for index := range record.ObservationRefs {
		if record.ObservationRefs[index] != input.ObservationRefs[index] {
			return false
		}
	}
	return true
}

func (store *caseStore) updateForTask(ctx context.Context, caseID, taskID string, mutate func(*caseRecord) error) (caseRecord, error) {
	key := store.caseKey(caseID)
	for attempt := 0; attempt < 8; attempt++ {
		var output caseRecord
		err := store.client.Watch(ctx, func(tx *redis.Tx) error {
			value, err := tx.Get(ctx, key).Bytes()
			if errors.Is(err, redis.Nil) {
				return errCaseNotFound
			}
			if err != nil {
				return err
			}
			if err := decodeCoordinatorBytes(value, &output); err != nil {
				return errors.New("stored maintenance case is invalid")
			}
			if output.CurrentTaskID != taskID {
				return errCaseConflict
			}
			if err := retainLegacyPublicationEffects(&output, output.UpdatedAt); err != nil {
				return err
			}
			if err := mutate(&output); err != nil {
				return err
			}
			encoded, err := json.Marshal(output)
			if err != nil {
				return err
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, key, encoded, 0)
				return nil
			})
			return err
		}, key)
		if errors.Is(err, redis.TxFailedErr) {
			continue
		}
		if err != nil {
			return caseRecord{}, err
		}
		return output, nil
	}
	return caseRecord{}, errors.New("maintenance case update was contended")
}

func (store *caseStore) markQueued(ctx context.Context, caseID, taskID string, now time.Time) error {
	_, err := store.updateForTask(ctx, caseID, taskID, func(record *caseRecord) error {
		record.Status = "queued"
		record.LastErrorCode = ""
		record.BoundaryFailure = nil
		record.ValidationEvidence = nil
		record.UpdatedAt = now
		return nil
	})
	return err
}

func (store *caseStore) markAdmissionFailed(ctx context.Context, caseID, taskID, code string, now time.Time) error {
	_, err := store.updateForTask(ctx, caseID, taskID, func(record *caseRecord) error {
		record.Status = "admission_failed"
		record.LastErrorCode = code
		record.BoundaryFailure = nil
		record.ValidationEvidence = nil
		record.UpdatedAt = now
		return nil
	})
	return err
}

func (store *caseStore) claimExecution(ctx context.Context, caseID, taskID string, now time.Time) (caseRecord, bool, error) {
	record, err := store.updateForTask(ctx, caseID, taskID, func(record *caseRecord) error {
		if record.Status != "queued" && record.Status != "pending_admission" {
			return errCaseConflict
		}
		record.Status = "running"
		record.ValidationEvidence = nil
		record.UpdatedAt = now
		return nil
	})
	if errors.Is(err, errCaseConflict) {
		existing, getErr := store.get(ctx, caseID)
		if getErr != nil {
			return caseRecord{}, false, getErr
		}
		return existing, false, nil
	}
	return record, err == nil, err
}

func (store *caseStore) complete(ctx context.Context, caseID, taskID string, result investigationResult, now time.Time) error {
	_, err := store.updateForTask(ctx, caseID, taskID, func(record *caseRecord) error {
		record.Status = "completed"
		record.Disposition = result.Status
		record.ExecutionID = result.ExecutionID
		record.Summary = result.Summary
		record.Result = &result
		record.LastErrorCode = ""
		record.BoundaryFailure = nil
		record.ValidationEvidence = nil
		record.UpdatedAt = now
		return nil
	})
	return err
}

func (store *caseStore) markExecutionFailed(ctx context.Context, caseID, taskID, code string, now time.Time) error {
	return store.markExecutionFailedWithEvidence(ctx, caseID, taskID, code, "", "", now)
}

func (store *caseStore) markResultValidationFailed(ctx context.Context, caseID, taskID string, evidence resultValidationEvidence, now time.Time) error {
	_, err := store.updateForTask(ctx, caseID, taskID, func(record *caseRecord) error {
		if evidence.Publication != nil {
			if err := retainPublicationEffect(record, *evidence.Publication, evidence.ExecutionID, now); err != nil {
				return err
			}
		}
		record.Status = "failed"
		record.Disposition = "blocked"
		record.LastErrorCode = "RESULT_VALIDATION_FAILED"
		record.BoundaryFailure = nil
		record.ValidationEvidence = &evidence
		record.ExecutionID = evidence.ExecutionID
		record.Summary = evidence.Error
		record.UpdatedAt = now
		return nil
	})
	return err
}

func (store *caseStore) markExecutionFailedWithEvidence(ctx context.Context, caseID, taskID, code, summary, executionID string, now time.Time, boundary ...*agentfailure.BoundaryFailure) error {
	_, err := store.updateForTask(ctx, caseID, taskID, func(record *caseRecord) error {
		record.Status = "failed"
		record.Disposition = "blocked"
		record.LastErrorCode = code
		record.BoundaryFailure = nil
		record.ValidationEvidence = nil
		if len(boundary) > 0 && boundary[0] != nil {
			copy := *boundary[0]
			record.BoundaryFailure = &copy
		}
		if summary != "" {
			record.Summary = summary
		}
		if executionID != "" {
			record.ExecutionID = executionID
		}
		record.UpdatedAt = now
		return nil
	})
	return err
}

func (store *caseStore) getWatermark(ctx context.Context, profileID string) (time.Time, error) {
	value, err := store.client.Get(ctx, store.watermarkKey(profileID)).Result()
	if errors.Is(err, redis.Nil) {
		return time.Time{}, nil
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("read collection watermark: %w", err)
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, errors.New("stored collection watermark is invalid")
	}
	return parsed.UTC(), nil
}

func (store *caseStore) setWatermark(ctx context.Context, profileID string, value time.Time) error {
	if err := store.client.Set(ctx, store.watermarkKey(profileID), value.UTC().Format(time.RFC3339Nano), 0).Err(); err != nil {
		return fmt.Errorf("write collection watermark: %w", err)
	}
	return nil
}

func normalizeObservationRefs(values []string, maximum int) ([]string, error) {
	if len(values) == 0 || len(values) > maximum {
		return nil, errors.New("observation references are outside supported bounds")
	}
	result := append([]string(nil), values...)
	for _, value := range result {
		if !observationIDPattern.MatchString(value) {
			return nil, errors.New("observation reference is invalid")
		}
	}
	sort.Strings(result)
	for index := 1; index < len(result); index++ {
		if result[index] == result[index-1] {
			return nil, errors.New("observation references must be unique")
		}
	}
	return result, nil
}
