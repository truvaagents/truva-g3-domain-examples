package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/truvaagents/truva-g3/core"
)

var errPublicationNotFound = errors.New("publication record not found")
var errPublicationClaimed = errors.New("publication is currently claimed")

type publicationStore struct {
	client   redis.UniversalClient
	keyspace core.RedisKeyspace
}

func (store *publicationStore) reserve(ctx context.Context, record publicationRecord, ttl time.Duration) (publicationRecord, error) {
	encoded, err := json.Marshal(record)
	if err != nil {
		return publicationRecord{}, failPublication("state_encode", false, err)
	}
	created, err := store.client.SetNX(ctx, store.recordKey(record.PublicationID), encoded, ttl).Result()
	if err != nil {
		return publicationRecord{}, failPublication("state_unavailable", true, err)
	}
	if created {
		return record, nil
	}
	return store.get(ctx, record.PublicationID)
}

func (store *publicationStore) put(ctx context.Context, record publicationRecord, ttl time.Duration) error {
	encoded, err := json.Marshal(record)
	if err != nil {
		return failPublication("state_encode", false, err)
	}
	if err := store.client.Set(ctx, store.recordKey(record.PublicationID), encoded, ttl).Err(); err != nil {
		return failPublication("state_unavailable", true, err)
	}
	return nil
}

func (store *publicationStore) get(ctx context.Context, publicationID string) (publicationRecord, error) {
	data, err := store.client.Get(ctx, store.recordKey(publicationID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return publicationRecord{}, errPublicationNotFound
	}
	if err != nil {
		return publicationRecord{}, failPublication("state_unavailable", true, err)
	}
	var record publicationRecord
	if err := decodePublicationStrictBytes(data, &record); err != nil {
		return publicationRecord{}, failPublication("state_invalid", false, err)
	}
	return record, nil
}

func (store *publicationStore) acquireClaim(ctx context.Context, publicationID string, ttl time.Duration) (string, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", failPublication("claim_token", false, err)
	}
	token := hex.EncodeToString(bytes)
	created, err := store.client.SetNX(ctx, store.claimKey(publicationID), token, ttl).Result()
	if err != nil {
		return "", failPublication("state_unavailable", true, err)
	}
	if !created {
		return "", errPublicationClaimed
	}
	return token, nil
}

func (store *publicationStore) releaseClaim(ctx context.Context, publicationID, token string) {
	const compareAndDelete = `if redis.call("GET", KEYS[1]) == ARGV[1] then return redis.call("DEL", KEYS[1]) else return 0 end`
	_, _ = store.client.Eval(ctx, compareAndDelete, []string{store.claimKey(publicationID)}, token).Result()
}

func (store *publicationStore) recordKey(publicationID string) string {
	return store.keyspace.Tagged(publicationToolName, publicationID, "publication")
}

func (store *publicationStore) claimKey(publicationID string) string {
	return store.keyspace.Tagged(publicationToolName, publicationID, "claim")
}
