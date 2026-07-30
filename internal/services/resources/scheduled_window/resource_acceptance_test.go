package scheduled_window_test

import (
	"context"
	"fmt"
	"os"
	"regexp"
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

func testClient(t *testing.T) *client.Client {
	t.Helper()
	orgID, err := strconv.ParseInt(os.Getenv(acceptance.EnvOrganizationID), 10, 64)
	if err != nil {
		t.Fatalf("%s: %v", acceptance.EnvOrganizationID, err)
	}
	c, err := client.New(client.Options{APIKey: os.Getenv(acceptance.EnvAPIKey), OrganizationID: orgID})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func defaultGroupID(t *testing.T) int64 {
	t.Helper()
	var groups []struct {
		ID       int64 `json:"id"`
		ParentID int64 `json:"parent_server_group_id"`
	}
	if err := testClient(t).List(context.Background(), client.ListOptions{
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

// TestAccScheduledWindow_OnceLifecycle covers a single-occurrence window.
//
// It also exercises the reason automox_server_group exposes an undocumented
// uuid: this endpoint identifies groups by UUID while every group endpoint
// returns an integer id, so without that attribute the two resources could not
// be wired together in configuration at all.
//
// The window is an exclusion, so its effect is to *prevent* patching. It covers
// only a group this test creates, which contains no devices.
func TestAccScheduledWindow_OnceLifecycle(t *testing.T) {
	acceptance.SkipUnlessAcceptance(t)
	acceptance.PreCheck(t)
	acceptance.RequireWriteScope(t)

	parent := defaultGroupID(t)
	groupName := acceptance.Name("window-target")
	name := acceptance.Name("window-once")
	renamed := name + "-renamed"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: protoV6(),
		CheckDestroy:             checkDestroy,
		Steps: []resource.TestStep{
			{
				Config: configOnce(groupName, parent, name, "20261201T020000Z"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("automox_scheduled_window.test", "window_name", name),
					resource.TestCheckResourceAttrSet("automox_scheduled_window.test", "window_uuid"),
					// exclude is the only accepted value; the vendor's own example
					// value "exclusion" is rejected by the API.
					resource.TestCheckResourceAttr("automox_scheduled_window.test", "window_type", "exclude"),
					resource.TestCheckResourceAttr("automox_scheduled_window.test", "recurrence", "ONCE"),
					resource.TestCheckResourceAttr("automox_scheduled_window.test", "status", "active"),
					// Automox derives the duration for a ONCE window and stores none.
					resource.TestCheckNoResourceAttr("automox_scheduled_window.test", "duration_minutes"),
					resource.TestCheckResourceAttr("automox_scheduled_window.test", "group_uuids.#", "1"),
					resource.TestCheckResourceAttrSet("automox_scheduled_window.test", "org_uuid"),
					// The group UUID in the window must be the group's own uuid.
					resource.TestCheckResourceAttrPair(
						"automox_scheduled_window.test", "group_uuids.0",
						"automox_server_group.target", "uuid"),
				),
			},
			{
				ResourceName:      "automox_scheduled_window.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: importIDOf("automox_scheduled_window.test"),
				// This resource is identified by window_uuid. Automox issues no
				// integer id for a window, so there is no "id" attribute for the
				// harness to assume.
				ImportStateVerifyIdentifierAttribute: "window_uuid",
			},
			{
				Config: configOnceNamed(groupName, parent, renamed, "20261201T040000Z"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("automox_scheduled_window.test", "window_name", renamed),
					resource.TestCheckResourceAttr("automox_scheduled_window.test", "rrule",
						"FREQ=DAILY;UNTIL=20261201T040000Z"),
				),
			},
			{
				Config:   configOnceNamed(groupName, parent, renamed, "20261201T040000Z"),
				PlanOnly: true,
			},
		},
	})
}

// TestAccScheduledWindow_Recurring covers the annual form, where duration_minutes
// is permitted and the rrule grammar is different.
func TestAccScheduledWindow_Recurring(t *testing.T) {
	acceptance.SkipUnlessAcceptance(t)
	acceptance.PreCheck(t)
	acceptance.RequireWriteScope(t)

	parent := defaultGroupID(t)
	groupName := acceptance.Name("recur-target")
	name := acceptance.Name("window-recur")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: protoV6(),
		CheckDestroy:             checkDestroy,
		Steps: []resource.TestStep{
			{
				Config: configRecurring(groupName, parent, name),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("automox_scheduled_window.test", "recurrence", "RECURRING"),
					// Accepted here, unlike on a ONCE window.
					resource.TestCheckResourceAttr("automox_scheduled_window.test", "duration_minutes", "120"),
					resource.TestCheckResourceAttr("automox_scheduled_window.test", "rrule",
						"FREQ=YEARLY;BYMONTH=1;BYDAY=+1MO"),
				),
			},
			{
				Config:   configRecurring(groupName, parent, name),
				PlanOnly: true,
			},
		},
	})
}

// TestAccScheduledWindow_RejectsDurationOnOnce proves the plan-time validator
// catches what would otherwise be an opaque 400 naming a camelCase field the
// practitioner never wrote.
func TestAccScheduledWindow_RejectsDurationOnOnce(t *testing.T) {
	acceptance.SkipUnlessAcceptance(t)
	acceptance.PreCheck(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: protoV6(),
		Steps: []resource.TestStep{
			{
				Config: `
resource "automox_scheduled_window" "invalid" {
  window_name        = "TESTING-never-created"
  window_description = "should fail during plan"
  recurrence         = "ONCE"
  dtstart            = "2026-12-01T00:00:00Z"
  rrule              = "FREQ=DAILY;UNTIL=20261201T020000Z"
  duration_minutes   = 60
  group_uuids        = []
}
`,
				ExpectError: regexp.MustCompile(`duration_minutes cannot be set on a ONCE window`),
			},
			{
				Config: `
resource "automox_scheduled_window" "invalid" {
  window_name        = "TESTING-never-created"
  window_description = "should fail during plan"
  recurrence         = "ONCE"
  dtstart            = "2026-12-01T00:00:00Z"
  rrule              = "FREQ=WEEKLY;COUNT=5"
  group_uuids        = []
}
`,
				ExpectError: regexp.MustCompile(`Unsupported rrule for a ONCE window`),
			},
		},
	})
}

func configOnce(groupName string, parent int64, name, until string) string {
	return fmt.Sprintf(`
resource "automox_server_group" "target" {
  name                   = %q
  refresh_interval       = 1440
  parent_server_group_id = %d
}

resource "automox_scheduled_window" "test" {
  window_name        = %q
  window_description = "created by the provider acceptance suite"
  recurrence         = "ONCE"
  dtstart            = "2026-12-01T00:00:00Z"
  rrule              = "FREQ=DAILY;UNTIL=%s"

  # Identified by UUID, not by the integer id every group endpoint returns.
  group_uuids = [automox_server_group.target.uuid]
}
`, groupName, parent, name, until)
}

func configOnceNamed(groupName string, parent int64, name, until string) string {
	return configOnce(groupName, parent, name, until)
}

func configRecurring(groupName string, parent int64, name string) string {
	return fmt.Sprintf(`
resource "automox_server_group" "target" {
  name                   = %q
  refresh_interval       = 1440
  parent_server_group_id = %d
}

resource "automox_scheduled_window" "test" {
  window_name        = %q
  window_description = "annual freeze, created by the acceptance suite"
  recurrence         = "RECURRING"
  dtstart            = "2026-12-01T00:00:00Z"
  rrule              = "FREQ=YEARLY;BYMONTH=1;BYDAY=+1MO"
  duration_minutes   = 120

  group_uuids = [automox_server_group.target.uuid]
}
`, groupName, parent, name)
}

func importIDOf(resourceName string) resource.ImportStateIdFunc {
	return func(s *terraform.State) (string, error) {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return "", fmt.Errorf("%s not found in state", resourceName)
		}
		return rs.Primary.Attributes["window_uuid"], nil
	}
}

// checkDestroy asserts only what this test's state owns. A sweep for every
// TESTING-prefixed object would fail whenever another package runs concurrently.
func checkDestroy(s *terraform.State) error {
	orgID, err := strconv.ParseInt(os.Getenv(acceptance.EnvOrganizationID), 10, 64)
	if err != nil {
		return err
	}
	c, err := client.New(client.Options{APIKey: os.Getenv(acceptance.EnvAPIKey), OrganizationID: orgID})
	if err != nil {
		return err
	}
	ctx := context.Background()

	orgUUID, err := c.DefaultOrganizationUUID(ctx)
	if err != nil {
		return fmt.Errorf("resolving the organization UUID: %w", err)
	}

	for _, rs := range s.RootModule().Resources {
		switch rs.Type {
		case "automox_scheduled_window":
			uuid := rs.Primary.Attributes["window_uuid"]
			if uuid == "" {
				continue
			}
			err := c.Do(ctx, client.Request{
				Method:   "GET",
				Path:     fmt.Sprintf("/policy-windows/org/%s/window/%s", orgUUID, uuid),
				OrgScope: client.OrgScopeNone,
			}, &map[string]any{})
			if err == nil {
				return fmt.Errorf("scheduled window %s still exists after destroy", uuid)
			}
			if !client.IsNotFound(err) {
				return fmt.Errorf("could not confirm window %s was destroyed: %w", uuid, err)
			}

		case "automox_server_group":
			id, convErr := strconv.ParseInt(rs.Primary.Attributes["id"], 10, 64)
			if convErr != nil {
				continue
			}
			var groups []struct {
				ID   int64  `json:"id"`
				Name string `json:"name"`
			}
			if err := c.List(ctx, client.ListOptions{
				Path: "/servergroups", OrgScope: client.OrgScopeQuery, Envelope: client.EnvelopeArray,
			}, &groups); err != nil {
				return fmt.Errorf("listing server groups to confirm destruction: %w", err)
			}
			for _, g := range groups {
				if g.ID == id {
					return fmt.Errorf("server group %d (%q) still exists after destroy", g.ID, g.Name)
				}
			}
		}
	}
	return nil
}
