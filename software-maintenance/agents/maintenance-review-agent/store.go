package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/truvaagents/truva-g3/core"
)

var errReviewNotFound = errors.New("review receipt not found")

type reviewStore struct {
	client   redis.UniversalClient
	keyspace core.RedisKeyspace
}

func (store *reviewStore) put(ctx context.Context, receipt reviewReceipt, ttl time.Duration) error {
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	created, err := store.client.SetNX(ctx, store.key(receipt.ReviewID), encoded, ttl).Result()
	if err != nil {
		return err
	}
	if !created {
		existing, getErr := store.get(ctx, receipt.ReviewID)
		if getErr != nil {
			return getErr
		}
		existingBytes, _ := json.Marshal(existing)
		if !bytesEqual(encoded, existingBytes) {
			return errors.New("review receipt identity collision")
		}
	}
	return nil
}

func (store *reviewStore) get(ctx context.Context, reviewID string) (reviewReceipt, error) {
	data, err := store.client.Get(ctx, store.key(reviewID)).Bytes()
	if errors.Is(err, redis.Nil) {
		return reviewReceipt{}, errReviewNotFound
	}
	if err != nil {
		return reviewReceipt{}, err
	}
	var receipt reviewReceipt
	if err := decodeReviewStrictBytes(data, &receipt); err != nil {
		return reviewReceipt{}, err
	}
	return receipt, nil
}

func (store *reviewStore) key(reviewID string) string {
	return store.keyspace.Tagged(reviewAgentName, reviewID, "receipt")
}

func bytesEqual(first, second []byte) bool {
	if len(first) != len(second) {
		return false
	}
	var different byte
	for index := range first {
		different |= first[index] ^ second[index]
	}
	return different == 0
}
