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

// These read a real fleet of employee laptops. They create and change nothing.

// excludedDeviceAttributes are the fields this provider refuses to put into
// Terraform state.
//
// Together they identify whose machine a device is: who last logged in, its
// serial and service tag, its fully qualified names and Active Directory path.
// State is stored in plaintext and shared far more freely than that warrants.
var excludedDeviceAttributes = []string{
	"last_logged_in_user",
	"serial_number",
	"detail",
	"instance_id",
	"ip_addrs_private",
}

// TestAccDevices_ExcludesIdentifyingAttributes is the privacy assertion for the
// device data sources, and the counterpart to the access key check on
// organizations.
func TestAccDevices_ExcludesIdentifyingAttributes(t *testing.T) {
	acceptance.SkipUnlessAcceptance(t)
	acceptance.PreCheck(t)

	checks := []resource.TestCheckFunc{
		resource.TestCheckResourceAttrSet("data.automox_devices.sample", "devices.#"),
		resource.TestCheckResourceAttrSet("data.automox_devices.sample", "devices.0.id"),
		resource.TestCheckResourceAttrSet("data.automox_devices.sample", "devices.0.os_family"),
		resource.TestCheckResourceAttrSet("data.automox_devices.sample", "devices.0.server_group_id"),
	}
	for _, attr := range excludedDeviceAttributes {
		checks = append(checks, resource.TestCheckNoResourceAttr(
			"data.automox_devices.sample", "devices.0."+attr))
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: protoV6(),
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`data "automox_devices" "sample" {
  group_id = %d
}`, defaultGroupID(t)),
				Check: resource.ComposeAggregateTestCheckFunc(checks...),
			},
		},
	})
}

// TestAccDevices_Filters proves each filter narrows the result rather than being
// silently ignored, which is how a filter that does nothing usually hides.
func TestAccDevices_Filters(t *testing.T) {
	acceptance.SkipUnlessAcceptance(t)
	acceptance.PreCheck(t)

	group := defaultGroupID(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: protoV6(),
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
data "automox_devices" "in_group" {
  group_id = %d
}

data "automox_devices" "windows" {
  group_id  = %d
  os_family = "Windows"
}

data "automox_devices" "connected" {
  group_id  = %d
  connected = true
}
`, group, group, group),
				Check: resource.ComposeAggregateTestCheckFunc(
					// group_id is applied by the API, the others to the results.
					checkEveryDeviceInGroup("data.automox_devices.in_group", group),
					checkEveryDeviceAttr("data.automox_devices.windows", "os_family", "Windows"),
					checkEveryDeviceAttr("data.automox_devices.connected", "connected", "true"),
					// The narrowed sets must be strict subsets, or the filter did nothing.
					checkFewerThan("data.automox_devices.windows", "data.automox_devices.in_group"),
				),
			},
		},
	})
}

// TestAccDevice_Single covers the single-device lookup and its failure mode.
func TestAccDevice_Single(t *testing.T) {
	acceptance.SkipUnlessAcceptance(t)
	acceptance.PreCheck(t)

	id := anyDeviceID(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: protoV6(),
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`data "automox_device" "one" {
  id = %d
}`, id),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.automox_device.one", "id", strconv.FormatInt(id, 10)),
					resource.TestCheckResourceAttrSet("data.automox_device.one", "uuid"),
					resource.TestCheckResourceAttrSet("data.automox_device.one", "os_family"),
					resource.TestCheckNoResourceAttr("data.automox_device.one", "last_logged_in_user"),
					resource.TestCheckNoResourceAttr("data.automox_device.one", "serial_number"),
				),
			},
		},
	})
}

// TestAccDevicePackages reads one device's packages. It asserts the shape rather
// than a count, since what is installed on a real laptop changes.
func TestAccDevicePackages(t *testing.T) {
	acceptance.SkipUnlessAcceptance(t)
	acceptance.PreCheck(t)

	id := connectedDeviceID(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: protoV6(),
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
data "automox_device_packages" "installed" {
  device_id = %d
  installed = true
}

data "automox_device_packages" "all" {
  device_id = %d
}
`, id, id),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.automox_device_packages.installed", "packages.#"),
					checkEveryPackageInstalled("data.automox_device_packages.installed"),
					// Automox returns cve_score as a quoted string ("9.6"). The
					// provider parses it to a number, and a device's package list
					// decodes only if that conversion holds -- this asserts the parsed
					// value is a sane CVSS score rather than a zero that a failed
					// conversion would leave behind.
					checkCVEScoresParse("data.automox_device_packages.all"),
				),
			},
		},
	})
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

type probeDevice struct {
	ID        int64 `json:"id"`
	Connected bool  `json:"connected"`
}

func someDevice(t *testing.T, wantConnected bool) int64 {
	t.Helper()
	var devices []probeDevice
	if err := testClient(t).List(context.Background(), client.ListOptions{
		Path: "/servers", OrgScope: client.OrgScopeQuery, Envelope: client.EnvelopeArray,
	}, &devices); err != nil {
		t.Fatalf("listing devices: %v", err)
	}
	for _, d := range devices {
		if !wantConnected || d.Connected {
			return d.ID
		}
	}
	t.Skip("no suitable device in this organization to read")
	return 0
}

func anyDeviceID(t *testing.T) int64       { return someDevice(t, false) }
func connectedDeviceID(t *testing.T) int64 { return someDevice(t, true) }

func checkEveryDeviceInGroup(name string, group int64) resource.TestCheckFunc {
	return checkEveryDeviceAttr(name, "server_group_id", strconv.FormatInt(group, 10))
}

func checkEveryDeviceAttr(name, attr, want string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		attrs, err := attributesOf(s, name)
		if err != nil {
			return err
		}
		count, _ := strconv.Atoi(attrs["devices.#"])
		if count == 0 {
			return fmt.Errorf("%s returned no devices, so the filter is untested", name)
		}
		for i := 0; i < count; i++ {
			key := fmt.Sprintf("devices.%d.%s", i, attr)
			if got := attrs[key]; got != want {
				return fmt.Errorf("%s is %q, want %q", key, got, want)
			}
		}
		return nil
	}
}

// checkFewerThan guards against a filter that silently matches everything.
func checkFewerThan(narrow, wide string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		narrowAttrs, err := attributesOf(s, narrow)
		if err != nil {
			return err
		}
		wideAttrs, err := attributesOf(s, wide)
		if err != nil {
			return err
		}
		n, _ := strconv.Atoi(narrowAttrs["devices.#"])
		w, _ := strconv.Atoi(wideAttrs["devices.#"])
		if n >= w {
			return fmt.Errorf("%s returned %d devices and %s returned %d; the filter did not narrow anything",
				narrow, n, wide, w)
		}
		return nil
	}
}

// checkCVEScoresParse asserts every scored package carries a usable CVSS number.
//
// It skips rather than fails when the device has no scored packages: what is
// outstanding on a real laptop changes, so requiring one would make the test
// depend on the patch state of somebody's machine.
func checkCVEScoresParse(name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		attrs, err := attributesOf(s, name)
		if err != nil {
			return err
		}

		count, _ := strconv.Atoi(attrs["packages.#"])
		scored := 0
		for i := 0; i < count; i++ {
			raw, ok := attrs[fmt.Sprintf("packages.%d.cve_score", i)]
			if !ok || raw == "" {
				continue
			}
			score, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return fmt.Errorf("packages.%d.cve_score is %q, which is not a number", i, raw)
			}
			if score <= 0 || score > 10 {
				return fmt.Errorf("packages.%d.cve_score is %v, outside the CVSS range; "+
					"a zero here usually means the string was never parsed", i, score)
			}
			scored++
		}
		if scored == 0 {
			fmt.Printf("  (no scored packages on this device; cve_score parsing not exercised)\n")
		}
		return nil
	}
}

func checkEveryPackageInstalled(name string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		attrs, err := attributesOf(s, name)
		if err != nil {
			return err
		}
		count, _ := strconv.Atoi(attrs["packages.#"])
		if count == 0 {
			return fmt.Errorf("%s returned no packages, so the installed filter is untested", name)
		}
		for i := 0; i < count; i++ {
			key := fmt.Sprintf("packages.%d.installed", i)
			if attrs[key] != "true" {
				return fmt.Errorf("%s is %q with installed = true set", key, attrs[key])
			}
		}
		return nil
	}
}
