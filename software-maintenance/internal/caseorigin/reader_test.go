package caseorigin

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/truvaagents/truva-g3/core"
)

func TestCaseKeyAndOriginProjection(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	keys, err := core.NewRedisKeyspace("diagnostic")
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256([]byte("case-origin"))
	oldKey := keys.Plain("maintenance-cases", hex.EncodeToString(digest[:]))
	if Key(keys, "case-origin") != oldKey {
		t.Fatal("case key changed during ownership cutover")
	}
	reader := Reader{Client: client, Keys: keys}
	raw := `{"schema_version":1,"case_id":"case-origin","coordinator_profile_id":"profile","coordinator_profile_sha256":"pin","status":"completed","objective":"opaque coordinator-owned data","observation_refs":["observation"],"result":{"execution_id":"execution","execution_success":true,"status":"benign","summary":"evidenced conclusion","findings":[]},"publication_effects":{}}`
	server.Set(oldKey, raw)
	record, err := reader.Get(t.Context(), "case-origin")
	if err != nil || record.Result == nil || !record.Result.ExecutionSuccess || record.Result.ExecutionID != "execution" {
		t.Fatalf("projection lost original evidence: %v", err)
	}
	bounded := Reader{Client: client, Keys: keys, MaximumBytes: len(raw) - 1}
	if _, err := bounded.Get(t.Context(), "case-origin"); !errors.Is(err, ErrTooLarge) {
		t.Fatal("oversized origin was not rejected as a permanent read-bound failure")
	}
	for _, invalid := range []string{`{"schema_version":1,"case_id":"different"}`, `{"schema_version":2,"case_id":"case-origin"}`, `{"schema_version":1,"case_id":"case-origin","case_id":"case-origin"}`} {
		server.Set(oldKey, invalid)
		if _, err := reader.Get(t.Context(), "case-origin"); err == nil {
			t.Fatal("invalid origin accepted")
		}
	}
}
