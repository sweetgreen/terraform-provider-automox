package datasources_test

import (
	"fmt"
	"regexp"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/sweetgreen/terraform-provider-automox/internal/acceptance"
)

// TestAccEvents_BoundedAndTruncated is the important one.
//
// The event log runs past 200 pages in this organization, so an unbounded read
// would be a slow plan that deposits tens of thousands of records into state.
// This asserts the cap holds and that hitting it is reported rather than
// returning a prefix that looks like the whole log.
func TestAccEvents_BoundedAndTruncated(t *testing.T) {
	acceptance.SkipUnlessAcceptance(t)
	acceptance.PreCheck(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: protoV6(),
		Steps: []resource.TestStep{
			{
				Config: `data "automox_events" "recent" {
  max_results = 5
}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.automox_events.recent", "events.#", "5"),
					// The log is far longer than five events, so the cap was hit.
					resource.TestCheckResourceAttr("data.automox_events.recent", "truncated", "true"),
					resource.TestCheckResourceAttrSet("data.automox_events.recent", "events.0.id"),
					resource.TestCheckResourceAttrSet("data.automox_events.recent", "events.0.name"),
					// The free-form data blob carries employee names on user events.
					resource.TestCheckNoResourceAttr("data.automox_events.recent", "events.0.data"),
				),
			},
			{
				// A filter narrow enough to fall under the cap must report
				// truncated = false, or the flag is just always true.
				Config: fmt.Sprintf(`data "automox_events" "one_device" {
  device_id   = %d
  max_results = 1000
}`, anyDeviceID(t)),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.automox_events.one_device", "truncated", "false"),
					checkEveryEventForDevice("data.automox_events.one_device"),
				),
			},
			{
				Config: `data "automox_events" "typed" {
  event_name  = "system.patch.applied"
  max_results = 20
}`,
				Check: checkEveryEventNamed("data.automox_events.typed", "system.patch.applied"),
			},
			{
				Config:      `data "automox_events" "bad" { max_results = 0 }`,
				ExpectError: regexp.MustCompile(`(?s)max_results.*must be at least 1`),
			},
			{
				// End on a valid config: the harness re-plans the last one to tear it
				// down, and an invalid config fails that plan.
				Config: `data "automox_events" "recent" { max_results = 5 }`,
			},
		},
	})
}

// TestAccWorklets covers the catalogue search and a single entry.
func TestAccWorklets(t *testing.T) {
	acceptance.SkipUnlessAcceptance(t)
	acceptance.PreCheck(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: protoV6(),
		Steps: []resource.TestStep{
			{
				Config: `
data "automox_worklets" "all" {}

data "automox_worklets" "windows" {
  os_family = "Windows"
}

data "automox_worklets" "security" {
  category = "Security"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.automox_worklets.all", "worklets.#"),
					resource.TestCheckResourceAttrSet("data.automox_worklets.all", "worklets.0.uuid"),
					resource.TestCheckResourceAttrSet("data.automox_worklets.all", "worklets.0.name"),
					// Both filters are applied by the API; assert they narrow rather
					// than being accepted and ignored, which is how language behaves.
					checkEveryWorkletAttr("data.automox_worklets.windows", "os_family", "Windows"),
					checkNarrower("data.automox_worklets.windows", "data.automox_worklets.all", "worklets"),
					checkNarrower("data.automox_worklets.security", "data.automox_worklets.all", "worklets"),
				),
			},
			{
				Config: `
data "automox_worklets" "all" {}

data "automox_worklet" "one" {
  uuid = data.automox_worklets.all.worklets[0].uuid
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair(
						"data.automox_worklet.one", "uuid",
						"data.automox_worklets.all", "worklets.0.uuid"),
					resource.TestCheckResourceAttrPair(
						"data.automox_worklet.one", "name",
						"data.automox_worklets.all", "worklets.0.name"),
					resource.TestCheckResourceAttrSet("data.automox_worklet.one", "os_family"),
				),
			},
		},
	})
}

func checkEveryEventNamed(name, want string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		attrs, err := attributesOf(s, name)
		if err != nil {
			return err
		}
		count, _ := strconv.Atoi(attrs["events.#"])
		if count == 0 {
			return fmt.Errorf("%s returned no events, so the event_name filter is untested", name)
		}
		for i := 0; i < count; i++ {
			key := fmt.Sprintf("events.%d.name", i)
			if got := attrs[key]; got != want {
				return fmt.Errorf("%s is %q, want %q", key, got, want)
			}
		}
		return nil
	}
}

func checkEveryEventForDevice(name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		attrs, err := attributesOf(s, name)
		if err != nil {
			return err
		}
		want := attrs["device_id"]
		count, _ := strconv.Atoi(attrs["events.#"])
		for i := 0; i < count; i++ {
			key := fmt.Sprintf("events.%d.device_id", i)
			if got := attrs[key]; got != want {
				return fmt.Errorf("%s is %q, want %q", key, got, want)
			}
		}
		return nil
	}
}

func checkEveryWorkletAttr(name, attr, want string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		attrs, err := attributesOf(s, name)
		if err != nil {
			return err
		}
		count, _ := strconv.Atoi(attrs["worklets.#"])
		if count == 0 {
			return fmt.Errorf("%s returned no worklets, so the filter is untested", name)
		}
		for i := 0; i < count; i++ {
			key := fmt.Sprintf("worklets.%d.%s", i, attr)
			if got := attrs[key]; got != want {
				return fmt.Errorf("%s is %q, want %q", key, got, want)
			}
		}
		return nil
	}
}

// checkNarrower guards against a filter the API accepts and ignores.
func checkNarrower(narrow, wide, collection string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		narrowAttrs, err := attributesOf(s, narrow)
		if err != nil {
			return err
		}
		wideAttrs, err := attributesOf(s, wide)
		if err != nil {
			return err
		}
		n, _ := strconv.Atoi(narrowAttrs[collection+".#"])
		w, _ := strconv.Atoi(wideAttrs[collection+".#"])
		if n == 0 {
			return fmt.Errorf("%s returned nothing", narrow)
		}
		if n >= w {
			return fmt.Errorf("%s returned %d and %s returned %d; the filter was accepted but ignored",
				narrow, n, wide, w)
		}
		return nil
	}
}
