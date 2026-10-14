package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"
)

// Private storage preconditions, not new fields in public evidence receipts.
type evidenceVersion struct {
	key, value string
}

var errEvidenceChanged = errors.New("workspace evidence changed during reconciliation; read the same reference again")

// All participating keys belong to one workspace slot. Compare exact bytes,
// including presence, before writing anything. KEEPTTL preserves receipt life.
var commitEvidenceScript = redis.NewScript(`
for i, key in ipairs(KEYS) do
  if redis.call('GET', key) ~= ARGV[i] then return 0 end
end
if ARGV[#KEYS + 1] ~= ARGV[1] then
  redis.call('SET', KEYS[1], ARGV[#KEYS + 1], 'KEEPTTL')
end
return 1
`)

func (store *workspaceStore) getEvidenceVersion(ctx context.Context, kind, id string, destination any) (evidenceVersion, error) {
	seedKey := store.recordKey(kind, id)
	seed, err := store.client.Get(ctx, seedKey).Result()
	if errors.Is(err, redis.Nil) {
		return evidenceVersion{}, errWorkspaceRecordNotFound
	}
	if err != nil {
		return evidenceVersion{}, serviceFailure(fmt.Errorf("read evidence identity: %w", err), true)
	}
	var workspaceID string
	switch kind {
	case "job":
		var record jobRecord
		if err := decodeWorkspaceStrictBytes([]byte(seed), &record); err != nil {
			return evidenceVersion{}, err
		}
		if record.JobID != id {
			return evidenceVersion{}, errors.New("job identity seed does not match its reference")
		}
		workspaceID = record.WorkspaceID
	case "verification":
		var record verificationRecord
		if err := decodeWorkspaceStrictBytes([]byte(seed), &record); err != nil {
			return evidenceVersion{}, err
		}
		if record.VerificationID != id {
			return evidenceVersion{}, errors.New("verification identity seed does not match its reference")
		}
		var candidate candidateRecord
		if err := store.get(ctx, "candidate", record.CandidateID, &candidate); err != nil {
			return evidenceVersion{}, err
		}
		workspaceID = candidate.WorkspaceID
	default:
		return evidenceVersion{}, errors.New("unsupported mutable evidence kind")
	}
	if !workspaceIDPattern.MatchString(workspaceID) {
		return evidenceVersion{}, errors.New("evidence workspace identity is invalid")
	}
	key := store.keyspace.Tagged(workspaceToolName, workspaceID, "evidence-v2", kind, id)
	raw, err := store.client.Get(ctx, key).Result()
	if errors.Is(err, redis.Nil) {
		// Old writers must be stopped before rollout. Seeds are immutable from
		// this version onward. The canonical copy cannot outlive seed lookup.
		ttl, ttlErr := store.client.PTTL(ctx, seedKey).Result()
		if ttlErr != nil {
			return evidenceVersion{}, serviceFailure(ttlErr, true)
		}
		if ttl <= 0 {
			return evidenceVersion{}, errWorkspaceRecordNotFound
		}
		if err := store.client.SetNX(ctx, key, seed, ttl).Err(); err != nil {
			return evidenceVersion{}, serviceFailure(err, true)
		}
		raw, err = store.client.Get(ctx, key).Result()
	}
	if errors.Is(err, redis.Nil) {
		return evidenceVersion{}, errWorkspaceRecordNotFound
	}
	if err != nil {
		return evidenceVersion{}, serviceFailure(fmt.Errorf("read canonical evidence: %w", err), true)
	}
	if err := decodeWorkspaceStrictBytes([]byte(raw), destination); err != nil {
		return evidenceVersion{}, serviceFailure(err, false)
	}
	return evidenceVersion{key: key, value: raw}, nil
}

func (store *workspaceStore) commitEvidence(ctx context.Context, versions []evidenceVersion, value any) error {
	if len(versions) == 0 {
		return errors.New("evidence commit requires a source snapshot")
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return err
	}
	keys, args := make([]string, len(versions)), make([]any, 0, len(versions)+1)
	for i, version := range versions {
		keys[i] = version.key
		args = append(args, version.value)
	}
	args = append(args, string(encoded))
	result, err := commitEvidenceScript.Run(ctx, store.client, keys, args...).Int()
	if err != nil {
		return serviceFailure(fmt.Errorf("commit workspace evidence: %w", err), true)
	}
	if result != 1 {
		return serviceFailure(errEvidenceChanged, true)
	}
	return nil
}
