package client

import (
	"context"
	"fmt"
)

// Automox is not consistent about how it reports an absent resource, so "is this
// gone?" cannot be answered from the HTTP status alone.
//
// Verified against the live API on 2026-07-30 by creating a server group,
// deleting it, and re-reading it:
//
//	GET /policies/999999999      -> 404 {"errors":["Not found"]}
//	GET /servergroups/999999999  -> 403 "You do not have permission..."
//	GET /servergroups/588692     -> 403   (id had just been deleted)
//	GET /servergroups/1          -> 403   (id never existed)
//
// So /policies distinguishes absence correctly while /servergroups reports every
// unreadable id as forbidden. That is defensible as a security posture — it avoids
// confirming which ids exist — but it means a 403 from those endpoints is
// ambiguous between "you deleted it" and "your credential lost access".
//
// Guessing either way is harmful. Treating every 403 as absence would let a
// credential regression silently look like deletion, and Terraform would propose
// recreating live objects. Treating every 403 as an error would make a normally
// deleted resource fail every subsequent plan instead of being recreated.
//
// The list endpoint resolves it: it is authorized separately, and it enumerates
// exactly the objects the credential can see. If the list succeeds and the id is
// absent, the resource is genuinely gone. If the list itself fails, the credential
// is the problem and the original error stands.

// ExistsFunc reports whether id is present in the collection the caller can list.
// It returns an error only when the listing itself failed, which is distinct from
// the object being absent.
type ExistsFunc func(ctx context.Context) (exists bool, err error)

// GoneMode describes how an endpoint signals that a resource is absent.
type GoneMode int

const (
	// GoneByNotFound is for endpoints that return a clean 404, such as /policies.
	GoneByNotFound GoneMode = iota

	// GoneByForbidden is for endpoints that return 403 for absent ids, such as
	// /servergroups. Requires an ExistsFunc to disambiguate.
	GoneByForbidden
)

// IsGone reports whether err means the resource no longer exists and should be
// removed from Terraform state.
//
// A true result must be trustworthy: callers act on it by dropping the object from
// state, which makes Terraform plan a replacement. When the answer cannot be
// established, IsGone returns the underlying error rather than a guess.
func IsGone(ctx context.Context, err error, mode GoneMode, exists ExistsFunc) (bool, error) {
	if err == nil {
		return false, nil
	}

	// A clean 404 is unambiguous regardless of mode.
	if IsNotFound(err) {
		return true, nil
	}

	if mode == GoneByNotFound || !IsForbidden(err) {
		// Either the endpoint reports absence properly and this was not a 404, or
		// the failure is not an authorization one. Not gone; surface it.
		return false, err
	}

	// GoneByForbidden and a 403: ambiguous, so ask the list endpoint.
	if exists == nil {
		return false, fmt.Errorf(
			"cannot distinguish deletion from lost access: %w "+
				"(this endpoint reports absent resources as 403 and no existence check was supplied)", err)
	}

	found, listErr := exists(ctx)
	if listErr != nil {
		// The credential cannot even list, so the 403 is a permissions problem.
		// Report both: the original failure, and the evidence that led here.
		return false, fmt.Errorf(
			"%w (could not confirm whether the resource still exists; listing also failed: %v)", err, listErr)
	}

	if found {
		// Listable but not individually readable. Genuinely odd — surface it
		// rather than deleting from state on a shape we do not understand.
		return false, fmt.Errorf(
			"%w (the resource is still present in the collection listing, so this is not a deletion)", err)
	}

	return true, nil
}
