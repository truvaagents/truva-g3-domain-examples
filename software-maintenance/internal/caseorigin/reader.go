package caseorigin

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
	"github.com/truvaagents/truva-g3-domain-examples/software-maintenance/internal/strictjson"
	"github.com/truvaagents/truva-g3/core"
)

var ErrNotFound = errors.New("maintenance case not found")
var ErrTooLarge = errors.New("maintenance case origin exceeds configured read bound")

// Key is shared with the coordinator writer; existing durable keys stay unchanged.
func Key(keys core.RedisKeyspace, caseID string) string {
	digest := sha256.Sum256([]byte(caseID))
	return keys.Plain("maintenance-cases", hex.EncodeToString(digest[:]))
}

// Record is a read-only projection, not a second case/result owner.
type Record struct {
	SchemaVersion            int      `json:"schema_version"`
	CaseID                   string   `json:"case_id"`
	CoordinatorProfileID     string   `json:"coordinator_profile_id"`
	CoordinatorProfileSHA256 string   `json:"coordinator_profile_sha256"`
	Status                   string   `json:"status"`
	ObservationRefs          []string `json:"observation_refs"`
	Result                   *struct {
		ExecutionID      string `json:"execution_id"`
		ExecutionSuccess bool   `json:"execution_success"`
		Status           string `json:"status"`
		Summary          string `json:"summary"`
	} `json:"result"`
	PublicationEffects map[string]struct {
		Publication struct {
			PublicationID  string `json:"publication_id"`
			CaseID         string `json:"case_id"`
			ProfileID      string `json:"profile_id"`
			ProfileSHA256  string `json:"profile_sha256"`
			PullRequestURL string `json:"pull_request_url"`
		} `json:"publication"`
		OriginExecutionID string `json:"origin_execution_id"`
	} `json:"publication_effects"`
}

type Reader struct {
	Client       redis.UniversalClient
	Keys         core.RedisKeyspace
	MaximumBytes int
}

func (reader Reader) Get(ctx context.Context, caseID string) (Record, error) {
	value, err := reader.Client.Get(ctx, Key(reader.Keys, caseID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return Record{}, ErrNotFound
	}
	if err != nil {
		return Record{}, fmt.Errorf("read maintenance case origin: %w", err)
	}
	var record Record
	maximum := reader.MaximumBytes
	if maximum == 0 {
		maximum = 1 << 20
	}
	if len(value) > maximum {
		return Record{}, ErrTooLarge
	}
	if strictjson.DecodePartial(value, &record) != nil || record.SchemaVersion != 1 || record.CaseID != caseID {
		return Record{}, errors.New("stored maintenance case origin is invalid")
	}
	return record, nil
}
