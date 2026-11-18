package policy_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
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

// TestAccPolicy_PatchLifecycle covers create, read, update, import, and destroy
// for a patch policy.
//
// The safety envelope is in the configuration below, not in a comment: the
// policy targets a group this test creates and which therefore contains no
// devices, and it is scheduled with schedule_days = 0 so it can never run.
// auto_patch and auto_reboot are false as well. Any one of those alone prevents
// the policy acting on an endpoint.
func TestAccPolicy_PatchLifecycle(t *testing.T) {
	acceptance.SkipUnlessAcceptance(t)
	acceptance.PreCheck(t)
	acceptance.RequireWriteScope(t)

	parent := defaultGroupID(t)
	groupName := acceptance.Name("policy-target")
	name := acceptance.Name("patch")
	renamed := name + "-renamed"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: protoV6(),
		CheckDestroy:             checkDestroy,
		Steps: []resource.TestStep{
			{
				Config: configPatch(groupName, parent, name, `"include"`, `["Google Chrome"]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("automox_policy.test", "name", name),
					resource.TestCheckResourceAttr("automox_policy.test", "policy_type", "patch"),
					resource.TestCheckResourceAttrSet("automox_policy.test", "id"),
					resource.TestCheckResourceAttrSet("automox_policy.test", "uuid"),
					// The safety envelope, asserted rather than assumed.
					resource.TestCheckResourceAttr("automox_policy.test", "schedule_days", "0"),
					resource.TestCheckResourceAttr("automox_policy.test", "configuration.auto_patch", "false"),
					resource.TestCheckResourceAttr("automox_policy.test", "configuration.auto_reboot", "false"),
					resource.TestCheckResourceAttr("automox_policy.test", "server_count", "0"),
					resource.TestCheckResourceAttr("automox_policy.test", "configuration.patch_rule", "filter"),
					resource.TestCheckResourceAttr("automox_policy.test", "configuration.filters.0", "Google Chrome"),
					resource.TestCheckResourceAttr("automox_policy.test", "configuration.filter_type", "include"),
				),
			},
			{
				ResourceName:      "automox_policy.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: importIDOf("automox_policy.test"),
				// server_groups ordering and the named schedule mirror are
				// configuration-shaped; an import has no configuration to mirror.
				ImportStateVerifyIgnore: []string{"schedule_days_of_week", "schedule_months_of_year"},
			},
			{
				// Change only the schedule. API-populated metadata and configuration
				// must remain concrete in the plan instead of all becoming known after
				// apply; the offline modifier tests assert that planning behavior.
				Config: configPatchAtTime(groupName, parent, name, `"include"`, `["Google Chrome"]`, "01:00"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("automox_policy.test", "schedule_time", "01:00"),
					resource.TestCheckResourceAttrSet("automox_policy.test", "create_time"),
					resource.TestCheckResourceAttr("automox_policy.test", "server_count", "0"),
					resource.TestCheckResourceAttrSet("automox_policy.test", "configuration.patch_rule"),
				),
			},
			{
				Config:   configPatchAtTime(groupName, parent, name, `"include"`, `["Google Chrome"]`, "01:00"),
				PlanOnly: true,
			},
			{
				Config: configPatch(groupName, parent, renamed, `"include"`, `["Firefox", "Microsoft Edge"]`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("automox_policy.test", "name", renamed),
					resource.TestCheckResourceAttr("automox_policy.test", "configuration.filter_type", "include"),
					resource.TestCheckResourceAttr("automox_policy.test", "configuration.filters.#", "2"),
					// Still cannot run after an update.
					resource.TestCheckResourceAttr("automox_policy.test", "schedule_days", "0"),
				),
			},
			{
				Config:   configPatch(groupName, parent, renamed, `"include"`, `["Firefox", "Microsoft Edge"]`),
				PlanOnly: true,
			},
		},
	})
}

// TestAccPolicy_NamedScheduleRoundTrip proves the named schedule form encodes to
// the integer Automox actually stores.
//
// This is the highest-consequence assertion in the suite. The bit order was
// derived by inference, and an error would silently move when patching runs
// across production endpoints. The policy still cannot execute: it targets an
// empty group and has auto_patch and auto_reboot off.
func TestAccPolicy_NamedScheduleRoundTrip(t *testing.T) {
	acceptance.SkipUnlessAcceptance(t)
	acceptance.PreCheck(t)
	acceptance.RequireWriteScope(t)

	parent := defaultGroupID(t)
	groupName := acceptance.Name("sched-target")
	name := acceptance.Name("sched")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: protoV6(),
		CheckDestroy:             checkDestroy,
		Steps: []resource.TestStep{
			{
				Config: configNamedSchedule(groupName, parent, name),
				Check: resource.ComposeAggregateTestCheckFunc(
					// monday|thursday -> bits 1 and 4 -> 2 + 16 = 18.
					resource.TestCheckResourceAttr("automox_policy.test", "schedule_days", "18"),
					// march|september -> bits 3 and 9 -> 8 + 512 = 520.
					resource.TestCheckResourceAttr("automox_policy.test", "schedule_months", "520"),
					resource.TestCheckResourceAttr("automox_policy.test", "schedule_days_of_week.#", "2"),
				),
			},
			{
				Config:   configNamedSchedule(groupName, parent, name),
				PlanOnly: true,
			},
		},
	})
}

// TestAccPolicy_StatusFollowsGroupsAndAutoPatch is the live counterpart of
// TestUnitStatusUnknownOnlyWhenItsDriversChange: it takes a policy through the
// shape that failed on sweetgreen/terraform-infrastructure#2149 -- no groups and
// auto_patch off, then a group attached and auto_patch on in one apply -- and
// requires the apply to succeed with the status Automox reports.
//
// Enabling auto_patch here is safe only because of the rest of the envelope:
// the one group is created by this test and so holds no devices, and
// schedule_days = 0 means the policy never runs. auto_reboot stays off.
func TestAccPolicy_StatusFollowsGroupsAndAutoPatch(t *testing.T) {
	acceptance.SkipUnlessAcceptance(t)
	acceptance.PreCheck(t)
	acceptance.RequireWriteScope(t)

	parent := defaultGroupID(t)
	groupName := acceptance.Name("status-target")
	name := acceptance.Name("status")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: protoV6(),
		CheckDestroy:             checkDestroy,
		Steps: []resource.TestStep{
			{
				Config: configPatchStatus(groupName, parent, name, false),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("automox_policy.test", "server_groups.#", "0"),
					resource.TestCheckResourceAttr("automox_policy.test", "configuration.auto_patch", "false"),
					resource.TestCheckResourceAttr("automox_policy.test", "status", "inactive"),
					resource.TestCheckResourceAttr("automox_policy.test", "schedule_days", "0"),
				),
			},
			{
				// The #2149 change. Before 0.1.9 this step failed with
				// ".status: was cty.StringVal("inactive"), but now cty.StringVal("active")".
				Config: configPatchStatus(groupName, parent, name, true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("automox_policy.test", "server_groups.#", "1"),
					resource.TestCheckResourceAttr("automox_policy.test", "configuration.auto_patch", "true"),
					resource.TestCheckResourceAttr("automox_policy.test", "status", "active"),
					resource.TestCheckResourceAttr("automox_policy.test", "schedule_days", "0"),
					resource.TestCheckResourceAttr("automox_policy.test", "server_count", "0"),
				),
			},
			{
				Config:   configPatchStatus(groupName, parent, name, true),
				PlanOnly: true,
			},
			{
				// And back: detaching every group and turning auto_patch off must
				// not fail the other way.
				Config: configPatchStatus(groupName, parent, name, false),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("automox_policy.test", "server_groups.#", "0"),
					resource.TestCheckResourceAttr("automox_policy.test", "status", "inactive"),
				),
			},
		},
	})
}

// TestAccPolicy_AdvancedFilterAppend is the live regression for PLA-464
// (sweetgreen/terraform-infrastructure#2275). Appending an advanced_filter entry
// to an existing policy applied in Automox but failed with
// ".configuration.advanced_filter[2].op: was null, but now
// cty.StringVal("does-not-contain")". Changing the condition of an entry that
// already had op in state failed the same way. The offline equivalent, which
// runs in CI, is TestUnitAppendingAdvancedFilterIsConsistentAfterApply.
//
// The policy cannot run: it targets an empty group this test creates, has no
// scheduled day, and auto_patch and auto_reboot are off.
func TestAccPolicy_AdvancedFilterAppend(t *testing.T) {
	acceptance.SkipUnlessAcceptance(t)
	acceptance.PreCheck(t)
	acceptance.RequireWriteScope(t)

	parent := defaultGroupID(t)
	groupName := acceptance.Name("advanced-target")
	name := acceptance.Name("advanced")

	two := `
      { left = "patch-source", condition = "is", right = "windowsupdate" },
      { left = "display-name", condition = "does-not-contain", right = "Preview" },`
	three := two + `
      { left = "display-name", condition = "does-not-contain", op = null, right = "SQL Server" },`
	changed := strings.Replace(three, `condition = "is",`, `condition = "is-not",`, 1)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: protoV6(),
		CheckDestroy:             checkDestroy,
		Steps: []resource.TestStep{
			{
				Config: configPatchAdvanced(groupName, parent, name, two),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("automox_policy.test", "configuration.advanced_filter.#", "2"),
					resource.TestCheckResourceAttr("automox_policy.test", "schedule_days", "0"),
				),
			},
			{
				Config: configPatchAdvanced(groupName, parent, name, three),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("automox_policy.test", "configuration.advanced_filter.#", "3"),
					resource.TestCheckResourceAttr("automox_policy.test",
						"configuration.advanced_filter.2.condition", "does-not-contain"),
					resource.TestCheckNoResourceAttr("automox_policy.test", "configuration.advanced_filter.2.op"),
				),
			},
			{
				Config:   configPatchAdvanced(groupName, parent, name, three),
				PlanOnly: true,
			},
			{
				Config: configPatchAdvanced(groupName, parent, name, changed),
				Check: resource.TestCheckResourceAttr("automox_policy.test",
					"configuration.advanced_filter.0.condition", "is-not"),
			},
			{
				Config:   configPatchAdvanced(groupName, parent, name, changed),
				PlanOnly: true,
			},
		},
	})
}

// configPatchAdvanced is a patch policy that never runs, selecting patches with
// the given advanced_filter entries.
func configPatchAdvanced(groupName string, parent int64, name, filters string) string {
	return fmt.Sprintf(`
resource "automox_server_group" "target" {
  name                   = %q
  refresh_interval       = 1440
  parent_server_group_id = %d
}

resource "automox_policy" "test" {
  name          = %q
  policy_type   = "patch"
  notes         = "advanced filters: created by the provider acceptance suite"
  server_groups = [automox_server_group.target.id]

  # Never runs: no scheduled day, and nothing automatic.
  schedule_time = "00:00"
  schedule_days = 0

  configuration = {
    patch_rule           = "advanced"
    filter_type          = "all"
    advanced_filter      = [%s
    ]
    auto_patch           = false
    auto_reboot          = false
    notify_user          = false
    is_patch_tuesday     = false
    patch_tuesday_offset = 0
  }
}
`, groupName, parent, name, filters)
}

// configPatchStatus is a patch policy that never runs. With attached=false it
// targets no groups and has auto_patch off; with attached=true it targets the
// test-created (empty) group and has auto_patch on. The group exists in both so
// the only diff between them is the one under test.
func configPatchStatus(groupName string, parent int64, name string, attached bool) string {
	groups := "[]"
	if attached {
		groups = "[automox_server_group.target.id]"
	}
	return fmt.Sprintf(`
resource "automox_server_group" "target" {
  name                   = %q
  refresh_interval       = 1440
  parent_server_group_id = %d
}

resource "automox_policy" "test" {
  name          = %q
  policy_type   = "patch"
  notes         = "status flip: created by the provider acceptance suite"
  server_groups = %s

  # Never runs: no scheduled day. auto_patch is the variable under test.
  schedule_time = "00:00"
  schedule_days = 0

  configuration = {
    patch_rule           = "filter"
    filter_type          = "include"
    filters              = ["Google Chrome"]
    auto_patch           = %t
    auto_reboot          = false
    notify_user          = false
    is_patch_tuesday     = false
    patch_tuesday_offset = 0
  }
}
`, groupName, parent, name, groups, attached)
}

func configPatch(groupName string, parent int64, name, filterType, filters string) string {
	return fmt.Sprintf(`
resource "automox_server_group" "target" {
  name                   = %q
  refresh_interval       = 1440
  parent_server_group_id = %d
}

resource "automox_policy" "test" {
  name          = %q
  policy_type   = "patch"
  notes         = "created by the provider acceptance suite"
  server_groups = [automox_server_group.target.id]

  # Never runs: no scheduled day, and nothing automatic.
  schedule_time = "00:00"
  schedule_days = 0

  configuration = {
    patch_rule           = "filter"
    filter_type          = %s
    filters              = %s
    auto_patch           = false
    auto_reboot          = false
    notify_user          = false
    is_patch_tuesday     = false
    patch_tuesday_offset = 0
  }
}
`, groupName, parent, name, filterType, filters)
}

func configPatchAtTime(groupName string, parent int64, name, filterType, filters, scheduleTime string) string {
	return strings.Replace(
		configPatch(groupName, parent, name, filterType, filters),
		`schedule_time = "00:00"`,
		fmt.Sprintf(`schedule_time = %q`, scheduleTime),
		1,
	)
}

func configNamedSchedule(groupName string, parent int64, name string) string {
	return fmt.Sprintf(`
resource "automox_server_group" "target" {
  name                   = %q
  refresh_interval       = 1440
  parent_server_group_id = %d
}

resource "automox_policy" "test" {
  name          = %q
  policy_type   = "patch"
  notes         = "schedule encoding round-trip"
  server_groups = [automox_server_group.target.id]

  schedule_time           = "03:00"
  schedule_days_of_week   = ["monday", "thursday"]
  schedule_months_of_year = ["march", "september"]

  configuration = {
    patch_rule           = "filter"
    filter_type          = "include"
    filters              = []
    auto_patch           = false
    auto_reboot          = false
    notify_user          = false
    is_patch_tuesday     = false
    patch_tuesday_offset = 0
  }
}
`, groupName, parent, name)
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

// checkDestroy asserts both the policies and their target groups are gone. A
// leaked policy in a live organization is a test failure.
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

	// Assert only the objects this test's state owns. A sweep for every
	// TESTING-prefixed object would fail whenever another test package is running
	// concurrently, which is a property of the test runner rather than of this
	// resource. The organization-wide audit is a separate serial step.
	for _, rs := range s.RootModule().Resources {
		id, err := strconv.ParseInt(rs.Primary.Attributes["id"], 10, 64)
		if err != nil {
			continue
		}

		switch rs.Type {
		case "automox_policy":
			if err := assertGone(ctx, c, "/policies", client.GoneByNotFound, id, "policy"); err != nil {
				return err
			}
		case "automox_server_group":
			if err := assertGone(ctx, c, "/servergroups", client.GoneByForbidden, id, "server group"); err != nil {
				return err
			}
		}
	}
	return nil
}

// assertGone confirms one object is absent from its collection listing. The
// listing is used rather than a direct read because Automox reports an absent
// server group as 403, which a read cannot distinguish from lost access.
func assertGone(ctx context.Context, c *client.Client, path string, _ client.GoneMode, id int64, kind string) error {
	var items []struct {
		ID   int64  `json:"id"`
		Name string `json:"name"`
	}
	if err := c.List(ctx, client.ListOptions{
		Path: path, OrgScope: client.OrgScopeQuery, Envelope: client.EnvelopeArray,
	}, &items); err != nil {
		return fmt.Errorf("listing %s to confirm destruction: %w", path, err)
	}
	for _, it := range items {
		if it.ID == id {
			return fmt.Errorf("%s %d (%q) still exists after destroy; remove it manually", kind, id, it.Name)
		}
	}
	return nil
}
