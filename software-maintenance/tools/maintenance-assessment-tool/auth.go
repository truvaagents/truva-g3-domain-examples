package main

import (
	"crypto/subtle"
	"errors"
	"strings"
)

type coordinatorAuthorizer struct {
	caller string
	token  string
}

func newCoordinatorAuthorizer(caller, token string) (*coordinatorAuthorizer, error) {
	caller = strings.TrimSpace(caller)
	token = strings.TrimSpace(token)
	if caller == "" || len(token) < 32 {
		return nil, errors.New("assessment credential must contain at least 32 characters")
	}
	return &coordinatorAuthorizer{caller: caller, token: token}, nil
}

func (authorizer *coordinatorAuthorizer) authenticate(header string) (string, bool) {
	const prefix = "Bearer "
	if !strings.HasPrefix(header, prefix) {
		return "", false
	}
	presented := strings.TrimSpace(strings.TrimPrefix(header, prefix))
	if len(presented) != len(authorizer.token) || subtle.ConstantTimeCompare([]byte(presented), []byte(authorizer.token)) != 1 {
		return "", false
	}
	return authorizer.caller, true
}
