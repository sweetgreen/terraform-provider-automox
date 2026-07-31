package policy_file_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
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

const probeScript = "Write-Output 'TESTING placeholder; this worklet never runs'\n"

// TestAccPolicyFile_Lifecycle covers upload, read, replace, and delete.
//
// The worklet that owns the file targets a group this test creates, which holds
// no devices, and is scheduled with schedule_days = 0 so it can never run. The
// file itself is an inert script.
func TestAccPolicyFile_Lifecycle(t *testing.T) {
	acceptance.SkipUnlessAcceptance(t)
	acceptance.PreCheck(t)
	acceptance.RequireWriteScope(t)

	parent := defaultGroupID(t)
	groupName := acceptance.Name("file-target")
	policyName := acceptance.Name("file-worklet")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: protoV6(),
		CheckDestroy:             checkDestroy,
		Steps: []resource.TestStep{
			{
				Config: config(groupName, parent, policyName, "install.ps1", probeScript),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("automox_policy_file.test", "filename", "install.ps1"),
					resource.TestCheckResourceAttrSet("automox_policy_file.test", "uuid"),
					// Automox records the size it received; a mismatch would mean the
					// multipart body was truncated or double-encoded.
					resource.TestCheckResourceAttr("automox_policy_file.test", "size",
						strconv.Itoa(len(probeScript))),
					resource.TestCheckResourceAttr("automox_policy_file.test", "content_sha256",
						sha256Hex(probeScript)),
					// The file must actually be on the policy, not merely in state.
					checkFileOnPolicy("automox_policy_file.test", "install.ps1"),
				),
			},
			{
				Config:   config(groupName, parent, policyName, "install.ps1", probeScript),
				PlanOnly: true,
			},
			{
				// Changing content replaces the file: Automox rejects a second upload
				// under a name already present, so update-in-place is impossible.
				Config: config(groupName, parent, policyName, "install.ps1", probeScript+"# revised\n"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("automox_policy_file.test", "content_sha256",
						sha256Hex(probeScript+"# revised\n")),
					resource.TestCheckResourceAttr("automox_policy_file.test", "size",
						strconv.Itoa(len(probeScript+"# revised\n"))),
					// Exactly one file, so the replacement removed the old one rather
					// than leaving both behind.
					checkFileCount("automox_policy_file.test", 1),
				),
			},
		},
	})
}

// TestAccPolicyFile_RejectsDuplicateName proves the error a practitioner gets
// when two resources claim the same filename on one policy.
//
// depends_on is load-bearing. Without it Terraform uploads both files
// concurrently, and two simultaneous uploads of the same name race inside
// Automox into a 500 rather than the duplicate rejection -- a different failure
// with a far less useful message. Serialising them exercises the documented
// path; the race is noted in the package documentation.
func TestAccPolicyFile_RejectsDuplicateName(t *testing.T) {
	acceptance.SkipUnlessAcceptance(t)
	acceptance.PreCheck(t)
	acceptance.RequireWriteScope(t)

	parent := defaultGroupID(t)
	groupName := acceptance.Name("dupe-target")
	policyName := acceptance.Name("dupe-worklet")

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acceptance.PreCheck(t) },
		ProtoV6ProviderFactories: protoV6(),
		CheckDestroy:             checkDestroy,
		Steps: []resource.TestStep{
			{
				Config: config(groupName, parent, policyName, "dupe.ps1", probeScript) + `
resource "automox_policy_file" "second" {
  policy_id  = automox_policy.owner.id
  filename   = "dupe.ps1"
  content    = "different content\n"
  depends_on = [automox_policy_file.test]
}
`,
				ExpectError: regexp.MustCompile(`(?s)A file with that name already exists`),
			},
		},
	})
}

func config(groupName string, parent int64, policyName, filename, content string) string {
	return fmt.Sprintf(`
resource "automox_server_group" "target" {
  name                   = %q
  refresh_interval       = 1440
  parent_server_group_id = %d
}

resource "automox_policy" "owner" {
  name          = %q
  policy_type   = "custom"
  notes         = "owns a file for the acceptance suite"
  server_groups = [automox_server_group.target.id]

  # Never runs.
  schedule_time = "00:00"
  schedule_days = 0

  configuration = {
    os_family        = "Windows"
    evaluation_code  = "exit 1"
    remediation_code = "exit 0"
    auto_reboot      = false
  }
}

resource "automox_policy_file" "test" {
  policy_id = automox_policy.owner.id
  filename  = %q
  content   = %q
}
`, groupName, parent, policyName, filename, content)
}

func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

type wireFile struct {
	UUID     string `json:"uuid"`
	Filename string `json:"filename"`
}

func checkFileOnPolicy(name, filename string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return fmt.Errorf("%s not found in state", name)
		}
		policyID, _ := strconv.ParseInt(rs.Primary.Attributes["policy_id"], 10, 64)
		wantUUID := rs.Primary.Attributes["uuid"]

		files, err := listFiles(policyID)
		if err != nil {
			return err
		}
		for _, f := range files {
			if f.UUID == wantUUID && f.Filename == filename {
				return nil
			}
		}
		return fmt.Errorf("file %s (%q) is in state but not on policy %d", wantUUID, filename, policyID)
	}
}

func checkFileCount(name string, want int) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return fmt.Errorf("%s not found in state", name)
		}
		policyID, _ := strconv.ParseInt(rs.Primary.Attributes["policy_id"], 10, 64)

		files, err := listFiles(policyID)
		if err != nil {
			return err
		}
		if len(files) != want {
			names := make([]string, 0, len(files))
			for _, f := range files {
				names = append(names, f.Filename)
			}
			return fmt.Errorf("policy %d has %d files %v, want %d; a replacement that "+
				"uploads before deleting would leave the old one behind",
				policyID, len(files), names, want)
		}
		return nil
	}
}

// checkDestroy asserts the file is gone from Automox, not merely from state.
func checkDestroy(s *terraform.State) error {
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "automox_policy_file" {
			continue
		}
		policyID, err := strconv.ParseInt(rs.Primary.Attributes["policy_id"], 10, 64)
		if err != nil {
			continue
		}

		files, err := listFiles(policyID)
		if err != nil {
			// The owning policy is destroyed in the same run, so its file listing
			// disappearing is the expected outcome rather than a failure.
			return nil
		}
		for _, f := range files {
			if f.UUID == rs.Primary.Attributes["uuid"] {
				return fmt.Errorf("file %s still attached to policy %d after destroy",
					f.UUID, policyID)
			}
		}
	}
	return nil
}

// listFiles builds its own client because CheckDestroy and TestCheckFunc have no
// *testing.T to fail through.
func listFiles(policyID int64) ([]wireFile, error) {
	orgID, err := strconv.ParseInt(os.Getenv(acceptance.EnvOrganizationID), 10, 64)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", acceptance.EnvOrganizationID, err)
	}
	c, err := client.New(client.Options{APIKey: os.Getenv(acceptance.EnvAPIKey), OrganizationID: orgID})
	if err != nil {
		return nil, err
	}

	var files []wireFile
	if err := c.List(context.Background(), client.ListOptions{
		Path: fmt.Sprintf("/policies/%d/files", policyID), OrgScope: client.OrgScopeQuery,
		Envelope: client.EnvelopeArray,
	}, &files); err != nil {
		return nil, err
	}
	return files, nil
}
