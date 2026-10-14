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

var errSnapshotNotFound = errors.New("source snapshot not found")

type snapshotStore interface {
	put(context.Context, snapshotMetadata, []byte, time.Duration) error
	getSlice(context.Context, string, int64, int64) (snapshotMetadata, []byte, error)
}

type redisSnapshotStore struct {
	client   redis.UniversalClient
	keyspace core.RedisKeyspace
}

func (store *redisSnapshotStore) put(ctx context.Context, metadata snapshotMetadata, archive []byte, ttl time.Duration) error {
	encodedMetadata, err := json.Marshal(metadata)
	if err != nil {
		return errors.New("encode snapshot metadata")
	}
	_, err = store.client.TxPipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.Set(ctx, store.metadataKey(metadata.SnapshotID), encodedMetadata, ttl)
		pipe.Set(ctx, store.archiveKey(metadata.SnapshotID), archive, ttl)
		return nil
	})
	if err != nil {
		return fmt.Errorf("store source snapshot: %w", err)
	}
	return nil
}

func (store *redisSnapshotStore) getSlice(ctx context.Context, snapshotID string, start, limit int64) (snapshotMetadata, []byte, error) {
	metadataCommand := store.client.Get(ctx, store.metadataKey(snapshotID))
	archiveCommand := store.client.GetRange(ctx, store.archiveKey(snapshotID), start, start+limit-1)
	metadataBytes, err := metadataCommand.Bytes()
	if errors.Is(err, redis.Nil) {
		return snapshotMetadata{}, nil, errSnapshotNotFound
	}
	if err != nil {
		return snapshotMetadata{}, nil, fmt.Errorf("read snapshot metadata: %w", err)
	}
	var metadata snapshotMetadata
	if err := decodeSourceStrictBytes(metadataBytes, &metadata); err != nil || !validSnapshotMetadata(metadata, snapshotID) {
		return snapshotMetadata{}, nil, errors.New("invalid stored snapshot metadata")
	}
	archive, err := archiveCommand.Bytes()
	if errors.Is(err, redis.Nil) {
		return snapshotMetadata{}, nil, errSnapshotNotFound
	}
	if err != nil {
		return snapshotMetadata{}, nil, fmt.Errorf("read snapshot archive: %w", err)
	}
	return metadata, archive, nil
}

func validSnapshotMetadata(metadata snapshotMetadata, snapshotID string) bool {
	return metadata.SchemaVersion == 1 &&
		metadata.SnapshotID == snapshotID &&
		snapshotIDPattern.MatchString(snapshotID) &&
		sourceIDPattern.MatchString(metadata.RepositoryID) &&
		sha256Pattern.MatchString(metadata.SourceProfileSHA256) &&
		gitSHAPattern.MatchString(metadata.RevisionSHA) &&
		gitSHAPattern.MatchString(metadata.TreeSHA) &&
		len(metadata.ArchiveSHA256) == 64 &&
		metadata.SnapshotID == "src_"+metadata.ArchiveSHA256 &&
		metadata.ArchiveBytes > 0 &&
		metadata.UncompressedBytes >= 0 &&
		metadata.FileCount > 0 &&
		metadata.ExcludedEntries >= 0 &&
		metadata.Format == "application/gzip" &&
		!metadata.CreatedAt.IsZero() &&
		metadata.ExpiresAt.After(metadata.CreatedAt)
}

func (store *redisSnapshotStore) metadataKey(snapshotID string) string {
	return store.keyspace.Tagged(sourceToolName, snapshotID, "snapshot", "metadata")
}

func (store *redisSnapshotStore) archiveKey(snapshotID string) string {
	return store.keyspace.Tagged(sourceToolName, snapshotID, "snapshot", "archive")
}
