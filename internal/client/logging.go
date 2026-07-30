package client

import (
	"net/http"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// This provider exists because the Automox API behaves unlike its published
// document in a dozen material ways. A practitioner hitting a thirteenth
// divergence needs to see the request and response that caused it, so HTTP
// logging is a feature, not diagnostics scaffolding.
//
// Logging is implemented as an http.RoundTripper rather than at each call site,
// so every request is covered by construction and no future endpoint can be added
// without it. tflog writes into Terraform's own log stream, surfacing under
// TF_LOG=DEBUG, and degrades to a no-op when no Terraform logger is in the
// context — which is what keeps the httptest-based unit tests dependency-free.

// redactedHeaders never have their values logged. This is the primary control
// protecting the API key in log output — see the note on loggingTransport.secrets
// for why tflog's own masking does not cover this case.
//
// Authorization carries the API key. The others are listed because credentials
// migrate into new headers over time, and a denylist that only knew about
// Authorization would silently start leaking the day one moved.
var redactedHeaders = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"cookie":              true,
	"set-cookie":          true,
	"x-api-key":           true,
}

// loggingTransport logs each request and its outcome.
type loggingTransport struct {
	next http.RoundTripper

	// secrets are registered with tflog's field masking.
	//
	// Scope, measured rather than assumed: tflog masking replaces matching
	// *top-level* field values. It does NOT reach inside a nested map, so it
	// would not catch the credential in the "headers" field this transport logs.
	// Verified by disabling redactHeaders with masking left on — the key still
	// appeared in output, for both MaskAllFieldValuesStrings and MaskLogStrings.
	//
	// So redactHeaders is the control that actually protects the credential here,
	// and TestUnitLogging_NeverLogsTheAPIKey is what proves it. Masking is kept
	// because it does cover a different and plausible accident: a future call
	// site passing the key as a top-level field. It is a second net over a
	// different hole, not a backstop for this one.
	secrets []string
}

func newLoggingTransport(next http.RoundTripper, secrets ...string) http.RoundTripper {
	if next == nil {
		next = http.DefaultTransport
	}

	nonEmpty := make([]string, 0, len(secrets))
	for _, s := range secrets {
		if strings.TrimSpace(s) != "" {
			nonEmpty = append(nonEmpty, s)
		}
	}

	return &loggingTransport{next: next, secrets: nonEmpty}
}

func (t *loggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	if len(t.secrets) > 0 {
		ctx = tflog.MaskAllFieldValuesStrings(ctx, t.secrets...)
	}

	fields := map[string]any{
		"method": req.Method,
		// The path is logged without the query string, because query parameters
		// are the likeliest place for a credential to appear by accident.
		"url":     redactURL(req.URL.String()),
		"host":    req.URL.Host,
		"headers": redactHeaders(req.Header),
	}

	tflog.Debug(ctx, "automox: sending request", fields)

	start := time.Now()
	resp, err := t.next.RoundTrip(req)
	elapsed := time.Since(start)

	if err != nil {
		tflog.Error(ctx, "automox: request failed", map[string]any{
			"method":      req.Method,
			"url":         redactURL(req.URL.String()),
			"duration_ms": elapsed.Milliseconds(),
			// err here is a transport failure, not an API error, so it carries no
			// response body and cannot leak one.
			"error": err.Error(),
		})
		return nil, err
	}

	outcome := map[string]any{
		"method":      req.Method,
		"url":         redactURL(req.URL.String()),
		"status":      resp.StatusCode,
		"duration_ms": elapsed.Milliseconds(),
	}

	// A 429 is the one status where the server tells us how long to wait, and
	// waiting the wrong amount is the difference between recovering and burning
	// another request against the same limit.
	if retryAfter := resp.Header.Get("Retry-After"); retryAfter != "" {
		outcome["retry_after"] = retryAfter
	}

	switch {
	case resp.StatusCode >= 500:
		tflog.Error(ctx, "automox: server error", outcome)
	case resp.StatusCode >= 400:
		// Client errors are warnings, not errors: a 404 during refresh is a
		// normal deletion, and a 403 may be a deleted server group. The decoder
		// and IsGone decide which; the log should not pre-judge.
		tflog.Warn(ctx, "automox: request rejected", outcome)
	default:
		tflog.Debug(ctx, "automox: request succeeded", outcome)
	}

	return resp, nil
}

// redactHeaders copies headers with sensitive values replaced. The header *names*
// are kept, because knowing that an Authorization header was sent is useful and
// only its value is secret.
func redactHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for name, values := range h {
		if redactedHeaders[strings.ToLower(name)] {
			out[name] = "REDACTED"
			continue
		}
		out[name] = strings.Join(values, ", ")
	}
	return out
}
