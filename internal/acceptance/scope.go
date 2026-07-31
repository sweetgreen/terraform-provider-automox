// Package acceptance holds the guards that acceptance tests run behind.
//
// Two problems it solves.
//
// First, credential scope. An Automox key's permissions are fixed when it is
// issued, and the provider is developed against keys of varying scope. A
// write-scoped test running under a read-only key must say so out loud. Skipping
// with a reason is honest; passing without exercising anything is the failure
// mode this package exists to prevent.
//
// Second, blast radius. Automox has no sandbox tenant, so acceptance tests run
// against the production organization. Every object they create is prefixed and
// neutered here rather than in each test, so a new test inherits the safety
// envelope instead of having to remember it.
package acceptance

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/sweetgreen/terraform-provider-automox/internal/client"
)

// Environment variables acceptance tests read.
const (
	EnvAPIKey         = "AUTOMOX_API_KEY"
	EnvOrganizationID = "AUTOMOX_ORGANIZATION_ID"
	EnvTFAcc          = "TF_ACC"

	// EnvAllowProductionOrg must be set to acknowledge that tests will create
	// objects in a live Automox organization. Deliberately not defaulted: a
	// contributor who has not read the safety envelope should hit a stop sign,
	// not a fleet.
	EnvAllowProductionOrg = "AUTOMOX_ACC_ALLOW_PRODUCTION_ORG"
)

// TestingPrefix marks every object acceptance tests create, so a leaked one is
// identifiable at a glance in the Automox console.
const TestingPrefix = "TESTING"

// SkipUnlessAcceptance skips the calling test unless acceptance testing is
// enabled. It must be the first line of any TestAcc function.
//
// resource.Test applies this gate itself, but only once it is running. Anything
// a test does *before* that — reading environment, looking up a parent group,
// asserting configuration — would otherwise run during a plain `go test ./...`
// and fail for a contributor who has no Automox credentials. An offline unit
// suite that cannot pass without cloud access is not an offline suite.
func SkipUnlessAcceptance(t *testing.T) {
	t.Helper()
	if os.Getenv(EnvTFAcc) == "" {
		t.Skipf("skipping acceptance test: set %s=1 to run tests against a live Automox organization", EnvTFAcc)
	}
}

// PreCheck asserts the configuration every acceptance test needs. Call it from
// TestCase.PreCheck, after SkipUnlessAcceptance has gated the test.
func PreCheck(t *testing.T) {
	t.Helper()

	if os.Getenv(EnvAPIKey) == "" {
		t.Fatalf("%s must be set for acceptance tests", EnvAPIKey)
	}
	if os.Getenv(EnvOrganizationID) == "" {
		t.Fatalf("%s must be set for acceptance tests", EnvOrganizationID)
	}
	if os.Getenv(EnvAllowProductionOrg) == "" {
		t.Fatalf(
			"%s is not set.\n\n"+
				"Acceptance tests create real objects in a live Automox organization; there is no\n"+
				"sandbox tenant. Objects are prefixed %q, attach only to an empty server group, and\n"+
				"are created with no schedule so they cannot execute on a device — but they are real.\n\n"+
				"Set %s=1 to confirm you intend that.",
			EnvAllowProductionOrg, TestingPrefix, EnvAllowProductionOrg)
	}
}

var (
	writeScopeOnce   sync.Once
	writeScopeResult error
)

// RequireWriteScope skips the calling test when the configured credential cannot
// write, stating why.
//
// The probe is a real create-then-delete of an empty server group, because
// Automox reports scope only by refusing an operation — there is no endpoint that
// describes a key's permissions. An empty group is the cheapest object that
// cannot affect a device: it holds no endpoints, runs nothing, and is removed
// immediately.
//
// The result is cached, so a package of write-scoped tests costs one probe.
func RequireWriteScope(t *testing.T) {
	t.Helper()

	writeScopeOnce.Do(func() {
		writeScopeResult = probeWriteScope()
	})

	if writeScopeResult == nil {
		return
	}

	// Only a refused write means a read-only credential. Anything else -- a 503,
	// a timeout, a probe broken by a refactor -- is an unexplained failure, and
	// skipping on it would turn every write test into a silent no-op while the
	// suite reported green. That is the same false-confidence FR-25 exists to
	// prevent, arrived at from the other direction.
	if IsReadOnlyCredential(writeScopeResult) {
		t.Skipf(
			"skipping: the configured Automox credential cannot write.\n"+
				"  reason: %v\n"+
				"  This is a skip, not a pass — nothing was verified. An Automox API key's scope is\n"+
				"  fixed when it is issued, so a key upgrade means issuing a new key, not editing\n"+
				"  an existing one.",
			writeScopeResult)
	}

	t.Fatalf(
		"the write-scope probe failed for a reason other than permissions, so whether this\n"+
			"  credential can write is unknown.\n"+
			"  reason: %v\n"+
			"  This is deliberately a failure rather than a skip: skipping here would quietly\n"+
			"  turn every write test into a no-op while the suite reported success.",
		writeScopeResult)
}

// IsReadOnlyCredential reports whether the probe failed because Automox refused
// the write, as opposed to failing for any other reason.
//
// Automox validates a request body before checking authorization, so a probe
// that sent an incomplete body would be answered 400 and never reach the
// permission check. The probe therefore sends a complete, valid server group;
// a 403 from it is genuine evidence about scope, and nothing else is.
func IsReadOnlyCredential(err error) bool {
	if err == nil {
		return false
	}
	var apiErr *client.APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	return apiErr.StatusCode == http.StatusForbidden ||
		apiErr.StatusCode == http.StatusUnauthorized
}

func probeWriteScope() error {
	orgID, err := strconv.ParseInt(os.Getenv(EnvOrganizationID), 10, 64)
	if err != nil {
		return fmt.Errorf("%s is not a valid integer: %w", EnvOrganizationID, err)
	}

	c, err := client.New(client.Options{
		APIKey:         os.Getenv(EnvAPIKey),
		OrganizationID: orgID,
	})
	if err != nil {
		return fmt.Errorf("configuring the probe client: %w", err)
	}

	ctx := context.Background()

	// The probe needs a parent, and the organization's default group is the only
	// one guaranteed to exist. It is identified by being its own parent.
	var groups []struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		ParentID int64  `json:"parent_server_group_id"`
	}
	if err := c.List(ctx, client.ListOptions{
		Path: "/servergroups", OrgScope: client.OrgScopeQuery, Envelope: client.EnvelopeArray,
	}, &groups); err != nil {
		return fmt.Errorf("listing server groups to locate the default group: %w", err)
	}

	var defaultGroupID int64
	for _, g := range groups {
		if g.ID == g.ParentID {
			defaultGroupID = g.ID
			break
		}
	}
	if defaultGroupID == 0 {
		return fmt.Errorf("could not identify the organization's default server group among %d groups", len(groups))
	}

	var created struct {
		ID int64 `json:"id"`
	}
	err = c.Do(ctx, client.Request{
		Method:   "POST",
		Path:     "/servergroups",
		OrgScope: client.OrgScopeQuery,
		Body: map[string]any{
			"name":                   Name("scope-probe"),
			"refresh_interval":       1440,
			"parent_server_group_id": defaultGroupID,
			"notes":                  "Transient write-scope probe created by the Terraform provider acceptance suite.",
		},
	}, &created)
	if err != nil {
		return err
	}

	if created.ID == 0 {
		return fmt.Errorf("the probe group was created but returned no id, so it cannot be cleaned up; " +
			"check for a leftover " + TestingPrefix + " group")
	}

	// Best effort cleanup. A failure here is worth reporting loudly even though
	// the probe itself succeeded, because it means a stray object was left in a
	// production organization.
	if delErr := c.Do(ctx, client.Request{
		Method:   "DELETE",
		Path:     fmt.Sprintf("/servergroups/%d", created.ID),
		OrgScope: client.OrgScopeQuery,
	}, nil); delErr != nil {
		return fmt.Errorf(
			"write scope confirmed, but the probe group %d could not be deleted and is still "+
				"present in the organization: %w", created.ID, delErr)
	}

	return nil
}

// Name builds a prefixed, unique name for an object a test creates.
//
// Uniqueness matters beyond tidiness: policy names must be unique within an
// organization, because the provider recovers a created policy's id by matching
// on name. Parallel or re-run tests colliding on a name would make that lookup
// ambiguous, which the provider treats as a hard error.
func Name(kind string) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	suffix := make([]byte, 8)
	for i := range suffix {
		suffix[i] = alphabet[rand.Intn(len(alphabet))]
	}
	return fmt.Sprintf("%s-%s-%s", TestingPrefix, kind, string(suffix))
}

// IsTestingObject reports whether a name was created by this suite. Used by
// cleanup and by the post-run audit that asserts nothing was left behind.
func IsTestingObject(name string) bool {
	return strings.HasPrefix(name, TestingPrefix+"-")
}
