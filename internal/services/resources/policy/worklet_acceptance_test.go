package policy_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/sweetgreen/terraform-provider-automox/internal/acceptance"
)

// TestAccPolicy_WorkletLifecycle covers the worklet type, which Automox calls
// "custom".
//
// A worklet runs arbitrary code on endpoints, so the safety envelope matters
// more here than anywhere else in the suite. Four separate things stop it: the
// policy targets a group this test creates and which therefore holds no devices,
// schedule_days is 0 so it is never scheduled, auto_reboot is false, and both
// scripts are `exit 0`. Any one of them alone is sufficient.
func TestAccPolicy_WorkletLifecycle(t *testing.T) {
	acceptance.SkipUnlessAcceptance(t)
	acceptance.PreCheck(t)
	acceptance.RequireWriteScope(t)

	parent := defaultGroupID(t)
	groupName := acceptance.Name("worklet-target")
	name := acceptance.Name("worklet")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: protoV6(),
		CheckDestroy:             checkDestroy,
		Steps: []resource.TestStep{
			{
				Config: configWorklet(groupName, parent, name, "worklet under test", "exit 0"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("automox_policy.test", "name", name),
					// The wire value is "custom"; there is no "worklet" type name.
					resource.TestCheckResourceAttr("automox_policy.test", "policy_type", "custom"),
					resource.TestCheckResourceAttrSet("automox_policy.test", "id"),
					resource.TestCheckResourceAttr("automox_policy.test", "configuration.os_family", "Windows"),
					resource.TestCheckResourceAttr("automox_policy.test", "configuration.evaluation_code", "exit 0"),
					// The safety envelope, asserted rather than assumed.
					resource.TestCheckResourceAttr("automox_policy.test", "schedule_days", "0"),
					resource.TestCheckResourceAttr("automox_policy.test", "configuration.auto_reboot", "false"),
					resource.TestCheckResourceAttr("automox_policy.test", "server_count", "0"),
				),
			},
			{
				// An update that does NOT rename the policy.
				//
				// Automox re-checks name uniqueness on update and only excludes the
				// policy itself when `id` is present in the request body. Drop that
				// field and this step fails with "A policy with this name already
				// exists", while every other update in the suite keeps passing because
				// each of them also renames. That makes this the only coverage of the
				// id-in-body requirement.
				Config: configWorklet(groupName, parent, name, "notes changed, name unchanged", "exit 0"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("automox_policy.test", "name", name),
					resource.TestCheckResourceAttr("automox_policy.test", "notes", "notes changed, name unchanged"),
				),
			},
			{
				ResourceName:            "automox_policy.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateIdFunc:       importIDOf("automox_policy.test"),
				ImportStateVerifyIgnore: []string{"schedule_days_of_week", "schedule_months_of_year"},
			},
			{
				Config:   configWorklet(groupName, parent, name, "notes changed, name unchanged", "exit 0"),
				PlanOnly: true,
			},
		},
	})
}

// TestAccPolicy_ScriptWhitespace covers scripts that end in whitespace, which is
// what file() produces from almost any editor.
//
// Automox trims both ends of a script when it saves it. Before v0.1.7 the
// provider recorded the trimmed value, so this create failed with "Provider
// produced inconsistent result after apply" and left the policy tainted. The
// configured value must now survive create, refresh and import, and a real
// change to the script must still plan as an update.
func TestAccPolicy_ScriptWhitespace(t *testing.T) {
	acceptance.SkipUnlessAcceptance(t)
	acceptance.PreCheck(t)
	acceptance.RequireWriteScope(t)

	parent := defaultGroupID(t)
	groupName := acceptance.Name("ws-target")
	name := acceptance.Name("worklet-ws")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: protoV6(),
		CheckDestroy:             checkDestroy,
		Steps: []resource.TestStep{
			{
				Config: configWorkletScripts(groupName, parent, name, "exit 0\n", "exit 0\r\n"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("automox_policy.test", "configuration.evaluation_code", "exit 0\n"),
					resource.TestCheckResourceAttr("automox_policy.test", "configuration.remediation_code", "exit 0\r\n"),
					resource.TestCheckResourceAttr("automox_policy.test", "schedule_days", "0"),
					resource.TestCheckResourceAttr("automox_policy.test", "server_count", "0"),
				),
			},
			{
				// Refresh reads back the trimmed scripts; the plan must still be empty.
				Config:   configWorkletScripts(groupName, parent, name, "exit 0\n", "exit 0\r\n"),
				PlanOnly: true,
			},
			{
				// Several trailing newlines, spaces and a byte-order mark: all trimmed
				// by Automox, so the update must apply cleanly too.
				Config: configWorkletScripts(groupName, parent, name, "\ufeffexit 1 \n\n", "exit 0\r\n"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("automox_policy.test", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.TestCheckResourceAttr("automox_policy.test",
					"configuration.evaluation_code", "\ufeffexit 1 \n\n"),
			},
			{
				// Import has no configured value to keep, so it records what Automox
				// stores: the trimmed scripts.
				ResourceName:      "automox_policy.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: importIDOf("automox_policy.test"),
				ImportStateVerifyIgnore: []string{
					"schedule_days_of_week", "schedule_months_of_year",
					"configuration.evaluation_code", "configuration.remediation_code",
				},
				ImportStateCheck: importedScripts(map[string]string{
					"configuration.evaluation_code":  "exit 1",
					"configuration.remediation_code": "exit 0",
				}),
			},
		},
	})
}

// TestAccPolicy_InstallationCodeWhitespace covers the third script attribute,
// which only required-software policies use.
func TestAccPolicy_InstallationCodeWhitespace(t *testing.T) {
	acceptance.SkipUnlessAcceptance(t)
	acceptance.PreCheck(t)
	acceptance.RequireWriteScope(t)

	parent := defaultGroupID(t)
	groupName := acceptance.Name("reqsw-ws-target")
	name := acceptance.Name("reqsw-ws")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: protoV6(),
		CheckDestroy:             checkDestroy,
		Steps: []resource.TestStep{
			{
				Config: configRequiredSoftwareCode(groupName, parent, name, "exit 0\r\n\n"),
				Check: resource.TestCheckResourceAttr("automox_policy.test",
					"configuration.installation_code", "exit 0\r\n\n"),
			},
			{
				Config:   configRequiredSoftwareCode(groupName, parent, name, "exit 0\r\n\n"),
				PlanOnly: true,
			},
			{
				ResourceName:      "automox_policy.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: importIDOf("automox_policy.test"),
				// evaluation_code is unrelated to this test. Create reads the new
				// policy from the list endpoint, which omits it on required-software
				// policies, while import's GET returns it as "".
				ImportStateVerifyIgnore: []string{"configuration.installation_code", "configuration.evaluation_code"},
				ImportStateCheck: importedScripts(map[string]string{
					"configuration.installation_code": "exit 0",
				}),
			},
		},
	})
}

// importedScripts asserts the imported policy holds the scripts as Automox
// stores them.
func importedScripts(want map[string]string) resource.ImportStateCheckFunc {
	return func(states []*terraform.InstanceState) error {
		if len(states) != 1 {
			return fmt.Errorf("imported %d resources, want 1", len(states))
		}
		for key, value := range want {
			if got := states[0].Attributes[key]; got != value {
				return fmt.Errorf("imported %s = %q, want %q", key, got, value)
			}
		}
		return nil
	}
}

// TestAccPolicy_RequiredSoftware covers the third policy type, whose required
// configuration fields differ from both other types.
func TestAccPolicy_RequiredSoftware(t *testing.T) {
	acceptance.SkipUnlessAcceptance(t)
	acceptance.PreCheck(t)
	acceptance.RequireWriteScope(t)

	parent := defaultGroupID(t)
	groupName := acceptance.Name("reqsw-target")
	name := acceptance.Name("reqsw")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: protoV6(),
		CheckDestroy:             checkDestroy,
		Steps: []resource.TestStep{
			{
				Config: configRequiredSoftware(groupName, parent, name, "1.0.0"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("automox_policy.test", "policy_type", "required_software"),
					resource.TestCheckResourceAttr("automox_policy.test", "configuration.package_name", "TESTING-never-installed"),
					resource.TestCheckResourceAttr("automox_policy.test", "configuration.package_version", "1.0.0"),
					resource.TestCheckResourceAttr("automox_policy.test", "configuration.os_family", "Windows"),
					resource.TestCheckResourceAttr("automox_policy.test", "schedule_days", "0"),
					resource.TestCheckResourceAttr("automox_policy.test", "server_count", "0"),
					resource.TestCheckResourceAttrSet("automox_policy.test", "uuid"),
				),
			},
			{
				// Also a non-rename update, on a type whose required fields differ.
				Config: configRequiredSoftware(groupName, parent, name, "2.0.0"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("automox_policy.test", "name", name),
					resource.TestCheckResourceAttr("automox_policy.test", "configuration.package_version", "2.0.0"),
				),
			},
			{
				Config:   configRequiredSoftware(groupName, parent, name, "2.0.0"),
				PlanOnly: true,
			},
		},
	})
}

// TestAccPolicy_RejectsIncompleteWorklet proves the plan-time validators catch
// what Automox reports as a field name the practitioner never wrote: its errors
// name "configuration.evaluation code", with a space.
//
// No API call is made, so these steps create nothing.
func TestAccPolicy_RejectsIncompleteWorklet(t *testing.T) {
	acceptance.SkipUnlessAcceptance(t)
	acceptance.PreCheck(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: protoV6(),
		Steps: []resource.TestStep{
			{
				Config:      configBareWorklet(`os_family = "Windows"`),
				ExpectError: regexp.MustCompile(`Missing configuration\.evaluation_code`),
			},
			{
				// os_family is required for worklets, which the vendor documentation
				// does not say.
				Config: configBareWorklet(`evaluation_code  = "exit 0"
    remediation_code = "exit 0"`),
				ExpectError: regexp.MustCompile(`Missing configuration\.os_family`),
			},
			{
				// Automox matches os_family exactly and rejects "windows".
				Config: configBareWorklet(`os_family        = "windows"
    evaluation_code  = "exit 0"
    remediation_code = "exit 0"`),
				ExpectError: regexp.MustCompile(`(?s)os_family.*value must be one of`),
			},
		},
	})
}

func configWorklet(groupName string, parent int64, name, notes, code string) string {
	return fmt.Sprintf(`
resource "automox_server_group" "target" {
  name                   = %q
  refresh_interval       = 1440
  parent_server_group_id = %d
}

resource "automox_policy" "test" {
  name          = %q
  policy_type   = "custom"
  notes         = %q
  server_groups = [automox_server_group.target.id]

  # Never runs: no scheduled day, and no automatic reboot.
  schedule_time = "00:00"
  schedule_days = 0

  configuration = {
    os_family        = "Windows"
    evaluation_code  = %q
    remediation_code = "exit 0"
    auto_reboot      = false
  }
}
`, groupName, parent, name, notes, code)
}

func configRequiredSoftware(groupName string, parent int64, name, version string) string {
	return fmt.Sprintf(`
resource "automox_server_group" "target" {
  name                   = %q
  refresh_interval       = 1440
  parent_server_group_id = %d
}

resource "automox_policy" "test" {
  name          = %q
  policy_type   = "required_software"
  notes         = "created by the provider acceptance suite"
  server_groups = [automox_server_group.target.id]

  # Never runs: no scheduled day, and no automatic reboot.
  schedule_time = "00:00"
  schedule_days = 0

  configuration = {
    os_family         = "Windows"
    package_name      = "TESTING-never-installed"
    package_version   = %q
    installation_code = "exit 0"
    auto_reboot       = false
  }
}
`, groupName, parent, name, version)
}

// configBareWorklet builds a worklet that must fail during plan, so it needs no
// server group and never reaches the API.
func configBareWorklet(configuration string) string {
	return fmt.Sprintf(`
resource "automox_policy" "invalid" {
  name          = "TESTING-never-created"
  policy_type   = "custom"
  notes         = "must fail during plan"
  server_groups = []
  schedule_time = "00:00"
  schedule_days = 0

  configuration = {
    %s
  }
}
`, configuration)
}

// configWorkletScripts is configWorklet with both scripts under test. The
// scripts are quoted with %q, which HCL reads with the same escapes as Go.
func configWorkletScripts(groupName string, parent int64, name, evaluation, remediation string) string {
	return fmt.Sprintf(`
resource "automox_server_group" "target" {
  name                   = %q
  refresh_interval       = 1440
  parent_server_group_id = %d
}

resource "automox_policy" "test" {
  name          = %q
  policy_type   = "custom"
  notes         = "script whitespace under test"
  server_groups = [automox_server_group.target.id]

  # Never runs: no scheduled day, and no automatic reboot.
  schedule_time = "00:00"
  schedule_days = 0

  configuration = {
    os_family        = "Windows"
    evaluation_code  = %q
    remediation_code = %q
    auto_reboot      = false
  }
}
`, groupName, parent, name, evaluation, remediation)
}

func configRequiredSoftwareCode(groupName string, parent int64, name, code string) string {
	return fmt.Sprintf(`
resource "automox_server_group" "target" {
  name                   = %q
  refresh_interval       = 1440
  parent_server_group_id = %d
}

resource "automox_policy" "test" {
  name          = %q
  policy_type   = "required_software"
  notes         = "installation_code whitespace under test"
  server_groups = [automox_server_group.target.id]

  # Never runs: no scheduled day, and no automatic reboot.
  schedule_time = "00:00"
  schedule_days = 0

  configuration = {
    os_family         = "Windows"
    package_name      = "TESTING-never-installed"
    package_version   = "1.0.0"
    installation_code = %q
    auto_reboot       = false
  }
}
`, groupName, parent, name, code)
}
