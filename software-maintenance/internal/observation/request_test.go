package observation

import (
	"net/http/httptest"
	"testing"

	"github.com/truvaagents/truva-g3/telemetry"
)

func TestRequestIDAndNilLogger(t *testing.T) {
	r := httptest.NewRequest("POST", "/", nil)
	r.Header.Set("X-TruvaG3-Request-ID", "header-id")
	if got := RequestID(r); got != "header-id" {
		t.Fatalf("header fallback = %q", got)
	}
	r = r.WithContext(telemetry.WithBaggage(r.Context(), "request_id", "execution-id"))
	if got := RequestID(r); got != "execution-id" {
		t.Fatalf("baggage precedence = %q", got)
	}
	Received(nil, r, "test")
}
