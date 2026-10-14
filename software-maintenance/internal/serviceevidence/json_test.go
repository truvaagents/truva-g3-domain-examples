package serviceevidence

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeStrictRequestRejectsUnknownDuplicateAndOversizedFields(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		body string
		max  int64
	}{
		{name: "unknown", body: `{"profile_id":"retail-order-service","url":"http://attacker.invalid"}`, max: 1024},
		{name: "duplicate", body: `{"profile_id":"retail-order-service","profile_id":"other"}`, max: 1024},
		{name: "oversized", body: `{"profile_id":"retail-order-service"}`, max: 8},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest("POST", "/", strings.NewReader(test.body))
			var decoded profileRequest
			if err := decodeStrictRequest(request, test.max, &decoded); err == nil {
				t.Fatal("decodeStrictRequest() accepted invalid input")
			}
		})
	}
}

func TestDecodeStrictRequestAcceptsOneKnownObject(t *testing.T) {
	t.Parallel()
	request := httptest.NewRequest("POST", "/", strings.NewReader(`{"profile_id":"retail-order-service"}`))
	var decoded profileRequest
	if err := decodeStrictRequest(request, 1024, &decoded); err != nil {
		t.Fatalf("decodeStrictRequest() error = %v", err)
	}
	if decoded.ProfileID != "retail-order-service" {
		t.Fatalf("profile_id = %q", decoded.ProfileID)
	}
}
