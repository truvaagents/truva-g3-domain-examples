package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"strings"
)

type repairAuthorizer struct {
	identity string
	digest   [sha256.Size]byte
}

func newRepairAuthorizer(identity, token string) (*repairAuthorizer, error) {
	if !repairNamePattern.MatchString(identity) || len(token) < 32 {
		return nil, errors.New("repair caller identity or credential is invalid")
	}
	return &repairAuthorizer{identity: identity, digest: sha256.Sum256([]byte(token))}, nil
}

func (authorizer *repairAuthorizer) authenticate(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	candidate := sha256.Sum256([]byte(parts[1]))
	return authorizer.identity, subtle.ConstantTimeCompare(candidate[:], authorizer.digest[:]) == 1
}
