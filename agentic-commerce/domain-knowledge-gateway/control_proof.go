package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

func issueEvidenceReceipt(key []byte, claims evidenceReceiptClaims) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("encode evidence receipt: %w", err)
	}
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("evidence-v1|"))
	_, _ = mac.Write(payload)
	return base64.RawURLEncoding.EncodeToString(payload) + "." + hex.EncodeToString(mac.Sum(nil)), nil
}
