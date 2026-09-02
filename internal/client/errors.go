package client

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"
)

// maxBodyExcerpt bounds how much of an unrecognized response body is echoed into a
// diagnostic. Enough to identify the failure, not enough to dump a payload into
// Terraform output.
const maxBodyExcerpt = 512

// APIError is the single error type the Automox client returns for any non-2xx
// response. It always carries the HTTP status and the operation attempted, so a
// failure can never surface as an empty result or a bare "request failed".
type APIError struct {
	StatusCode int
	Method     string
	Path       string

	// Summary is a short human-readable cause, extracted from whichever envelope
	// the API used.
	Summary string

	// FieldErrors maps a request field to the messages the API returned for it.
	// Populated for the validation envelopes; nil otherwise.
	FieldErrors map[string][]string

	// Envelope names which of the five known response shapes was decoded, or
	// "unrecognized". Useful when the API adds a sixth.
	Envelope string

	// BodyExcerpt is set only when no known envelope matched, so an unfamiliar
	// shape still produces an actionable message instead of silence.
	BodyExcerpt string

	// RetryAfter carries the server's own backoff guidance when it sends a
	// Retry-After header. Zero means the header was absent or unparseable.
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "automox api: %s %s returned %d", e.Method, e.Path, e.StatusCode)

	if e.Summary != "" {
		fmt.Fprintf(&b, ": %s", e.Summary)
	}

	if len(e.FieldErrors) != 0 {
		fields := make([]string, 0, len(e.FieldErrors))
		for f := range e.FieldErrors {
			fields = append(fields, f)
		}
		sort.Strings(fields)

		parts := make([]string, 0, len(fields))
		for _, f := range fields {
			parts = append(parts, fmt.Sprintf("%s: %s", f, strings.Join(e.FieldErrors[f], "; ")))
		}
		fmt.Fprintf(&b, " (%s)", strings.Join(parts, ", "))
	}

	if e.BodyExcerpt != "" {
		fmt.Fprintf(&b, " [unrecognized error envelope; body: %s]", e.BodyExcerpt)
	}

	return b.String()
}

// IsNotFound reports a clean 404.
//
// This deliberately tests the status code and nothing else, and on its own it is
// NOT sufficient to decide that a resource should be dropped from Terraform state.
// Automox is inconsistent: /policies returns 404 for an absent id, but
// /servergroups returns 403 for one. Use IsGone in gone.go, which takes the
// endpoint's reporting style into account.
//
// A 403 is never absence by itself. The API returns it for a credential lacking
// write scope, for Full-Administrator-only endpoints, and for absent ids on some
// endpoints. Treating it as "gone" unconditionally would let a credential
// regression look like deletion and make Terraform plan the destruction and
// recreation of live patch policies governing production endpoints.
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// IsForbidden reports an authorization failure. Callers surface this; they never
// convert it into absence or an empty collection.
func IsForbidden(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusForbidden
}

// IsRetryable reports whether the request may be retried after a backoff. 429 is
// rate limiting (GET /servers is capped at <30 req/min and returns 429 for a full
// minute); 409 is a conflict the scheduled-window endpoint documents; 503 is
// transient unavailability.
func IsRetryable(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.StatusCode {
	case http.StatusTooManyRequests, http.StatusConflict, http.StatusServiceUnavailable:
		return true
	default:
		return false
	}
}

// decodeError builds an APIError from a non-2xx response body.
//
// The Automox API uses five distinct error envelopes, not the two its OpenAPI
// document declares. All five were observed live on 2026-07-30; see
// specs/.../contracts/error-envelopes.md. Detection is by body shape rather than
// Content-Type, because RFC 9457 payloads are served as application/json.
//
// Order matters, because the shapes overlap: `errors` holds a string array in the
// legacy envelope and a field-keyed object in the validation envelope.
func decodeError(statusCode int, method, path string, body []byte) *APIError {
	apiErr := &APIError{
		StatusCode: statusCode,
		Method:     method,
		Path:       path,
	}

	var raw map[string]json.RawMessage
	if len(body) == 0 || json.Unmarshal(body, &raw) != nil {
		// Not JSON at all. DELETE /remotecontrol-st/uninstall returns text/plain,
		// and gateways can return HTML.
		apiErr.Envelope = "unrecognized"
		apiErr.Summary = http.StatusText(statusCode)
		apiErr.BodyExcerpt = excerpt(body)
		return apiErr
	}

	// Shape 4 — RFC 9457 plus a non-standard invalidFields map. Checked first
	// because it also carries title/detail/status and would otherwise decode as
	// shape 2, losing the per-field detail.
	if rawFields, ok := raw["invalidFields"]; ok {
		var fields map[string]string
		if json.Unmarshal(rawFields, &fields) == nil {
			apiErr.Envelope = "invalid-fields"
			apiErr.Summary = joinNonEmpty(": ", stringField(raw, "title"), stringField(raw, "detail"))
			apiErr.FieldErrors = make(map[string][]string, len(fields))
			for field, msg := range fields {
				apiErr.FieldErrors[field] = []string{msg}
			}
			return apiErr
		}
	}

	if rawErrors, ok := raw["errors"]; ok {
		// Shape 3 — legacy validation: {"errors": {"field": ["message"]}}
		var fieldMap map[string][]string
		if json.Unmarshal(rawErrors, &fieldMap) == nil && len(fieldMap) > 0 {
			apiErr.Envelope = "legacy-field-map"
			apiErr.Summary = "validation failed"
			apiErr.FieldErrors = fieldMap
			return apiErr
		}

		// Shape 2 variant — RFC 9457 with structured errors:
		// {"errors": [{"field": "...", "message": "..."}]}
		var structured []struct {
			Field   string `json:"field"`
			Message string `json:"message"`
		}
		if json.Unmarshal(rawErrors, &structured) == nil && len(structured) > 0 && structured[0].Field != "" {
			apiErr.Envelope = "problem-details"
			apiErr.Summary = joinNonEmpty(": ", stringField(raw, "title"), stringField(raw, "detail"))
			apiErr.FieldErrors = make(map[string][]string, len(structured))
			for _, fe := range structured {
				apiErr.FieldErrors[fe.Field] = append(apiErr.FieldErrors[fe.Field], fe.Message)
			}
			return apiErr
		}

		// Shape 1 — legacy: {"errors": ["message"]}. The document caps this at one
		// element; join defensively in case that changes.
		var messages []string
		if json.Unmarshal(rawErrors, &messages) == nil && len(messages) > 0 {
			apiErr.Envelope = "legacy"
			apiErr.Summary = strings.Join(messages, "; ")
			return apiErr
		}
	}

	// Shape 2 — RFC 9457 problem details without field errors.
	if title := stringField(raw, "title"); title != "" {
		apiErr.Envelope = "problem-details"
		apiErr.Summary = joinNonEmpty(": ", title, stringField(raw, "detail"))
		return apiErr
	}

	// Shape 5 — Spring Boot default: {timestamp, status, error, path}. No detail
	// and no errors key, so it is only reachable once the others are ruled out.
	if errText := stringField(raw, "error"); errText != "" {
		if _, hasTimestamp := raw["timestamp"]; hasTimestamp {
			apiErr.Envelope = "spring-default"
			apiErr.Summary = errText
			return apiErr
		}
	}

	// JSON, but none of the five known shapes. Never silent — Constitution V.
	apiErr.Envelope = "unrecognized"
	apiErr.Summary = http.StatusText(statusCode)
	apiErr.BodyExcerpt = excerpt(body)
	return apiErr
}

func stringField(raw map[string]json.RawMessage, key string) string {
	v, ok := raw[key]
	if !ok {
		return ""
	}
	var s string
	if json.Unmarshal(v, &s) != nil {
		return ""
	}
	return s
}

func joinNonEmpty(sep string, parts ...string) string {
	kept := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, sep)
}

func excerpt(body []byte) string {
	s := strings.TrimSpace(string(body))
	if s == "" {
		return "(empty)"
	}
	if len(s) > maxBodyExcerpt {
		return s[:maxBodyExcerpt] + "…(truncated)"
	}
	return s
}
