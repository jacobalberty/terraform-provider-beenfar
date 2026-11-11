package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"

	configv1 "github.com/jacobalberty/beenfar/api/gen/go/beenfar/config/v1"
)

// Calls to beenfar carry a W3C trace context, so the controller's spans
// join the provider's trace, and the API token.
func TestClientPropagatesTraceAndToken(t *testing.T) {
	var traceparent, auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceparent, auth = r.Header.Get("Traceparent"), r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/proto")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	t.Setenv("TRACEPARENT", "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01")
	c, err := NewClient(srv.Client(), srv.URL, "bf_token", newTelemetry(context.Background()))
	if err != nil {
		t.Fatal(err)
	}
	ctx, span := c.span(context.Background(), "test")
	_, _ = c.Networks.ListNetworks(ctx, &configv1.ListNetworksRequest{})
	span.End()

	if auth != "Bearer bf_token" {
		t.Errorf("Authorization = %q", auth)
	}
	// Same trace as TRACEPARENT, new span.
	if !regexp.MustCompile(`^00-0af7651916cd43dd8448eb211c80319c-[0-9a-f]{16}-01$`).MatchString(traceparent) ||
		traceparent == "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01" {
		t.Errorf("traceparent = %q", traceparent)
	}
}
