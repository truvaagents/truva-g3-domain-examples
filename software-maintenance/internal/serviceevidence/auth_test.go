package serviceevidence

import "testing"

func TestAuthorizerAuthenticatesDistinctCallers(t *testing.T) {
	t.Parallel()
	auth, err := newAuthorizer(map[string]string{
		"maintenance-agent":        "coordinator-token-0000000000000000",
		"maintenance-repair-agent": "repair-token-000000000000000000000",
	})
	if err != nil {
		t.Fatalf("newAuthorizer() error = %v", err)
	}
	identity, ok := auth.authenticate("Bearer coordinator-token-0000000000000000")
	if !ok || identity != "maintenance-agent" {
		t.Fatalf("authenticate() = %q, %v", identity, ok)
	}
	if _, ok := auth.authenticate("Bearer invalid-token-00000000000000000000"); ok {
		t.Fatal("authenticate() accepted an unknown token")
	}
}

func TestAuthorizerRejectsUnsafeConfiguration(t *testing.T) {
	t.Parallel()
	for name, tokens := range map[string]map[string]string{
		"short token": {"maintenance-agent": "short"},
		"shared token": {
			"maintenance-agent":        "shared-token-000000000000000000000",
			"maintenance-repair-agent": "shared-token-000000000000000000000",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := newAuthorizer(tokens); err == nil {
				t.Fatal("newAuthorizer() succeeded for unsafe configuration")
			}
		})
	}
}
