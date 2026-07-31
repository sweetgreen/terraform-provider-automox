package datasources_test

import (
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
	automox "github.com/sweetgreen/terraform-provider-automox/internal/provider"
)

// These tests only read. They create nothing, so unlike the resource suites they
// need no write scope and leave no objects behind -- which is what makes them
// safe to run against the live organization.

func protoV6() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"automox": providerserver.NewProtocol6WithError(automox.New("acctest")()),
	}
}

// TestAccOrganizations_ExcludesAccessKey is the security assertion for this
// package.
//
// Automox returns a live organization access key from /orgs. Every data source
// attribute is written to Terraform state in plaintext, so exposing it would put
// a credential into state files and CI artefacts. The attribute must not exist
// at all -- marking it sensitive would still store it.
func TestAccOrganizations_ExcludesAccessKey(t *testing.T) {
	acceptance.SkipUnlessAcceptance(t)
	acceptance.PreCheck(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: protoV6(),
		Steps: []resource.TestStep{
			{
				Config: `data "automox_organizations" "all" {}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.automox_organizations.all", "organizations.#"),
					resource.TestCheckResourceAttrSet("data.automox_organizations.all", "organizations.0.id"),
					resource.TestCheckResourceAttrSet("data.automox_organizations.all", "organizations.0.uuid"),
					resource.TestCheckResourceAttrSet("data.automox_organizations.all", "organizations.0.name"),
					resource.TestCheckResourceAttrSet("data.automox_organizations.all", "organizations.0.device_count"),
					// The point of the test.
					resource.TestCheckNoResourceAttr("data.automox_organizations.all", "organizations.0.access_key"),
					// The configured organization must be among the results.
					checkOrganizationPresent("data.automox_organizations.all"),
				),
			},
		},
	})
}

// TestAccServerGroups covers the unfiltered read, the name filter, and the
// derived is_default flag.
func TestAccServerGroups(t *testing.T) {
	acceptance.SkipUnlessAcceptance(t)
	acceptance.PreCheck(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: protoV6(),
		Steps: []resource.TestStep{
			{
				Config: `data "automox_server_groups" "all" {}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.automox_server_groups.all", "server_groups.#"),
					resource.TestCheckResourceAttrSet("data.automox_server_groups.all", "server_groups.0.id"),
					// The undocumented uuid, which automox_scheduled_window needs.
					resource.TestCheckResourceAttrSet("data.automox_server_groups.all", "server_groups.0.uuid"),
					// Automox marks the default group by making it its own parent
					// rather than with a flag, so exactly one must be derived.
					checkExactlyOneDefaultGroup("data.automox_server_groups.all"),
				),
			},
			{
				// A name that cannot exist must fail loudly rather than returning an
				// empty list a practitioner would then index into.
				Config: `data "automox_server_groups" "missing" {
  name = "TESTING-no-such-group-anywhere"
}`,
				ExpectError: regexp.MustCompile(`No Automox server group has that name`),
			},
		},
	})
}

// TestAccPolicies covers the unfiltered read and the policy_type filter, and
// asserts that worklet source code is not exposed.
func TestAccPolicies(t *testing.T) {
	acceptance.SkipUnlessAcceptance(t)
	acceptance.PreCheck(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: protoV6(),
		Steps: []resource.TestStep{
			{
				Config: `data "automox_policies" "all" {}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.automox_policies.all", "policies.#"),
					resource.TestCheckResourceAttrSet("data.automox_policies.all", "policies.0.id"),
					resource.TestCheckResourceAttrSet("data.automox_policies.all", "policies.0.policy_type"),
					// configuration is deliberately absent: it carries worklet scripts.
					resource.TestCheckNoResourceAttr("data.automox_policies.all", "policies.0.configuration"),
				),
			},
			{
				Config: `data "automox_policies" "worklets" {
  policy_type = "custom"
}`,
				Check: checkEveryPolicyHasType("data.automox_policies.worklets", "custom"),
			},
			{
				// The resource validates the same set, so an unknown type is rejected
				// before the request rather than returning nothing. "worklet" is the
				// obvious guess and is wrong; the wire name is "custom".
				Config: `data "automox_policies" "bad" {
  policy_type = "worklet"
}`,
				ExpectError: regexp.MustCompile(`(?s)policy_type.*value must be one of`),
			},
			{
				// The run must end on a valid configuration. The harness plans the
				// final config once more to destroy it, and a config left in the
				// rejected state fails that plan for the reason the previous step was
				// asserting -- reported as a teardown error rather than a pass.
				Config: `data "automox_policies" "all" {}`,
			},
		},
	})
}

func TestAccPolicyStats(t *testing.T) {
	acceptance.SkipUnlessAcceptance(t)
	acceptance.PreCheck(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: protoV6(),
		Steps: []resource.TestStep{
			{
				Config: `data "automox_policy_stats" "all" {}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.automox_policy_stats.all", "policy_stats.#"),
					resource.TestCheckResourceAttrSet("data.automox_policy_stats.all", "policy_stats.0.policy_id"),
					resource.TestCheckResourceAttrSet("data.automox_policy_stats.all", "policy_stats.0.compliant"),
					resource.TestCheckResourceAttrSet("data.automox_policy_stats.all", "policy_stats.0.noncompliant"),
				),
			},
		},
	})
}

func checkOrganizationPresent(name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		attrs, err := attributesOf(s, name)
		if err != nil {
			return err
		}
		want := os.Getenv(acceptance.EnvOrganizationID)

		count, _ := strconv.Atoi(attrs["organizations.#"])
		for i := 0; i < count; i++ {
			if attrs[fmt.Sprintf("organizations.%d.id", i)] == want {
				return nil
			}
		}
		return fmt.Errorf("configured organization %s not present in %d organizations", want, count)
	}
}

// checkExactlyOneDefaultGroup guards the derivation rather than the data: the
// default group is identified by being its own parent, so a change in that
// convention would otherwise pass unnoticed.
func checkExactlyOneDefaultGroup(name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		attrs, err := attributesOf(s, name)
		if err != nil {
			return err
		}

		count, _ := strconv.Atoi(attrs["server_groups.#"])
		if count == 0 {
			return fmt.Errorf("no server groups returned")
		}

		defaults := 0
		for i := 0; i < count; i++ {
			if attrs[fmt.Sprintf("server_groups.%d.is_default", i)] == "true" {
				defaults++
			}
		}
		if defaults != 1 {
			return fmt.Errorf("expected exactly one default server group, found %d of %d", defaults, count)
		}
		return nil
	}
}

func checkEveryPolicyHasType(name, want string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		attrs, err := attributesOf(s, name)
		if err != nil {
			return err
		}

		count, _ := strconv.Atoi(attrs["policies.#"])
		if count == 0 {
			return fmt.Errorf("filtering by policy_type=%q returned nothing, so the filter is untested", want)
		}
		for i := 0; i < count; i++ {
			if got := attrs[fmt.Sprintf("policies.%d.policy_type", i)]; got != want {
				return fmt.Errorf("policies.%d.policy_type is %q, want %q", i, got, want)
			}
		}
		return nil
	}
}

func attributesOf(s *terraform.State, name string) (map[string]string, error) {
	rs, ok := s.RootModule().Resources[name]
	if !ok {
		return nil, fmt.Errorf("%s not found in state", name)
	}
	return rs.Primary.Attributes, nil
}
