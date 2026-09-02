package client

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestUnitParseRetryAfter covers both forms RFC 9110 allows plus the malformed
// values a server can still send.
//
// A wrong answer here is not a wrong number, it is a stalled apply: the value
// becomes a sleep, and Terraform prints nothing while it waits.
func TestUnitParseRetryAfter(t *testing.T) {
	cases := []struct {
		name   string
		header string
		want   time.Duration
		approx bool
	}{
		{name: "absent", header: "", want: 0},
		{name: "delay seconds", header: "60", want: 60 * time.Second},
		{name: "zero seconds", header: "0", want: 0},
		// Negative seconds are not valid; treated as no guidance rather than as a
		// negative sleep, which would busy-loop.
		{name: "negative seconds", header: "-5", want: 0},
		{name: "not a number", header: "soon", want: 0},
		{name: "empty-ish whitespace", header: "   ", want: 0},
		// An HTTP-date in the past means the wait has already elapsed.
		{name: "http date in the past", header: "Mon, 02 Jan 2006 15:04:05 GMT", want: 0},
		{name: "garbage date", header: "Nonsuch, 99 Xxx 9999", want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseRetryAfter(tc.header)
			if got != tc.want {
				t.Errorf("parseRetryAfter(%q) = %v, want %v", tc.header, got, tc.want)
			}
		})
	}

	// A future HTTP-date yields roughly the remaining time.
	future := time.Now().Add(90 * time.Second).UTC().Format(http.TimeFormat)
	if d := parseRetryAfter(future); d < 80*time.Second || d > 90*time.Second {
		t.Errorf("parseRetryAfter(future date) = %v, want about 90s", d)
	}
}

// TestUnitRetry_RefusesAbsurdRetryAfter is the regression test for a real hazard:
// MaxBackoff bounds the local backoff, but a server-supplied Retry-After bypassed
// it, so a header of 86400 would have parked an apply for a day with no output.
func TestUnitRetry_RefusesAbsurdRetryAfter(t *testing.T) {
	var slept []time.Duration
	attempts := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Retry-After", "86400") // one day
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = fmt.Fprint(w, `{"errors":["rate limited"]}`)
	}))
	t.Cleanup(srv.Close)

	policy := DefaultRetryPolicy()
	policy.sleep = func(_ context.Context, d time.Duration) error {
		slept = append(slept, d)
		return nil
	}

	c, err := New(Options{
		BaseURL: srv.URL, APIKey: "k", OrganizationID: 120547,
		HTTPClient: srv.Client(), Retry: &policy,
	})
	if err != nil {
		t.Fatal(err)
	}

	err = c.Do(context.Background(), Request{
		Method: "GET", Path: "/policies", OrgScope: OrgScopeQuery,
	}, nil)

	if err == nil {
		t.Fatal("expected an error rather than a day-long sleep")
	}
	if len(slept) != 0 {
		t.Errorf("slept %v; the request should have been abandoned, not waited out", slept)
	}
	if attempts != 1 {
		t.Errorf("made %d attempts, want 1", attempts)
	}
	// The message must say what happened, so this is not mistaken for a bug in
	// the provider.
	for _, want := range []string{"24h", "not retried", "429"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
}

// TestUnitRetry_HonoursRetryAfterWithinTheCeiling confirms the ceiling did not
// break the ordinary case: Automox's documented one-minute rate-limit penalty is
// still waited out.
func TestUnitRetry_HonoursRetryAfterWithinTheCeiling(t *testing.T) {
	var slept []time.Duration
	attempts := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "60")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = fmt.Fprint(w, `{"errors":["rate limited"]}`)
			return
		}
		_, _ = fmt.Fprint(w, `[]`)
	}))
	t.Cleanup(srv.Close)

	policy := DefaultRetryPolicy()
	policy.sleep = func(_ context.Context, d time.Duration) error {
		slept = append(slept, d)
		return nil
	}

	c, err := New(Options{
		BaseURL: srv.URL, APIKey: "k", OrganizationID: 120547,
		HTTPClient: srv.Client(), Retry: &policy,
	})
	if err != nil {
		t.Fatal(err)
	}

	var out []struct{}
	if err := c.Do(context.Background(), Request{
		Method: "GET", Path: "/policies", OrgScope: OrgScopeQuery,
	}, &out); err != nil {
		t.Fatal(err)
	}

	if len(slept) != 1 || slept[0] != 60*time.Second {
		t.Errorf("slept %v, want exactly one 60s wait honouring the server's header", slept)
	}
}
