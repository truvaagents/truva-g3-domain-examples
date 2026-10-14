package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strings"

	"github.com/redis/go-redis/v9"
	"github.com/truvaagents/truva-g3/core"
)

var (
	ErrNotFound = errors.New("assessment not found")
	ErrConflict = errors.New("assessment version or identity conflicts with its durable record")
	recordID    = regexp.MustCompile(`^(assessment|issue)_[a-f0-9]{64}$`)
)

type Store struct {
	client      redis.UniversalClient
	keys        core.RedisKeyspace
	scope       Scope
	atomicScope string
}

// IssueVersion is an authoritative version for the exact current signature.
// History pages cannot establish this version when facts changed or pagination
// omitted the current record. Put still enforces CAS at the write boundary.
func (store *Store) IssueVersion(ctx context.Context, facts Facts) (uint64, error) {
	if facts.Scope != store.scope || facts.PolicySHA256 == "" || len(facts.Signature) == 0 {
		return 0, ErrConflict
	}
	id := IssueID(facts)
	data, err := store.client.Get(ctx, store.key("issue", id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	var issue Issue
	if decodeRecord(data, &issue) != nil || issue.IssueID != id || issue.Scope != store.scope || issue.Version == 0 {
		return 0, ErrConflict
	}
	return issue.Version, nil
}

// NewStore borrows the application's Redis client; the application owns closing it.
func NewStore(client redis.UniversalClient, keys core.RedisKeyspace, scope Scope) (*Store, error) {
	if client == nil || !slug.MatchString(scope.CoordinatorProfileID) || !slug.MatchString(scope.ServiceProfileID) || !slug.MatchString(scope.RepositoryID) || !slug.MatchString(scope.EnvironmentID) {
		return nil, errors.New("assessment store requires an authorized complete scope and Redis client")
	}
	encoded, err := json.Marshal(scope)
	if err != nil {
		return nil, err
	}
	return &Store{client: client, keys: keys, scope: scope, atomicScope: Digest(encoded)}, nil
}

func (store *Store) key(parts ...string) string {
	return store.keys.Tagged("maintenance-lifecycle", store.atomicScope, parts...)
}

func decodeRecord(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var extra any
	if !errors.Is(decoder.Decode(&extra), io.EOF) {
		return errors.New("lifecycle record must contain exactly one JSON value")
	}
	return nil
}

func (store *Store) Get(ctx context.Context, id string) (Assessment, error) {
	if !recordID.MatchString(id) || len(id) != len("assessment_")+64 {
		return Assessment{}, ErrNotFound
	}
	data, err := store.client.Get(ctx, store.key("assessment", id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return Assessment{}, ErrNotFound
	}
	if err != nil {
		return Assessment{}, fmt.Errorf("read assessment: %w", err)
	}
	var record Assessment
	if decodeRecord(data, &record) != nil || validateAssessment(record, store.scope) != nil || record.AssessmentID != id || record.AssessmentID != assessmentIdentity(record) || record.IssueID != IssueID(record.Facts) || record.Version == 0 {
		return Assessment{}, errors.New("stored assessment identity or scope is invalid")
	}
	return record, nil
}

func validateAssessment(record Assessment, scope Scope) error {
	if record.SchemaVersion != 1 || record.Facts.Scope != scope || record.OriginCaseID == "" || record.OriginExecutionID == "" || record.AssessedAt.IsZero() || !record.RevalidateAfter.After(record.AssessedAt) || record.Facts.ObservedAt.IsZero() || record.Facts.ObservationID == "" || !slug.MatchString(record.Facts.CheckID) || len(record.Facts.Comparison) == 0 || len(record.Facts.Signature) == 0 || len(record.Facts.Evidence) == 0 || strings.TrimSpace(record.Summary) == "" {
		return errors.New("assessment lacks authoritative origin or comparison facts")
	}
	if record.Disposition != "benign" && record.Disposition != "draft_pr_opened" || record.Disposition == "draft_pr_opened" && len(record.Remediations) == 0 {
		return errors.New("assessment disposition lacks authoritative evidence")
	}
	for _, reference := range record.Remediations {
		if reference.PublicationID == "" || reference.OriginCaseID != record.OriginCaseID || reference.OriginExecutionID == "" || reference.PullRequestURL == "" {
			return errors.New("assessment publication reference is incomplete or has a different origin")
		}
	}
	comparison, err := json.Marshal(record.Facts.Comparison)
	if err != nil || record.Facts.ComparedFactsDigest != Digest(comparison) {
		return errors.New("assessment comparison digest is invalid")
	}
	return nil
}

func assessmentIdentity(record Assessment) string {
	identity, _ := json.Marshal(struct {
		OriginCase, OriginExecution, Observation, Issue, Facts, Disposition, Summary string
		Remediations                                                                 []RemediationReference
		Version                                                                      uint64
	}{record.OriginCaseID, record.OriginExecutionID, record.Facts.ObservationID, IssueID(record.Facts), record.Facts.ComparedFactsDigest, record.Disposition, record.Summary, record.Remediations, record.Version})
	return "assessment_" + Digest(identity)
}

// Put atomically creates an immutable assessment, advances one conditional
// issue version and its explicit indexes. Every key shares the profile slot.
func (store *Store) Put(ctx context.Context, record Assessment, expectedVersion uint64) (Assessment, error) {
	if err := validateAssessment(record, store.scope); err != nil {
		return Assessment{}, err
	}
	if expectedVersion == math.MaxUint64 {
		return Assessment{}, ErrConflict
	}
	// Expected version identifies the write intention: replay N returns N+1
	// unchanged; a renewal uses the current version and creates a new record.
	record.Version = expectedVersion + 1
	record.IssueID = IssueID(record.Facts)
	record.AssessmentID = assessmentIdentity(record)
	issueKey := store.key("issue", record.IssueID)
	assessmentKey := store.key("assessment", record.AssessmentID)
	for attempt := 0; attempt < 8; attempt++ {
		output := record
		err := store.client.Watch(ctx, func(tx *redis.Tx) error {
			// Successful replay preserves the original immutable assessment/time.
			data, err := tx.Get(ctx, assessmentKey).Bytes()
			if err == nil {
				currentFacts, _ := json.Marshal(record.Facts.Comparison)
				if decodeRecord(data, &output) != nil || validateAssessment(output, store.scope) != nil || output.AssessmentID != record.AssessmentID || assessmentIdentity(output) != record.AssessmentID || output.IssueID != record.IssueID || output.Version == 0 {
					return ErrConflict
				}
				previousFacts, _ := json.Marshal(output.Facts.Comparison)
				if !bytes.Equal(previousFacts, currentFacts) {
					return ErrConflict
				}
				return nil
			}
			if !errors.Is(err, redis.Nil) {
				return err
			}
			var issue Issue
			data, err = tx.Get(ctx, issueKey).Bytes()
			if err == nil {
				if decodeRecord(data, &issue) != nil || issue.IssueID != record.IssueID || issue.Scope != store.scope || issue.PolicySHA256 != record.Facts.PolicySHA256 || issue.CheckID != record.Facts.CheckID {
					return ErrConflict
				}
				previous, _ := json.Marshal(issue.Signature)
				current, _ := json.Marshal(record.Facts.Signature)
				if !bytes.Equal(previous, current) {
					return ErrConflict
				}
			} else if !errors.Is(err, redis.Nil) {
				return err
			}
			if issue.Version != expectedVersion {
				return ErrConflict
			}
			output.Version = record.Version
			output.SupersedesID = issue.CurrentAssessmentID
			issue = Issue{SchemaVersion: 1, IssueID: record.IssueID, Scope: store.scope, PolicySHA256: record.Facts.PolicySHA256, CheckID: record.Facts.CheckID, Signature: record.Facts.Signature, Version: output.Version, CurrentAssessmentID: record.AssessmentID, UpdatedAt: record.AssessedAt}
			assessmentData, err := json.Marshal(output)
			if err != nil {
				return err
			}
			issueData, err := json.Marshal(issue)
			if err != nil {
				return err
			}
			_, err = tx.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
				pipe.Set(ctx, assessmentKey, assessmentData, 0)
				pipe.Set(ctx, issueKey, issueData, 0)
				pipe.ZAdd(ctx, store.key("assessments"), redis.Z{Score: float64(record.AssessedAt.UnixMilli()), Member: record.AssessmentID})
				pipe.ZAdd(ctx, store.key("check", record.Facts.CheckID), redis.Z{Score: float64(record.AssessedAt.UnixMilli()), Member: record.AssessmentID})
				pipe.ZAdd(ctx, store.key("due"), redis.Z{Score: float64(record.RevalidateAfter.UnixMilli()), Member: record.AssessmentID})
				return nil
			})
			return err
		}, issueKey, assessmentKey)
		if errors.Is(err, redis.TxFailedErr) {
			continue
		}
		if err != nil {
			return Assessment{}, err
		}
		return output, nil
	}
	return Assessment{}, ErrConflict
}

func (store *Store) List(ctx context.Context, checkID string, limit int) ([]Assessment, bool, error) {
	if limit < 1 || limit > 50 || checkID != "" && !slug.MatchString(checkID) {
		return nil, false, errors.New("assessment page bounds are invalid")
	}
	index := store.key("assessments")
	if checkID != "" {
		index = store.key("check", checkID)
	}
	ids, err := store.client.ZRevRange(ctx, index, 0, int64(limit)).Result()
	if err != nil {
		return nil, false, err
	}
	truncated := len(ids) > limit
	if truncated {
		ids = ids[:limit]
	}
	result := make([]Assessment, 0, len(ids))
	for _, id := range ids {
		record, err := store.Get(ctx, id)
		if err != nil {
			return nil, false, err
		} // Missing evidence is not an empty successful lookup.
		result = append(result, record)
	}
	return result, truncated, nil
}

func (store *Store) Current(ctx context.Context, record Assessment) (bool, error) {
	data, err := store.client.Get(ctx, store.key("issue", record.IssueID)).Bytes()
	if err != nil {
		return false, err
	}
	var issue Issue
	if decodeRecord(data, &issue) != nil || issue.SchemaVersion != 1 || issue.Scope != store.scope || issue.IssueID != record.IssueID {
		return false, errors.New("issue record is invalid")
	}
	return issue.Version == record.Version && issue.CurrentAssessmentID == record.AssessmentID, nil
}
