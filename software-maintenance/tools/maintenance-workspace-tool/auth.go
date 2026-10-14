package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"sort"
	"strings"
)

type workspaceCredential struct {
	identity string
	digest   [sha256.Size]byte
}

type workspaceAuthorizer struct {
	credentials []workspaceCredential
}

func newWorkspaceAuthorizer(tokens map[string]string) (*workspaceAuthorizer, error) {
	identities := make([]string, 0, len(tokens))
	for identity := range tokens {
		identities = append(identities, identity)
	}
	sort.Strings(identities)
	authorizer := &workspaceAuthorizer{credentials: make([]workspaceCredential, 0, len(tokens))}
	seen := make(map[[sha256.Size]byte]struct{}, len(tokens))
	for _, identity := range identities {
		token := tokens[identity]
		if !workspaceNamePattern.MatchString(identity) || len(token) < 32 {
			return nil, errors.New("workspace caller identity or credential is invalid")
		}
		digest := sha256.Sum256([]byte(token))
		if _, exists := seen[digest]; exists {
			return nil, errors.New("workspace caller credentials must be distinct")
		}
		seen[digest] = struct{}{}
		authorizer.credentials = append(authorizer.credentials, workspaceCredential{identity: identity, digest: digest})
	}
	return authorizer, nil
}

func (authorizer *workspaceAuthorizer) authenticate(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	candidate := sha256.Sum256([]byte(parts[1]))
	identity := ""
	matched := 0
	for _, credential := range authorizer.credentials {
		equal := subtle.ConstantTimeCompare(candidate[:], credential.digest[:])
		if equal == 1 {
			identity = credential.identity
		}
		matched |= equal
	}
	return identity, matched == 1
}
