package main

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"sort"
	"strings"
)

type reviewCredential struct {
	identity string
	digest   [sha256.Size]byte
}

type reviewAuthorizer struct {
	credentials []reviewCredential
}

func newReviewAuthorizer(tokens map[string]string) (*reviewAuthorizer, error) {
	identities := make([]string, 0, len(tokens))
	for identity := range tokens {
		identities = append(identities, identity)
	}
	sort.Strings(identities)
	authorizer := &reviewAuthorizer{credentials: make([]reviewCredential, 0, len(tokens))}
	seen := make(map[[sha256.Size]byte]struct{}, len(tokens))
	for _, identity := range identities {
		token := tokens[identity]
		if !reviewNamePattern.MatchString(identity) || len(token) < 32 {
			return nil, errors.New("review caller identity or credential is invalid")
		}
		digest := sha256.Sum256([]byte(token))
		if _, exists := seen[digest]; exists {
			return nil, errors.New("review caller credentials must be distinct")
		}
		seen[digest] = struct{}{}
		authorizer.credentials = append(authorizer.credentials, reviewCredential{identity: identity, digest: digest})
	}
	return authorizer, nil
}

func (authorizer *reviewAuthorizer) authenticate(header string) (string, bool) {
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
