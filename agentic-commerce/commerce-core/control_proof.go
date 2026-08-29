package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

type evidenceReceiptClaims struct {
	Version      int       `json:"version"`
	SectionID    string    `json:"section_id"`
	ReleaseID    string    `json:"release_id"`
	ReleaseHash  string    `json:"release_hash"`
	TenantID     string    `json:"tenant_id"`
	Jurisdiction string    `json:"jurisdiction"`
	Channel      string    `json:"channel"`
	AsOf         time.Time `json:"as_of"`
	IssuedAt     time.Time `json:"issued_at"`
	ExpiresAt    time.Time `json:"expires_at"`
}

func verifyEvidenceReceipt(key []byte, receipt string, now time.Time) (evidenceReceiptClaims, error) {
	parts := strings.Split(receipt, ".")
	if len(parts) != 2 {
		return evidenceReceiptClaims{}, fmt.Errorf("invalid evidence receipt format")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return evidenceReceiptClaims{}, fmt.Errorf("decode evidence receipt: %w", err)
	}
	provided, err := hex.DecodeString(parts[1])
	if err != nil {
		return evidenceReceiptClaims{}, fmt.Errorf("decode evidence receipt signature: %w", err)
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("evidence-v1|"))
	_, _ = mac.Write(payload)
	if !hmac.Equal(provided, mac.Sum(nil)) {
		return evidenceReceiptClaims{}, fmt.Errorf("evidence receipt signature is invalid")
	}
	var claims evidenceReceiptClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return evidenceReceiptClaims{}, fmt.Errorf("decode evidence receipt claims: %w", err)
	}
	if claims.Version != 1 || claims.SectionID == "" || claims.ReleaseID == "" || claims.ReleaseHash == "" || claims.TenantID == "" || claims.Jurisdiction == "" || claims.Channel == "" || claims.AsOf.IsZero() || claims.IssuedAt.IsZero() || claims.ExpiresAt.IsZero() {
		return evidenceReceiptClaims{}, fmt.Errorf("evidence receipt claims are incomplete")
	}
	if now.Before(claims.IssuedAt.Add(-time.Minute)) || !now.Before(claims.ExpiresAt) {
		return evidenceReceiptClaims{}, fmt.Errorf("evidence receipt is not currently valid")
	}
	return claims, nil
}

func verifyApprovalProof(key []byte, checkpointID, decisionToken, idempotencyKey, proof string) bool {
	if checkpointID == "" || decisionToken == "" || idempotencyKey == "" || proof == "" {
		return false
	}
	provided, err := hex.DecodeString(proof)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("approval-v1|" + checkpointID + "|" + decisionToken + "|" + idempotencyKey))
	return hmac.Equal(provided, mac.Sum(nil))
}
