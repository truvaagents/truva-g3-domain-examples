package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
)

func approvalProof(key []byte, checkpointID, decisionToken, idempotencyKey string) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("approval-v1|" + checkpointID + "|" + decisionToken + "|" + idempotencyKey))
	return hex.EncodeToString(mac.Sum(nil))
}
