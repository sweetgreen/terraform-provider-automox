package acceptance_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/sweetgreen/terraform-provider-automox/internal/acceptance"
	"github.com/sweetgreen/terraform-provider-automox/internal/client"
)

// TestUnitIsReadOnlyCredential separates "Automox refused the write" from "the
// probe broke".
//
// Only the first justifies skipping the write tests. Treating the second as a
// read-only credential would turn every write test into a silent no-op while the
// suite reported success — a transient 503 during the probe would be enough.
func TestUnitIsReadOnlyCredential(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{name: "no error at all", err: nil, want: false},
		{
			name: "forbidden is a read-only credential",
			err:  &client.APIError{StatusCode: 403, Summary: "Forbidden"},
			want: true,
		},
		{
			name: "unauthorized is also a credential problem",
			err:  &client.APIError{StatusCode: 401, Summary: "Unauthorized"},
			want: true,
		},
		// The ones that must NOT skip.
		{
			name: "service unavailable is not evidence about scope",
			err:  &client.APIError{StatusCode: 503, Summary: "Service Unavailable"},
			want: false,
		},
		{
			name: "rate limited is not evidence about scope",
			err:  &client.APIError{StatusCode: 429, Summary: "Too Many Requests"},
			want: false,
		},
		{
			// Automox validates the body before checking authorization, so a probe
			// sending an incomplete body is answered 400 and never reaches the
			// permission check. Reading that as read-only would be wrong.
			name: "validation failure is not evidence about scope",
			err:  &client.APIError{StatusCode: 400, Summary: "The name field is required."},
			want: false,
		},
		{
			name: "transport failure is not evidence about scope",
			err:  errors.New("dial tcp: connection refused"),
			want: false,
		},
		{
			name: "wrapped forbidden is still recognised",
			err:  fmt.Errorf("listing groups: %w", &client.APIError{StatusCode: 403}),
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := acceptance.IsReadOnlyCredential(tc.err); got != tc.want {
				t.Errorf("IsReadOnlyCredential(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
