package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	c, err := New(Options{
		BaseURL:        srv.URL,
		APIKey:         "test-key",
		OrganizationID: 120547,
		HTTPClient:     srv.Client(),
		Retry:          &RetryPolicy{MaxAttempts: 1},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, srv
}

func TestUnitNew_ValidatesConfiguration(t *testing.T) {
	cases := []struct {
		name   string
		opts   Options
		expect string
	}{
		{"missing api key", Options{BaseURL: "https://example.com"}, "api_key is required"},
		{"blank api key", Options{APIKey: "   "}, "api_key is required"},
		{"schemeless base url", Options{APIKey: "k", BaseURL: "example.com/api"}, "must include a scheme"},
		{"unparseable base url", Options{APIKey: "k", BaseURL: "ht tp://x"}, "not a valid URL"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := New(tc.opts)
			if err == nil {
				t.Fatal("expected a configuration error")
			}
			if !strings.Contains(err.Error(), tc.expect) {
				t.Errorf("error %q should mention %q", err, tc.expect)
			}
		})
	}

	c, err := New(Options{APIKey: "k"})
	if err != nil {
		t.Fatalf("defaults should be valid: %v", err)
	}
	if c.baseURL.String() != DefaultBaseURL {
		t.Errorf("default base URL = %q, want %q", c.baseURL, DefaultBaseURL)
	}
}

// The key belongs in the Authorization header and nowhere else. A key in a query
// string leaks into access logs and into any error that echoes the URL.
func TestUnitDo_SendsBearerTokenInHeaderOnly(t *testing.T) {
	var gotAuth, gotRawQuery string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotRawQuery = r.URL.RawQuery
		w.Write([]byte(`{}`))
	})

	if err := c.Do(context.Background(), Request{
		Method: "GET", Path: "/policies", OrgScope: OrgScopeQuery,
	}, &map[string]any{}); err != nil {
		t.Fatalf("Do: %v", err)
	}

	if gotAuth != "Bearer test-key" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if strings.Contains(gotRawQuery, "test-key") {
		t.Errorf("api key leaked into the query string: %q", gotRawQuery)
	}
}

func TestUnitDo_AppliesOrgScoping(t *testing.T) {
	var got url.Values
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		w.Write([]byte(`{}`))
	})

	t.Run("query scope adds o", func(t *testing.T) {
		err := c.Do(context.Background(), Request{
			Method: "GET", Path: "/servergroups", OrgScope: OrgScopeQuery,
		}, &map[string]any{})
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		if got.Get("o") != "120547" {
			t.Errorf("o = %q, want 120547", got.Get("o"))
		}
	})

	t.Run("none scope omits o", func(t *testing.T) {
		err := c.Do(context.Background(), Request{
			Method: "GET", Path: "/orgs", OrgScope: OrgScopeNone,
		}, &map[string]any{})
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		if got.Has("o") {
			t.Errorf("unscoped endpoint should not carry o, got %q", got.Get("o"))
		}
	})

	t.Run("explicit o is not overwritten", func(t *testing.T) {
		err := c.Do(context.Background(), Request{
			Method: "GET", Path: "/servergroups", OrgScope: OrgScopeQuery,
			Query: url.Values{"o": []string{"999"}},
		}, &map[string]any{})
		if err != nil {
			t.Fatalf("Do: %v", err)
		}
		if got.Get("o") != "999" {
			t.Errorf("explicit o was overwritten: %q", got.Get("o"))
		}
	})
}

// Automox uses query parameter names that are not valid Go identifiers, so they
// must survive verbatim rather than being normalised by a struct encoder.
func TestUnitDo_PreservesAwkwardQueryParameterNames(t *testing.T) {
	var got url.Values
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		w.Write([]byte(`{}`))
	})

	awkward := url.Values{
		"status:in":                 []string{"ready"},
		"created_at:greater_than":   []string{"2026-01-01"},
		"policy_id[]":               []string{"1", "2"},
		"configuration_id:is_set":   []string{"true"},
		"solution_details_severity": []string{"critical"},
	}

	if err := c.Do(context.Background(), Request{
		Method: "GET", Path: "/data-extracts", OrgScope: OrgScopeQuery, Query: awkward,
	}, &map[string]any{}); err != nil {
		t.Fatalf("Do: %v", err)
	}

	for key, want := range awkward {
		gotVals := got[key]
		if len(gotVals) != len(want) {
			t.Errorf("parameter %q = %v, want %v", key, gotVals, want)
		}
	}
}

func TestUnitDo_MissingOrgIsAnExplicitError(t *testing.T) {
	c, err := New(Options{APIKey: "k", BaseURL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}

	err = c.Do(context.Background(), Request{
		Method: "GET", Path: "/policies", OrgScope: OrgScopeQuery,
	}, nil)

	if err == nil {
		t.Fatal("expected an error when no organization is configured")
	}
	if !strings.Contains(err.Error(), "organization_id is not configured") {
		t.Errorf("error %q should name the missing configuration", err)
	}
}

// PUT and DELETE on policies, server groups, and devices all return 204.
func TestUnitDo_HandlesEmptyResponseBodies(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	if err := c.Do(context.Background(), Request{
		Method: "DELETE", Path: "/servergroups/1", OrgScope: OrgScopeQuery,
	}, nil); err != nil {
		t.Errorf("204 with nil out: %v", err)
	}

	var out map[string]any
	if err := c.Do(context.Background(), Request{
		Method: "DELETE", Path: "/servergroups/1", OrgScope: OrgScopeQuery,
	}, &out); err != nil {
		t.Errorf("204 with non-nil out should not error: %v", err)
	}
}

func TestUnitDo_SurfacesUndecodableSuccessBody(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`<html>not json</html>`))
	})

	var out map[string]any
	err := c.Do(context.Background(), Request{
		Method: "GET", Path: "/policies", OrgScope: OrgScopeQuery,
	}, &out)

	if err == nil {
		t.Fatal("a 200 with an undecodable body must not be reported as success")
	}
	if !strings.Contains(err.Error(), "could not be decoded") {
		t.Errorf("error %q should explain the decode failure", err)
	}
}

func TestUnitDo_NonSuccessBecomesAPIError(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"errors":["You do not have permission to perform this action."]}`))
	})

	err := c.Do(context.Background(), Request{
		Method: "POST", Path: "/servergroups", OrgScope: OrgScopeQuery,
	}, nil)

	if !IsForbidden(err) {
		t.Fatalf("expected a forbidden APIError, got %v", err)
	}
	if IsNotFound(err) {
		t.Error("403 must not be classified as absence")
	}
}

func TestUnitDo_SendsJSONBody(t *testing.T) {
	var got map[string]any
	var contentType string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		contentType = r.Header.Get("Content-Type")
		json.NewDecoder(r.Body).Decode(&got)
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":1}`))
	})

	body := map[string]any{"name": "TESTING-group", "refresh_interval": 1440}
	var out map[string]any
	if err := c.Do(context.Background(), Request{
		Method: "POST", Path: "/servergroups", OrgScope: OrgScopeQuery, Body: body,
	}, &out); err != nil {
		t.Fatalf("Do: %v", err)
	}

	if contentType != "application/json" {
		t.Errorf("Content-Type = %q", contentType)
	}
	if got["name"] != "TESTING-group" {
		t.Errorf("body not transmitted: %v", got)
	}
}

func TestUnitRedactURL(t *testing.T) {
	got := redactURL("https://console.automox.com/api/policies?o=1&api_key=supersecret&token=abc")
	for _, leaked := range []string{"supersecret", "abc"} {
		if strings.Contains(got, leaked) {
			t.Errorf("redactURL leaked %q: %s", leaked, got)
		}
	}
	if !strings.Contains(got, "o=1") {
		t.Errorf("redactURL dropped a benign parameter: %s", got)
	}
}

// --- retry ---

func TestUnitRetry_RetriesRateLimitThenSucceeds(t *testing.T) {
	var calls int32
	slept := []time.Duration{}

	policy := RetryPolicy{
		MaxAttempts:      3,
		InitialBackoff:   time.Second,
		MaxBackoff:       10 * time.Second,
		RateLimitBackoff: 60 * time.Second,
		sleep: func(_ context.Context, d time.Duration) error {
			slept = append(slept, d)
			return nil
		},
	}

	body, err := policy.Do(context.Background(), func() ([]byte, error) {
		if atomic.AddInt32(&calls, 1) < 3 {
			return nil, decodeError(429, "GET", "/servers", nil)
		}
		return []byte(`ok`), nil
	})

	if err != nil {
		t.Fatalf("expected eventual success: %v", err)
	}
	if string(body) != "ok" {
		t.Errorf("body = %q", body)
	}
	if calls != 3 {
		t.Errorf("attempts = %d, want 3", calls)
	}
	// Automox holds a 429 for a full minute, so a sub-minute retry just spends
	// another request against the same limit.
	for _, d := range slept {
		if d != 60*time.Second {
			t.Errorf("rate-limit backoff = %v, want the documented 60s", d)
		}
	}
}

func TestUnitRetry_HonoursRetryAfterHeader(t *testing.T) {
	var slept time.Duration
	policy := RetryPolicy{
		MaxAttempts:      2,
		InitialBackoff:   time.Second,
		RateLimitBackoff: 60 * time.Second,
		sleep: func(_ context.Context, d time.Duration) error {
			slept = d
			return nil
		},
	}

	first := true
	_, err := policy.Do(context.Background(), func() ([]byte, error) {
		if first {
			first = false
			e := decodeError(429, "GET", "/servers", nil)
			e.RetryAfter = 5 * time.Second
			return nil, e
		}
		return []byte(`ok`), nil
	})

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if slept != 5*time.Second {
		t.Errorf("slept %v, want the server-provided 5s", slept)
	}
}

// Retrying a 403 cannot help and triples latency on the most common failure under
// a restricted credential.
func TestUnitRetry_DoesNotRetryNonRetryableStatuses(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 422, 500} {
		var calls int
		policy := RetryPolicy{
			MaxAttempts:    5,
			InitialBackoff: time.Millisecond,
			sleep:          func(context.Context, time.Duration) error { return nil },
		}

		_, err := policy.Do(context.Background(), func() ([]byte, error) {
			calls++
			return nil, decodeError(status, "POST", "/servergroups", nil)
		})

		if err == nil {
			t.Errorf("status %d should fail", status)
		}
		if calls != 1 {
			t.Errorf("status %d attempted %d times, want 1", status, calls)
		}
	}
}

func TestUnitRetry_GivesUpAfterMaxAttempts(t *testing.T) {
	var calls int
	policy := RetryPolicy{
		MaxAttempts:    3,
		InitialBackoff: time.Millisecond,
		sleep:          func(context.Context, time.Duration) error { return nil },
	}

	_, err := policy.Do(context.Background(), func() ([]byte, error) {
		calls++
		return nil, decodeError(503, "GET", "/policies", nil)
	})

	if calls != 3 {
		t.Errorf("attempts = %d, want 3", calls)
	}
	if err == nil || !strings.Contains(err.Error(), "giving up after 3 attempts") {
		t.Errorf("error %v should say how many attempts were made", err)
	}
	if !IsRetryable(err) && !strings.Contains(err.Error(), "503") {
		t.Errorf("the underlying status should remain visible: %v", err)
	}
}

func TestUnitRetry_CancellationReportsOriginalFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	policy := RetryPolicy{MaxAttempts: 3, InitialBackoff: time.Second}

	_, err := policy.Do(ctx, func() ([]byte, error) {
		return nil, decodeError(429, "GET", "/servers", nil)
	})

	if err == nil {
		t.Fatal("expected an error")
	}
	// A bare context.Canceled would hide that the request was being rate limited.
	if !strings.Contains(err.Error(), "429") {
		t.Errorf("error %q should preserve the original failure", err)
	}
	if !strings.Contains(err.Error(), "retry abandoned") {
		t.Errorf("error %q should explain why retrying stopped", err)
	}
}

// --- organization resolution ---

func TestUnitOrganizationUUID_ResolvesAndCaches(t *testing.T) {
	var calls int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.Write([]byte(`[{"id":120547,"uuid":"3f23a2f9-4e0b-4ab4-9c76-0eb088793697","name":"Sweetgreen"}]`))
	})

	for i := 0; i < 3; i++ {
		got, err := c.DefaultOrganizationUUID(context.Background())
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		if got != "3f23a2f9-4e0b-4ab4-9c76-0eb088793697" {
			t.Errorf("uuid = %q", got)
		}
	}

	if calls != 1 {
		t.Errorf("GET /orgs called %d times, want 1 (result should be cached)", calls)
	}
}

func TestUnitOrganizationUUID_UnknownOrgNamesWhatIsVisible(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id":120547,"uuid":"abc","name":"Sweetgreen"}]`))
	})

	_, err := c.OrganizationUUID(context.Background(), 999999)
	if err == nil {
		t.Fatal("expected an error for an unknown organization")
	}
	for _, want := range []string{"999999", "120547", "Sweetgreen"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q so the misconfiguration is obvious", err, want)
		}
	}
}

// A transient failure must not be cached, or one blip would poison every later
// lookup and fail an entire apply.
func TestUnitOrganizationUUID_DoesNotCacheFailures(t *testing.T) {
	var calls int32
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			w.Write([]byte(`{"errors":["boom"]}`))
			return
		}
		w.Write([]byte(`[{"id":120547,"uuid":"abc","name":"Sweetgreen"}]`))
	})

	if _, err := c.DefaultOrganizationUUID(context.Background()); err == nil {
		t.Fatal("first call should fail")
	}

	got, err := c.DefaultOrganizationUUID(context.Background())
	if err != nil {
		t.Fatalf("second call should succeed after a transient failure: %v", err)
	}
	if got != "abc" {
		t.Errorf("uuid = %q", got)
	}
}

func TestUnitOrganizationUUID_MissingUUIDIsExplicit(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id":120547,"name":"Sweetgreen"}]`))
	})

	_, err := c.OrganizationUUID(context.Background(), 120547)
	if err == nil || !strings.Contains(err.Error(), "no uuid") {
		t.Errorf("error %v should explain that the uuid is absent", err)
	}
}

func TestUnitOrganizationUUID_RequiresAnID(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {})
	if _, err := c.OrganizationUUID(context.Background(), 0); err == nil {
		t.Error("resolving organization 0 should be an error")
	}
}
