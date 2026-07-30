package server_group_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/sweetgreen/terraform-provider-automox/internal/acceptance"
	"github.com/sweetgreen/terraform-provider-automox/internal/client"
	automox "github.com/sweetgreen/terraform-provider-automox/internal/provider"
)

func protoV6() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"automox": providerserver.NewProtocol6WithError(automox.New("acctest")()),
	}
}

// defaultGroupID finds the organization's default group, which every test group
// is parented to. It is identified by being its own parent.
func defaultGroupID(t *testing.T) int64 {
	t.Helper()

	orgID, err := strconv.ParseInt(os.Getenv(acceptance.EnvOrganizationID), 10, 64)
	if err != nil {
		t.Fatalf("%s: %v", acceptance.EnvOrganizationID, err)
	}
	c, err := client.New(client.Options{APIKey: os.Getenv(acceptance.EnvAPIKey), OrganizationID: orgID})
	if err != nil {
		t.Fatal(err)
	}

	var groups []struct {
		ID       int64 `json:"id"`
		ParentID int64 `json:"parent_server_group_id"`
	}
	if err := c.List(context.Background(), client.ListOptions{
		Path: "/servergroups", OrgScope: client.OrgScopeQuery, Envelope: client.EnvelopeArray,
	}, &groups); err != nil {
		t.Fatalf("listing server groups: %v", err)
	}
	for _, g := range groups {
		if g.ID == g.ParentID {
			return g.ID
		}
	}
	t.Fatal("could not identify the organization's default server group")
	return 0
}

// TestAccServerGroup_Lifecycle covers create, read, update, import, and destroy.
//
// Every group it creates is empty and named with the TESTING prefix, so nothing
// it does can reach a device: a group with no devices has nothing to patch.
func TestAccServerGroup_Lifecycle(t *testing.T) {
	// Must come first: everything below reaches the live API, and none of it may
	// run during a plain `go test ./...`.
	acceptance.SkipUnlessAcceptance(t)
	acceptance.PreCheck(t)
	acceptance.RequireWriteScope(t)

	parent := defaultGroupID(t)
	name := acceptance.Name("group")
	renamed := name + "-renamed"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: protoV6(),
		CheckDestroy:             checkDestroy,
		Steps: []resource.TestStep{
			{
				Config: configMinimal(name, parent),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("automox_server_group.test", "name", name),
					resource.TestCheckResourceAttr("automox_server_group.test", "refresh_interval", "1440"),
					resource.TestCheckResourceAttrSet("automox_server_group.test", "id"),
					// The undocumented uuid is what automox_scheduled_window needs;
					// it is absent from the create response and only appears on read,
					// so this also proves the read-back happens.
					resource.TestCheckResourceAttrSet("automox_server_group.test", "uuid"),
					resource.TestCheckResourceAttr("automox_server_group.test", "server_count", "0"),
					// Unset tri-state fields must stay null rather than being
					// materialised as false, which would misrepresent "leave each
					// device alone" as "disable it everywhere".
					resource.TestCheckNoResourceAttr("automox_server_group.test", "enable_os_auto_update"),
					resource.TestCheckNoResourceAttr("automox_server_group.test", "enable_wsus"),
					// Automox returns "" for an omitted note; that must not leak into
					// state as an empty string where the configuration said nothing.
					resource.TestCheckNoResourceAttr("automox_server_group.test", "notes"),
					// policies is Optional+Computed because a policy can attach itself
					// from the other side, so it is an empty list rather than null.
					resource.TestCheckResourceAttr("automox_server_group.test", "policies.#", "0"),
				),
			},
			{
				// Import round-trip: state built purely from the API must match
				// state built from the configuration.
				ResourceName:      "automox_server_group.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: importIDOf("automox_server_group.test"),
			},
			{
				Config: configFull(renamed, parent),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("automox_server_group.test", "name", renamed),
					resource.TestCheckResourceAttr("automox_server_group.test", "refresh_interval", "240"),
					resource.TestCheckResourceAttr("automox_server_group.test", "ui_color", "#059F1D"),
					resource.TestCheckResourceAttr("automox_server_group.test", "notes", "managed by acceptance tests"),
					resource.TestCheckResourceAttr("automox_server_group.test", "enable_os_auto_update", "false"),
					// The write/read asymmetry: these are sent as enable_wsus and
					// wsus_server, and come back inside wsus_config.
					resource.TestCheckResourceAttr("automox_server_group.test", "enable_wsus", "true"),
					resource.TestCheckResourceAttr("automox_server_group.test", "wsus_server", "https://wsus.example.com:8530"),
				),
			},
			{
				// Re-applying the same configuration must produce no diff. This is
				// what catches a field that round-trips differently than it was
				// written, which is the failure the wsus mapping risks.
				Config:   configFull(renamed, parent),
				PlanOnly: true,
			},
		},
	})
}

func configMinimal(name string, parent int64) string {
	return fmt.Sprintf(`
resource "automox_server_group" "test" {
  name                   = %q
  refresh_interval       = 1440
  parent_server_group_id = %d
}
`, name, parent)
}

func configFull(name string, parent int64) string {
	return fmt.Sprintf(`
resource "automox_server_group" "test" {
  name                   = %q
  refresh_interval       = 240
  parent_server_group_id = %d
  ui_color               = "#059F1D"
  notes                  = "managed by acceptance tests"
  enable_os_auto_update  = false
  enable_wsus            = true
  wsus_server            = "https://wsus.example.com:8530"
}
`, name, parent)
}

func importIDOf(resourceName string) resource.ImportStateIdFunc {
	return func(s *terraform.State) (string, error) {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return "", fmt.Errorf("%s not found in state", resourceName)
		}
		return rs.Primary.Attributes["id"], nil
	}
}

// checkDestroy asserts the group is really gone. A leaked object in a live
// organization is a test failure, not an inconvenience.
func checkDestroy(s *terraform.State) error {
	orgID, err := strconv.ParseInt(os.Getenv(acceptance.EnvOrganizationID), 10, 64)
	if err != nil {
		return err
	}
	c, err := client.New(client.Options{APIKey: os.Getenv(acceptance.EnvAPIKey), OrganizationID: orgID})
	if err != nil {
		return err
	}

	var groups []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := c.List(context.Background(), client.ListOptions{
		Path: "/servergroups", OrgScope: client.OrgScopeQuery, Envelope: client.EnvelopeArray,
	}, &groups); err != nil {
		return fmt.Errorf("listing server groups to confirm destruction: %w", err)
	}

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "automox_server_group" {
			continue
		}
		wantID, _ := strconv.ParseInt(rs.Primary.Attributes["id"], 10, 64)
		for _, g := range groups {
			if g.ID == wantID {
				return fmt.Errorf(
					"server group %d (%q) still exists after destroy; it must be removed manually",
					g.ID, g.Name)
			}
		}
	}

	// Deliberately no organization-wide sweep for TESTING-prefixed objects here.
	// Test packages run concurrently, so a sweep would observe objects another
	// package is still using and fail for a reason unrelated to this test. The
	// state-based check above is precise; the whole-organization audit belongs in
	// a serial post-run step, not in per-resource teardown.
	return nil
}
