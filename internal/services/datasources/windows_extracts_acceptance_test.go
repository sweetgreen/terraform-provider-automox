package datasources_test

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/sweetgreen/terraform-provider-automox/internal/acceptance"
	"github.com/sweetgreen/terraform-provider-automox/internal/client"
)

// TestAccScheduledWindows reads maintenance windows through the POST-based
// search endpoint.
//
// Unlike the other data source tests this one creates something: the
// organization normally has no windows, so a pure read would assert against an
// empty list and pass whatever the decoding did. The window it creates is an
// exclusion covering a group the test also creates, which holds no devices, so
// it cannot affect patching.
func TestAccScheduledWindows(t *testing.T) {
	acceptance.SkipUnlessAcceptance(t)
	acceptance.PreCheck(t)
	acceptance.RequireWriteScope(t)

	groupName := acceptance.Name("winds-target")
	windowName := acceptance.Name("winds")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: protoV6(),
		CheckDestroy:             checkWindowsDestroyed,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
resource "automox_server_group" "target" {
  name                   = %q
  refresh_interval       = 1440
  parent_server_group_id = %d
}

resource "automox_scheduled_window" "test" {
  window_name        = %q
  window_description = "created by the data source acceptance suite"
  recurrence         = "ONCE"
  dtstart            = "2026-12-01T00:00:00Z"
  rrule              = "FREQ=DAILY;UNTIL=20261201T020000Z"
  group_uuids        = [automox_server_group.target.uuid]
}

data "automox_scheduled_windows" "all" {
  depends_on = [automox_scheduled_window.test]
}
`, groupName, defaultGroupID(t), windowName),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.automox_scheduled_windows.all", "scheduled_windows.#"),
					// The window just created must appear, with its fields decoded
					// out of the Spring page envelope this endpoint uses.
					checkWindowPresent("data.automox_scheduled_windows.all", windowName),
				),
			},
		},
	})
}

// TestAccDataExtracts is read-only and asserts the download URL is absent.
//
// A completed extract's download_url is a pre-signed link to an export of the
// organization's data, and state is plaintext.
func TestAccDataExtracts(t *testing.T) {
	acceptance.SkipUnlessAcceptance(t)
	acceptance.PreCheck(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: protoV6(),
		Steps: []resource.TestStep{
			{
				Config: `data "automox_data_extracts" "all" {}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.automox_data_extracts.all", "data_extracts.#"),
					resource.TestCheckResourceAttrSet("data.automox_data_extracts.all", "data_extracts.0.id"),
					resource.TestCheckResourceAttrSet("data.automox_data_extracts.all", "data_extracts.0.type"),
					// The point of the test.
					resource.TestCheckNoResourceAttr("data.automox_data_extracts.all", "data_extracts.0.download_url"),
					// parameters is flattened rather than mirrored as a blob, so these
					// must be populated for the flattening to be doing anything.
					checkExtractPeriodDecoded("data.automox_data_extracts.all"),
				),
			},
			{
				Config: `data "automox_data_extracts" "filtered" {
  type = "patch-history"
}`,
				Check: checkEveryExtractType("data.automox_data_extracts.filtered", "patch-history"),
			},
		},
	})
}

func checkWindowPresent(name, want string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		attrs, err := attributesOf(s, name)
		if err != nil {
			return err
		}
		count, _ := strconv.Atoi(attrs["scheduled_windows.#"])
		for i := 0; i < count; i++ {
			if attrs[fmt.Sprintf("scheduled_windows.%d.window_name", i)] != want {
				continue
			}
			// Found it. Fields spread across the page envelope must have decoded.
			for _, attr := range []string{"window_uuid", "org_uuid", "recurrence", "rrule", "dtstart"} {
				key := fmt.Sprintf("scheduled_windows.%d.%s", i, attr)
				if attrs[key] == "" {
					return fmt.Errorf("%s.%s is empty", name, key)
				}
			}
			if n, _ := strconv.Atoi(attrs[fmt.Sprintf("scheduled_windows.%d.group_uuids.#", i)]); n != 1 {
				return fmt.Errorf("%s: window %q has %d group_uuids, want 1", name, want, n)
			}
			return nil
		}
		return fmt.Errorf("%s: window %q not among the %d returned; the search envelope "+
			"may not have been unwrapped", name, want, count)
	}
}

func checkExtractPeriodDecoded(name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		attrs, err := attributesOf(s, name)
		if err != nil {
			return err
		}
		count, _ := strconv.Atoi(attrs["data_extracts.#"])
		if count == 0 {
			return fmt.Errorf("%s returned no extracts", name)
		}
		for i := 0; i < count; i++ {
			if attrs[fmt.Sprintf("data_extracts.%d.start_time", i)] != "" {
				return nil
			}
		}
		return fmt.Errorf("%s: no extract has start_time set, so the nested parameters "+
			"object was not flattened", name)
	}
}

func checkEveryExtractType(name, want string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		attrs, err := attributesOf(s, name)
		if err != nil {
			return err
		}
		count, _ := strconv.Atoi(attrs["data_extracts.#"])
		if count == 0 {
			return fmt.Errorf("%s returned nothing, so the type filter is untested", name)
		}
		for i := 0; i < count; i++ {
			key := fmt.Sprintf("data_extracts.%d.type", i)
			if got := attrs[key]; got != want {
				return fmt.Errorf("%s is %q, want %q", key, got, want)
			}
		}
		return nil
	}
}

// checkWindowsDestroyed asserts the window this test created is really gone.
// A leaked maintenance window would suppress patching on whatever it covers.
//
// It queries Automox rather than inspecting state: CheckDestroy is handed the
// state describing what was just destroyed, so those resources are still listed
// and a state-only check could never fail.
func checkWindowsDestroyed(s *terraform.State) error {
	wanted := map[string]bool{}
	for _, rs := range s.RootModule().Resources {
		if rs.Type == "automox_scheduled_window" {
			if u := rs.Primary.Attributes["window_uuid"]; u != "" {
				wanted[u] = true
			}
		}
	}
	if len(wanted) == 0 {
		return nil
	}

	// Not testClient: that takes a *testing.T and calls t.Fatalf, and CheckDestroy
	// has no T to hand it.
	c, err := client.New(client.Options{
		APIKey:         os.Getenv(acceptance.EnvAPIKey),
		OrganizationID: orgIDFromEnv(),
	})
	if err != nil {
		return fmt.Errorf("building a client to confirm destruction: %w", err)
	}

	orgUUID, err := c.DefaultOrganizationUUID(context.Background())
	if err != nil {
		return fmt.Errorf("resolving organization UUID to confirm destruction: %w", err)
	}

	var page struct {
		Content []struct {
			WindowUUID string `json:"window_uuid"`
			WindowName string `json:"window_name"`
		} `json:"content"`
	}
	if err := c.Do(context.Background(), client.Request{
		Method:   "POST",
		Path:     fmt.Sprintf("/policy-windows/org/%s/search", orgUUID),
		OrgScope: client.OrgScopeNone,
		Body:     map[string]any{"page": 0, "size": 100},
	}, &page); err != nil {
		return fmt.Errorf("listing maintenance windows to confirm destruction: %w", err)
	}

	for _, w := range page.Content {
		if wanted[w.WindowUUID] {
			return fmt.Errorf("maintenance window %s (%q) still exists after destroy; "+
				"remove it manually", w.WindowUUID, w.WindowName)
		}
	}
	return nil
}

// orgIDFromEnv reads the organization ID without a *testing.T. A malformed value
// yields 0, which fails the subsequent API call loudly rather than silently
// querying the wrong organization.
func orgIDFromEnv() int64 {
	id, err := strconv.ParseInt(os.Getenv(acceptance.EnvOrganizationID), 10, 64)
	if err != nil {
		return 0
	}
	return id
}
