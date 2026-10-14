package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/truvaagents/truva-g3/core"
)

var errWorkspaceRecordNotFound = errors.New("workspace record not found")

type workspaceStore struct {
	client   redis.UniversalClient
	keyspace core.RedisKeyspace
}

func (store *workspaceStore) putImmutable(ctx context.Context, kind, id string, value any, ttl time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s record: %w", kind, err)
	}
	created, err := store.client.SetNX(ctx, store.recordKey(kind, id), data, ttl).Result()
	if err != nil {
		return serviceFailure(fmt.Errorf("store %s record: %w", kind, err), true)
	}
	if !created {
		return nil
	}
	return nil
}

func (store *workspaceStore) putMutable(ctx context.Context, kind, id string, value any, ttl time.Duration) error {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("encode %s record: %w", kind, err)
	}
	if err := store.client.Set(ctx, store.recordKey(kind, id), data, ttl).Err(); err != nil {
		return serviceFailure(fmt.Errorf("store %s record: %w", kind, err), true)
	}
	return nil
}

func (store *workspaceStore) get(ctx context.Context, kind, id string, destination any) error {
	if kind == "job" || kind == "verification" {
		_, err := store.getEvidenceVersion(ctx, kind, id, destination)
		return err
	}
	data, err := store.client.Get(ctx, store.recordKey(kind, id)).Bytes()
	if errors.Is(err, redis.Nil) {
		return errWorkspaceRecordNotFound
	}
	if err != nil {
		return serviceFailure(fmt.Errorf("read %s record: %w", kind, err), true)
	}
	if err := decodeWorkspaceStrictBytes(data, destination); err != nil {
		return serviceFailure(fmt.Errorf("decode %s record: %w", kind, err), false)
	}
	return nil
}

func (store *workspaceStore) putArtifact(ctx context.Context, metadata artifactRecord, content []byte, ttl time.Duration) error {
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return errors.New("encode artifact metadata")
	}
	_, err = store.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.SetNX(ctx, store.artifactMetadataKey(metadata.CaseID, metadata.ArtifactID), encoded, ttl)
		// The content key is hash-addressed, so rewriting the same bytes safely
		// extends their lifetime for a later case that produced identical output.
		pipe.Set(ctx, store.artifactContentKey(metadata.ArtifactID), content, ttl)
		return nil
	})
	if err != nil {
		return serviceFailure(fmt.Errorf("store job artifact: %w", err), true)
	}
	return nil
}

func (store *workspaceStore) putArtifactMetadata(ctx context.Context, metadata artifactRecord, ttl time.Duration) error {
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return errors.New("encode artifact metadata")
	}
	if err := store.client.SetNX(ctx, store.artifactMetadataKey(metadata.CaseID, metadata.ArtifactID), encoded, ttl).Err(); err != nil {
		return serviceFailure(fmt.Errorf("store job artifact metadata: %w", err), true)
	}
	return nil
}

func (store *workspaceStore) getArtifactMetadata(ctx context.Context, caseID, artifactID string, destination any) error {
	data, err := store.client.Get(ctx, store.artifactMetadataKey(caseID, artifactID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return errWorkspaceRecordNotFound
	}
	if err != nil {
		return serviceFailure(fmt.Errorf("read artifact metadata: %w", err), true)
	}
	if err := decodeWorkspaceStrictBytes(data, destination); err != nil {
		return serviceFailure(fmt.Errorf("decode artifact metadata: %w", err), false)
	}
	return nil
}

func (store *workspaceStore) getArtifactSlice(ctx context.Context, caseID, artifactID string, start, limit int64) (artifactRecord, []byte, error) {
	var metadata artifactRecord
	if err := store.getArtifactMetadata(ctx, caseID, artifactID, &metadata); err != nil {
		return metadata, nil, err
	}
	if start < 0 || limit <= 0 || metadata.Bytes <= start {
		return metadata, nil, serviceFailure(errors.New("artifact byte range or stored size is invalid"), false)
	}
	expectedBytes := min(limit, metadata.Bytes-start)
	content, err := store.client.GetRange(ctx, store.artifactContentKey(artifactID), start, start+expectedBytes-1).Bytes()
	if errors.Is(err, redis.Nil) {
		return metadata, nil, errWorkspaceRecordNotFound
	}
	if err != nil {
		return metadata, nil, serviceFailure(fmt.Errorf("read artifact content: %w", err), true)
	}
	// Redis GETRANGE returns an empty string, not redis.Nil, for an absent key.
	// A short read must not be interpreted as evidence that a test stopped failing.
	if int64(len(content)) != expectedBytes {
		return metadata, nil, serviceFailure(errors.New("artifact content length differs from stored metadata"), false)
	}
	return metadata, content, nil
}

func (store *workspaceStore) recordKey(kind, id string) string {
	return store.keyspace.Tagged(workspaceToolName, id, kind)
}

func (store *workspaceStore) artifactContentKey(id string) string {
	return store.keyspace.Tagged(workspaceToolName, id, "artifact", "content")
}

func (store *workspaceStore) artifactMetadataKey(caseID, artifactID string) string {
	return store.keyspace.Tagged(workspaceToolName, artifactID, "artifact", "metadata", caseID)
}
