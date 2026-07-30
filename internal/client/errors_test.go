package client

import (
	"net/http"
	"strings"
	"testing"
)

// Every body below is a verbatim response captured from the live Automox API on
// 2026-07-30 (org 120547). The API's OpenAPI document declares two error formats;
// these are the five that actually occur.
func TestUnitDecodeError_AllFiveLiveEnvelopes(t *testing.T) {
	cases := []struct {
		name         string
		status       int
		body         string
		wantEnvelope string
		wantInSummry string
		wantFields   map[string][]string
	}{
		{
			name:   "shape 1 legacy string array",
			status: 403,
			body: `{"errors":["You do not have permission to perform this action. ` +
				`Please contact your system administrator for support."]}`,
			wantEnvelope: "legacy",
			wantInSummry: "do not have permission",
		},
		{
			name:   "shape 2 rfc 9457 problem details",
			status: 403,
			body: `{"type":"about:blank","title":"Forbidden","status":403,` +
				`"detail":"Insufficient permissions for this operation","instance":"/api/global/api_keys"}`,
			wantEnvelope: "problem-details",
			wantInSummry: "Forbidden: Insufficient permissions",
		},
		{
			name:         "shape 3 legacy field-keyed object",
			status:       400,
			body:         `{"errors":{"server_groups":["The server_groups field is required."]}}`,
			wantEnvelope: "legacy-field-map",
			wantFields:   map[string][]string{"server_groups": {"The server_groups field is required."}},
		},
		{
			name:   "shape 4 invalidFields map",
			status: 400,
			body: `{"type":"about:blank","title":"Bad Request","status":400,"detail":"Validation failed",` +
				`"instance":"/org/3f23a2f9-4e0b-4ab4-9c76-0eb088793697","invalidFields":{` +
				`"durationMinutes":"duration_minutes is derived from dtstart->UNTIL for ONCE windows and must not be supplied",` +
				`"windowType":"Invalid window type: exclusion. Valid values are: exclude"}}`,
			wantEnvelope: "invalid-fields",
			wantInSummry: "Bad Request: Validation failed",
			wantFields: map[string][]string{
				"durationMinutes": {"duration_minutes is derived from dtstart->UNTIL for ONCE windows and must not be supplied"},
				"windowType":      {"Invalid window type: exclusion. Valid values are: exclude"},
			},
		},
		{
			name:   "shape 5 spring boot default",
			status: 403,
			body: `{"timestamp":"2026-07-29T23:50:23.016+00:00","status":403,"error":"Forbidden",` +
				`"path":"/org/3f23a2f9-4e0b-4ab4-9c76-0eb088793697"}`,
			wantEnvelope: "spring-default",
			wantInSummry: "Forbidden",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := decodeError(tc.status, "POST", "/servergroups", []byte(tc.body))

			if err.Envelope != tc.wantEnvelope {
				t.Errorf("Envelope = %q, want %q", err.Envelope, tc.wantEnvelope)
			}
			if tc.wantInSummry != "" && !strings.Contains(err.Summary, tc.wantInSummry) {
				t.Errorf("Summary = %q, want it to contain %q", err.Summary, tc.wantInSummry)
			}
			for field, want := range tc.wantFields {
				got, ok := err.FieldErrors[field]
				if !ok {
					t.Errorf("FieldErrors missing %q; got %v", field, err.FieldErrors)
					continue
				}
				if len(got) != len(want) || (len(want) > 0 && got[0] != want[0]) {
					t.Errorf("FieldErrors[%q] = %v, want %v", field, got, want)
				}
			}

			// Whatever the shape, the message must always name status and operation.
			msg := err.Error()
			for _, required := range []string{"POST", "/servergroups"} {
				if !strings.Contains(msg, required) {
					t.Errorf("Error() = %q, missing %q", msg, required)
				}
			}
		})
	}
}

// The highest-consequence mapping in this client.
//
// Automox returns 403 for a credential lacking write scope and for endpoints
// requiring Full Administrator. The current organization-scoped key returns 403 on
// every write, so this path is exercised constantly. If 403 were read as "resource
// absent", Terraform would drop live objects from state and plan to recreate them —
// proposing the destruction of patch policies that govern 1467 production
// endpoints.
func TestUnitForbiddenIsNeverTreatedAsNotFound(t *testing.T) {
	forbiddenBodies := map[string]string{
		"legacy envelope": `{"errors":["You do not have permission to perform this action."]}`,
		"problem details": `{"title":"Forbidden","status":403,"detail":"Insufficient permissions for this operation"}`,
		"spring default":  `{"timestamp":"2026-07-29T23:50:23.016+00:00","status":403,"error":"Forbidden","path":"/org/x"}`,
		"empty body":      ``,
	}

	for name, body := range forbiddenBodies {
		t.Run(name, func(t *testing.T) {
			err := decodeError(http.StatusForbidden, "POST", "/servergroups", []byte(body))

			if IsNotFound(err) {
				t.Fatal("IsNotFound returned true for a 403; a permissions failure must never " +
					"be read as absence, or Terraform will plan to destroy live policies")
			}
			if !IsForbidden(err) {
				t.Error("IsForbidden returned false for a 403")
			}
			if IsRetryable(err) {
				t.Error("IsRetryable returned true for a 403; retrying will not grant permission")
			}
		})
	}
}

func TestUnitNotFoundIsTheOnlyAbsence(t *testing.T) {
	gone := decodeError(http.StatusNotFound, "GET", "/policies/1", []byte(`{"errors":["Not found"]}`))
	if !IsNotFound(gone) {
		t.Error("IsNotFound returned false for a 404")
	}

	// Nothing else counts as absence, including statuses that might loosely read
	// as "the thing isn't there".
	for _, status := range []int{400, 401, 403, 409, 422, 429, 500, 502, 503} {
		err := decodeError(status, "GET", "/policies/1", []byte(`{"errors":["nope"]}`))
		if IsNotFound(err) {
			t.Errorf("IsNotFound returned true for status %d", status)
		}
	}
}

func TestUnitIsRetryable(t *testing.T) {
	retryable := []int{http.StatusTooManyRequests, http.StatusConflict, http.StatusServiceUnavailable}
	for _, status := range retryable {
		if !IsRetryable(decodeError(status, "GET", "/servers", nil)) {
			t.Errorf("status %d should be retryable", status)
		}
	}

	for _, status := range []int{400, 401, 403, 404, 422, 500} {
		if IsRetryable(decodeError(status, "GET", "/servers", nil)) {
			t.Errorf("status %d should not be retryable", status)
		}
	}
}

// An unfamiliar body must still produce something actionable. Silence here would
// be the failure mode Constitution V exists to prevent.
func TestUnitDecodeError_UnrecognizedEnvelopeStillReports(t *testing.T) {
	cases := map[string]string{
		"html from a gateway": `<html><head><title>502 Bad Gateway</title></head></html>`,
		"plain text":          `Internal Server Error`,
		"unknown json shape":  `{"unexpected":"structure","code":42}`,
		"empty":               ``,
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			err := decodeError(http.StatusBadGateway, "GET", "/policies", []byte(body))

			if err.Envelope != "unrecognized" {
				t.Errorf("Envelope = %q, want unrecognized", err.Envelope)
			}
			msg := err.Error()
			if !strings.Contains(msg, "502") {
				t.Errorf("Error() = %q, must name the status", msg)
			}
			if err.BodyExcerpt == "" {
				t.Error("BodyExcerpt is empty; an unknown envelope must still show the body")
			}
		})
	}
}

func TestUnitDecodeError_BoundsTheBodyExcerpt(t *testing.T) {
	huge := strings.Repeat("x", maxBodyExcerpt*4)
	err := decodeError(500, "GET", "/servers", []byte(huge))

	if len(err.BodyExcerpt) > maxBodyExcerpt+len("…(truncated)") {
		t.Errorf("BodyExcerpt length %d exceeds the bound", len(err.BodyExcerpt))
	}
	if !strings.Contains(err.BodyExcerpt, "truncated") {
		t.Error("a truncated excerpt should say so")
	}
}

// Regression guard for the shape-1/shape-3 collision: both use the key `errors`,
// distinguished only by whether its value is an array or an object. Branching on
// presence rather than JSON type silently loses the field detail.
func TestUnitDecodeError_DistinguishesErrorsArrayFromErrorsObject(t *testing.T) {
	asArray := decodeError(400, "POST", "/x", []byte(`{"errors":["flat message"]}`))
	if asArray.Envelope != "legacy" {
		t.Errorf("array form decoded as %q, want legacy", asArray.Envelope)
	}
	if len(asArray.FieldErrors) != 0 {
		t.Errorf("array form should have no field errors, got %v", asArray.FieldErrors)
	}

	asObject := decodeError(400, "POST", "/x", []byte(`{"errors":{"field_a":["msg a"]}}`))
	if asObject.Envelope != "legacy-field-map" {
		t.Errorf("object form decoded as %q, want legacy-field-map", asObject.Envelope)
	}
	if len(asObject.FieldErrors) != 1 {
		t.Errorf("object form should carry field errors, got %v", asObject.FieldErrors)
	}
}

// Shape 4 also carries title/detail/status, so it would decode as shape 2 if
// invalidFields were not checked first — losing exactly the per-field guidance a
// practitioner needs to fix their configuration.
func TestUnitDecodeError_InvalidFieldsWinsOverProblemDetails(t *testing.T) {
	body := `{"title":"Bad Request","status":400,"detail":"Validation failed",` +
		`"invalidFields":{"windowType":"Invalid window type: exclusion. Valid values are: exclude"}}`

	err := decodeError(400, "POST", "/policy-windows/org/x", []byte(body))

	if err.Envelope != "invalid-fields" {
		t.Fatalf("Envelope = %q, want invalid-fields", err.Envelope)
	}
	if _, ok := err.FieldErrors["windowType"]; !ok {
		t.Fatalf("lost the per-field detail: %v", err.FieldErrors)
	}
	if !strings.Contains(err.Error(), "windowType") {
		t.Errorf("Error() = %q should name the offending field", err.Error())
	}
}

func TestUnitAPIError_NonAPIErrorsAreNotMisclassified(t *testing.T) {
	plain := errString("connection reset by peer")
	if IsNotFound(plain) || IsForbidden(plain) || IsRetryable(plain) {
		t.Error("a non-APIError must not classify as not-found, forbidden, or retryable")
	}
}

type errString string

func (e errString) Error() string { return string(e) }
