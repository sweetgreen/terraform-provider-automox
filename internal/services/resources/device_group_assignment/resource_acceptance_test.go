package device_group_assignment_test

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

// sweetgreenTestOrganizationID is organization 183625, the only organization
// this suite is allowed to run TestAccDeviceGroupAssignment against. See
// acceptance.RequireOrganization.
const sweetgreenTestOrganizationID = 183625

func protoV6() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"automox": providerserver.NewProtocol6WithError(automox.New("acctest")()),
	}
}

// device is the subset of the server record this test reads and restores.
// It mirrors, but does not import, the resource's own unexported apiDevice --
// duplicating it here keeps this test honest about what the live API returns
// rather than trusting the production code's own idea of the shape.
type device struct {
	ID            int64    `json:"id"`
	Name          string   `json:"name"`
	ServerGroupID int64    `json:"server_group_id"`
	Exception     bool     `json:"exception"`
	Tags          []string `json:"tags"`
	CustomName    string   `json:"custom_name"`
	IPAddrs       []string `json:"ip_addrs"`
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

// defaultGroupID finds the organization's default group, identified by being
// its own parent, the same way the server_group acceptance suite does.
func defaultGroupID(t *testing.T, c *client.Client) int64 {
	t.Helper()

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

// anyDevice picks one device already registered in the organization. This
// resource has nothing to move if the organization has no devices, which the
// test cannot fix -- the agent registers a device, not this provider -- so it
// skips rather than failing when that is the case.
func anyDevice(t *testing.T, c *client.Client) device {
	t.Helper()

	var devices []device
	if err := c.List(context.Background(), client.ListOptions{
		Path: "/servers", OrgScope: client.OrgScopeQuery, Envelope: client.EnvelopeArray,
	}, &devices); err != nil {
		t.Fatalf("listing devices: %v", err)
	}
	if len(devices) == 0 {
		t.Skip("skipping: organization 183625 (Sweetgreen-Test) has no devices to move")
	}
	return devices[0]
}

// createTestGroup makes an empty, policy-less, TESTING-prefixed group the
// device can be moved into without affecting what patches it receives.
func createTestGroup(t *testing.T, c *client.Client, parent int64) int64 {
	t.Helper()

	var created struct {
		ID int64 `json:"id"`
	}
	if err := c.Do(context.Background(), client.Request{
		Method:   "POST",
		Path:     "/servergroups",
		OrgScope: client.OrgScopeQuery,
		Body: map[string]any{
			"name":                   acceptance.Name("device-group-assignment"),
			"refresh_interval":       1440,
			"parent_server_group_id": parent,
			"notes":                  "Transient target group created by the automox_device_group_assignment acceptance suite.",
		},
	}, &created); err != nil {
		t.Fatalf("creating a testing server group: %v", err)
	}
	if created.ID == 0 {
		t.Fatal("the testing server group was created but returned no id")
	}
	return created.ID
}

func deleteTestGroup(t *testing.T, c *client.Client, id int64) {
	t.Helper()

	if err := c.Do(context.Background(), client.Request{
		Method: "DELETE", Path: fmt.Sprintf("/servergroups/%d", id), OrgScope: client.OrgScopeQuery,
	}, nil); err != nil {
		t.Errorf("deleting testing server group %d: %v (it is now leaked in organization %d)",
			id, err, sweetgreenTestOrganizationID)
	}
}

// restoreDevice puts the device back exactly where it was before the test
// touched it. The resource's own Delete deliberately does not do this -- see
// the comment on Delete in crud.go -- so the test does it directly against
// the API, the same way it would if a human had to clean up after moving a
// device by hand.
func restoreDevice(t *testing.T, c *client.Client, original device) {
	t.Helper()

	tags := original.Tags
	if tags == nil {
		tags = []string{}
	}
	ipAddrs := original.IPAddrs
	if ipAddrs == nil {
		ipAddrs = []string{}
	}

	err := c.Do(context.Background(), client.Request{
		Method:   "PUT",
		Path:     fmt.Sprintf("/servers/%d", original.ID),
		OrgScope: client.OrgScopeQuery,
		Body: map[string]any{
			"server_group_id": original.ServerGroupID,
			"exception":       original.Exception,
			"tags":            tags,
			"custom_name":     original.CustomName,
			"ip_addrs":        ipAddrs,
		},
	}, nil)
	if err != nil {
		t.Errorf("restoring device %d to its original server group %d: %v\n\n"+
			"The device is still in a test group in organization %d and must be moved back by hand.",
			original.ID, original.ServerGroupID, err, sweetgreenTestOrganizationID)
	}
}

func checkDeviceInGroup(t *testing.T, c *client.Client, deviceID, want int64) resource.TestCheckFunc {
	return func(*terraform.State) error {
		var got device
		if err := c.Do(context.Background(), client.Request{
			Method: "GET", Path: fmt.Sprintf("/servers/%d", deviceID), OrgScope: client.OrgScopeQuery,
		}, &got); err != nil {
			return fmt.Errorf("reading device %d: %w", deviceID, err)
		}
		if got.ServerGroupID != want {
			return fmt.Errorf("device %d is in server group %d, want %d", deviceID, got.ServerGroupID, want)
		}
		return nil
	}
}

// TestAccDeviceGroupAssignment_Lifecycle covers create (placing an already-
// registered device into a group), update (moving it again), import, and
// destroy -- specifically that destroy does NOT move the device anywhere.
//
// This differs from every other acceptance test in this provider in one
// important way: it does not create the object it manages. It borrows a
// device that is already registered in organization 183625 (Sweetgreen-Test),
// moves it between two disposable TESTING-prefixed groups, and restores it to
// its original group afterward -- not through Terraform, which by design
// cannot do that, but as an explicit cleanup step the same as a human would
// perform after moving a device by hand.
func TestAccDeviceGroupAssignment_Lifecycle(t *testing.T) {
	// Must come first: everything below reaches the live API, and none of it
	// may run during a plain `go test ./...`.
	acceptance.SkipUnlessAcceptance(t)
	acceptance.PreCheck(t)
	acceptance.RequireOrganization(t, sweetgreenTestOrganizationID)
	acceptance.RequireWriteScope(t)

	c := testClient(t)

	original := anyDevice(t, c)
	parent := defaultGroupID(t, c)
	groupA := createTestGroup(t, c, parent)
	groupB := createTestGroup(t, c, parent)

	t.Cleanup(func() {
		restoreDevice(t, c, original)
		deleteTestGroup(t, c, groupA)
		deleteTestGroup(t, c, groupB)
	})

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: protoV6(),
		Steps: []resource.TestStep{
			{
				Config: config(original.ID, groupA),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("automox_device_group_assignment.test",
						"device_id", strconv.FormatInt(original.ID, 10)),
					resource.TestCheckResourceAttr("automox_device_group_assignment.test",
						"server_group_id", strconv.FormatInt(groupA, 10)),
					resource.TestCheckResourceAttrSet("automox_device_group_assignment.test", "organization_id"),
					checkDeviceInGroup(t, c, original.ID, groupA),
				),
			},
			{
				ResourceName:      "automox_device_group_assignment.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateId:     strconv.FormatInt(original.ID, 10),
			},
			{
				// Update: move it again rather than replace the resource.
				Config: config(original.ID, groupB),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("automox_device_group_assignment.test",
						"server_group_id", strconv.FormatInt(groupB, 10)),
					checkDeviceInGroup(t, c, original.ID, groupB),
				),
			},
			{
				// Destroying the resource (an empty config has nothing left to
				// manage) must remove it from state without moving the device. The
				// deferred cleanup above, not this step, is what puts it back.
				Config: `# intentionally empty: nothing left for Terraform to manage`,
				Check:  checkDeviceInGroup(t, c, original.ID, groupB),
			},
		},
	})
}

func config(deviceID, serverGroupID int64) string {
	return fmt.Sprintf(`
resource "automox_device_group_assignment" "test" {
  device_id       = %d
  server_group_id = %d
}
`, deviceID, serverGroupID)
}
