package client

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-log/tfsdklog"
)

// captureLogs runs fn with a real tflog sink attached and returns everything it
// wrote. Asserting on genuinely emitted output is the point: a test that
// inspected a field map would still pass if the value never reached a log line,
// which is exactly the bug worth catching here.
//
// tflog writes to stderr through hclog rather than the standard log package, so
// capture replaces os.Stderr with a pipe for the duration.
func captureLogs(t *testing.T, fn func(ctx context.Context)) string {
	t.Helper()

	t.Setenv("TF_LOG", "TRACE")

	original := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating capture pipe: %v", err)
	}
	os.Stderr = w

	// Read concurrently so a log volume larger than the pipe buffer cannot
	// deadlock the test.
	captured := make(chan string, 1)
	go func() {
		out, _ := io.ReadAll(r)
		captured <- string(out)
	}()

	ctx := tfsdklog.NewRootProviderLogger(context.Background())
	func() {
		defer func() {
			os.Stderr = original
			_ = w.Close()
		}()
		fn(ctx)
	}()

	return <-captured
}

// The single most important property of this file: the API key must never reach
// a log line. A provider that leaks its credential into TF_LOG output puts it in
// CI artifacts, terminal scrollback, and support tickets.
func TestUnitLogging_NeverLogsTheAPIKey(t *testing.T) {
	const secret = "5f4dcc3b-5aa7-65d6-1d8f-27b6e21fbdcc"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c, err := New(Options{
		BaseURL:        srv.URL,
		APIKey:         secret,
		OrganizationID: 120547,
		HTTPClient:     srv.Client(),
		Retry:          &RetryPolicy{MaxAttempts: 1},
	})
	if err != nil {
		t.Fatal(err)
	}

	logs := captureLogs(t, func(ctx context.Context) {
		_ = c.Do(ctx, Request{Method: "GET", Path: "/policies", OrgScope: OrgScopeQuery}, &map[string]any{})
	})

	if logs == "" {
		t.Fatal("no log output captured; the logging transport is not wired in")
	}
	if strings.Contains(logs, secret) {
		t.Fatalf("the API key appeared in log output:\n%s", logs)
	}
	// The header name should survive so the log is still useful.
	if !strings.Contains(logs, "Authorization") {
		t.Error("expected the Authorization header name to be logged")
	}
	if !strings.Contains(logs, "REDACTED") {
		t.Error("expected the Authorization value to be replaced with REDACTED")
	}
}

func TestUnitLogging_RecordsRequestAndOutcome(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c, _ := New(Options{
		BaseURL: srv.URL, APIKey: "k", OrganizationID: 120547,
		HTTPClient: srv.Client(), Retry: &RetryPolicy{MaxAttempts: 1},
	})

	logs := captureLogs(t, func(ctx context.Context) {
		_ = c.Do(ctx, Request{Method: "GET", Path: "/servergroups", OrgScope: OrgScopeQuery}, &map[string]any{})
	})

	for _, want := range []string{"sending request", "request succeeded", "GET", "servergroups", "duration_ms"} {
		if !strings.Contains(logs, want) {
			t.Errorf("log output missing %q:\n%s", want, logs)
		}
	}
}

// A 4xx is normal traffic for this provider — a 404 during refresh is a deletion,
// and a 403 from /servergroups may be one too. Logging those at error level would
// train practitioners to ignore genuine errors.
func TestUnitLogging_ClientErrorsAreWarningsAndServerErrorsAreErrors(t *testing.T) {
	cases := []struct {
		status    int
		wantLevel string
		wantMsg   string
	}{
		{403, "warn", "request rejected"},
		{404, "warn", "request rejected"},
		{500, "error", "server error"},
		{503, "error", "server error"},
	}

	for _, tc := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(tc.status)
			_, _ = w.Write([]byte(`{"errors":["nope"]}`))
		}))

		c, _ := New(Options{
			BaseURL: srv.URL, APIKey: "k", OrganizationID: 1,
			HTTPClient: srv.Client(), Retry: &RetryPolicy{MaxAttempts: 1},
		})

		logs := captureLogs(t, func(ctx context.Context) {
			_ = c.Do(ctx, Request{Method: "GET", Path: "/x", OrgScope: OrgScopeQuery}, nil)
		})
		srv.Close()

		if !strings.Contains(logs, tc.wantMsg) {
			t.Errorf("status %d: log missing %q:\n%s", tc.status, tc.wantMsg, logs)
		}
		if !strings.Contains(strings.ToLower(logs), `"@level":"`+tc.wantLevel) {
			t.Errorf("status %d: expected level %q in:\n%s", tc.status, tc.wantLevel, logs)
		}
	}
}

// Retry-After is the only case where the server tells us how long to wait, and
// waiting the wrong amount means burning another request against the same limit.
func TestUnitLogging_RecordsRetryAfter(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer srv.Close()

	c, _ := New(Options{
		BaseURL: srv.URL, APIKey: "k", OrganizationID: 1,
		HTTPClient: srv.Client(), Retry: &RetryPolicy{MaxAttempts: 1},
	})

	logs := captureLogs(t, func(ctx context.Context) {
		_ = c.Do(ctx, Request{Method: "GET", Path: "/servers", OrgScope: OrgScopeQuery}, nil)
	})

	if !strings.Contains(logs, "retry_after") {
		t.Errorf("expected retry_after in log output:\n%s", logs)
	}
}

// Bodies may carry device inventories and organization access keys, so they are
// not logged. This guards against someone flipping that on casually.
func TestUnitLogging_DoesNotLogResponseBodies(t *testing.T) {
	const canary = "SUPER-SENSITIVE-ACCESS-KEY-VALUE"

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]string{"access_key": canary})
	}))
	defer srv.Close()

	c, _ := New(Options{
		BaseURL: srv.URL, APIKey: "k", OrganizationID: 1,
		HTTPClient: srv.Client(), Retry: &RetryPolicy{MaxAttempts: 1},
	})

	logs := captureLogs(t, func(ctx context.Context) {
		_ = c.Do(ctx, Request{Method: "GET", Path: "/orgs", OrgScope: OrgScopeNone}, &map[string]any{})
	})

	if strings.Contains(logs, canary) {
		t.Errorf("a response body value reached the logs:\n%s", logs)
	}
}

// Outside a Terraform context tflog is a no-op. Unit tests and the live-check
// harness rely on this: the client must work with no logger configured.
func TestUnitLogging_NoOpWithoutATerraformLogger(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	c, _ := New(Options{
		BaseURL: srv.URL, APIKey: "k", OrganizationID: 1,
		HTTPClient: srv.Client(), Retry: &RetryPolicy{MaxAttempts: 1},
	})

	var out map[string]any
	if err := c.Do(context.Background(), Request{
		Method: "GET", Path: "/orgs", OrgScope: OrgScopeNone,
	}, &out); err != nil {
		t.Fatalf("request failed without a logger in context: %v", err)
	}
	if out["ok"] != true {
		t.Errorf("response not decoded: %v", out)
	}
}

func TestUnitRedactHeaders(t *testing.T) {
	h := http.Header{}
	h.Set("Authorization", "Bearer secret-value")
	h.Set("Cookie", "session=secret-cookie")
	h.Set("X-Api-Key", "secret-key")
	h.Set("Content-Type", "application/json")
	h.Set("Accept", "application/json")

	got := redactHeaders(h)

	for _, name := range []string{"Authorization", "Cookie", "X-Api-Key"} {
		if got[name] != "REDACTED" {
			t.Errorf("%s = %q, want REDACTED", name, got[name])
		}
	}
	if got["Content-Type"] != "application/json" {
		t.Errorf("Content-Type should not be redacted, got %q", got["Content-Type"])
	}
}

// A custom HTTP client must not lose logging, and wrapping must not mutate the
// caller's client.
func TestUnitLogging_WrapsCustomTransportWithoutMutatingCaller(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	caller := srv.Client()
	original := caller.Transport

	c, err := New(Options{BaseURL: srv.URL, APIKey: "k", OrganizationID: 1, HTTPClient: caller})
	if err != nil {
		t.Fatal(err)
	}

	if caller.Transport != original {
		t.Error("New mutated the caller's http.Client; it should wrap a copy")
	}
	if _, ok := c.httpClient.Transport.(*loggingTransport); !ok {
		t.Errorf("client transport = %T, want *loggingTransport", c.httpClient.Transport)
	}
}
