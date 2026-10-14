package serviceevidence

import (
	"crypto/sha256"
	"crypto/subtle"
	"errors"
	"strings"
)

type callerCredential struct {
	identity string
	digest   [sha256.Size]byte
}

type authorizer struct {
	credentials []callerCredential
}

func newAuthorizer(tokens map[string]string) (*authorizer, error) {
	if len(tokens) == 0 {
		return nil, errors.New("at least one caller credential is required")
	}
	auth := &authorizer{credentials: make([]callerCredential, 0, len(tokens))}
	seen := make(map[[sha256.Size]byte]struct{}, len(tokens))
	for identity, token := range tokens {
		if !profileIDPattern.MatchString(identity) {
			return nil, errors.New("caller identity is invalid")
		}
		if len(token) < 32 {
			return nil, errors.New("caller credentials must contain at least 32 characters")
		}
		digest := sha256.Sum256([]byte(token))
		if _, exists := seen[digest]; exists {
			return nil, errors.New("caller credentials must be distinct")
		}
		seen[digest] = struct{}{}
		auth.credentials = append(auth.credentials, callerCredential{identity: identity, digest: digest})
	}
	return auth, nil
}

func (a *authorizer) authenticate(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	candidate := sha256.Sum256([]byte(parts[1]))
	identity := ""
	matched := 0
	for _, credential := range a.credentials {
		equal := subtle.ConstantTimeCompare(candidate[:], credential.digest[:])
		if equal == 1 {
			identity = credential.identity
		}
		matched |= equal
	}
	return identity, matched == 1
}
