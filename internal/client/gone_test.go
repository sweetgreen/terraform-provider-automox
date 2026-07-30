package client

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
)

func forbidden() error {
	return decodeError(http.StatusForbidden, "GET", "/servergroups/588692",
		[]byte(`{"errors":["You do not have permission to perform this action."]}`))
}

func notFound() error {
	return decodeError(http.StatusNotFound, "GET", "/policies/999999999",
		[]byte(`{"errors":["Not found"]}`))
}

func exists(v bool) ExistsFunc {
	return func(context.Context) (bool, error) { return v, nil }
}

func listFails(msg string) ExistsFunc {
	return func(context.Context) (bool, error) { return false, errors.New(msg) }
}

// /policies returns a clean 404 for an absent id, verified live.
func TestUnitIsGone_NotFoundIsUnambiguous(t *testing.T) {
	for _, mode := range []GoneMode{GoneByNotFound, GoneByForbidden} {
		gone, err := IsGone(context.Background(), notFound(), mode, nil)
		if err != nil {
			t.Errorf("mode %v: unexpected error %v", mode, err)
		}
		if !gone {
			t.Errorf("mode %v: a 404 must count as gone", mode)
		}
	}
}

// The case this file exists for: /servergroups returns 403 for a deleted id.
// Without disambiguation the provider would fail every plan after a normal delete.
func TestUnitIsGone_ForbiddenPlusAbsentFromListingIsGone(t *testing.T) {
	gone, err := IsGone(context.Background(), forbidden(), GoneByForbidden, exists(false))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !gone {
		t.Error("a 403 from an endpoint that reports absence as 403, with the id missing " +
			"from a successful listing, must be treated as gone")
	}
}

// The failure mode that would be worst: a credential regression looking like
// deletion, causing Terraform to propose recreating live objects.
func TestUnitIsGone_ForbiddenWithFailedListingIsNotGone(t *testing.T) {
	gone, err := IsGone(context.Background(), forbidden(), GoneByForbidden,
		listFails("403 listing servergroups"))

	if gone {
		t.Fatal("a 403 must not be read as deletion when the listing also failed; " +
			"that is a lost-credential signature, and treating it as deletion would " +
			"make Terraform plan to recreate live objects")
	}
	if err == nil {
		t.Fatal("expected an error explaining why the answer is unknown")
	}
	if !strings.Contains(err.Error(), "listing also failed") {
		t.Errorf("error %q should explain that the existence check could not run", err)
	}
	// The original API error must survive for the caller to inspect.
	if !IsForbidden(errors.Unwrap(err)) {
		t.Error("the underlying APIError should remain unwrappable")
	}
}

// Readable in the collection but not individually is a shape we do not understand.
// Deleting from state on an unexplained signal is exactly the guess to avoid.
func TestUnitIsGone_ForbiddenButStillListedIsNotGone(t *testing.T) {
	gone, err := IsGone(context.Background(), forbidden(), GoneByForbidden, exists(true))

	if gone {
		t.Fatal("must not report gone while the resource is still in the listing")
	}
	if err == nil || !strings.Contains(err.Error(), "still present") {
		t.Errorf("error %v should say the resource is still listed", err)
	}
}

// An endpoint that reports absence properly must never have a 403 reinterpreted,
// even if an ExistsFunc is available.
func TestUnitIsGone_NotFoundModeNeverReinterpretsForbidden(t *testing.T) {
	gone, err := IsGone(context.Background(), forbidden(), GoneByNotFound, exists(false))
	if gone {
		t.Error("under GoneByNotFound a 403 is a permissions failure, never deletion")
	}
	if !IsForbidden(err) {
		t.Errorf("the original 403 should be returned unchanged, got %v", err)
	}
}

func TestUnitIsGone_MissingExistsFuncIsAnExplicitError(t *testing.T) {
	gone, err := IsGone(context.Background(), forbidden(), GoneByForbidden, nil)
	if gone {
		t.Fatal("must not guess without an existence check")
	}
	if err == nil || !strings.Contains(err.Error(), "no existence check") {
		t.Errorf("error %v should name the missing existence check", err)
	}
}

func TestUnitIsGone_UnrelatedFailuresPassThrough(t *testing.T) {
	serverErr := decodeError(http.StatusInternalServerError, "GET", "/servergroups/1", nil)

	gone, err := IsGone(context.Background(), serverErr, GoneByForbidden, exists(false))
	if gone {
		t.Error("a 500 is not deletion")
	}
	if err == nil {
		t.Error("a 500 must surface")
	}

	if gone, _ := IsGone(context.Background(), nil, GoneByForbidden, exists(false)); gone {
		t.Error("a nil error is not deletion")
	}
}
