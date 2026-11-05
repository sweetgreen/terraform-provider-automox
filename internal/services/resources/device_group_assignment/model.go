package device_group_assignment

import (
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// deviceGroupAssignmentModel is the Terraform-facing shape of
// automox_device_group_assignment.
type deviceGroupAssignmentModel struct {
	DeviceID       types.Int64 `tfsdk:"device_id"`
	ServerGroupID  types.Int64 `tfsdk:"server_group_id"`
	OrganizationID types.Int64 `tfsdk:"organization_id"`
}

// apiDevice is the subset of the device (server) record this resource reads
// and writes.
//
// Automox's update endpoint takes the full set of writable fields, not a
// patch of what changed, so every field here is both read before a write and
// sent on every write -- otherwise a write that only meant to change the
// group would silently clear exception and tags, which this resource does
// not own and must leave exactly as Automox already had them.
//
// This deliberately mirrors only the writable fields. It is not the same
// struct the automox_device and automox_devices data sources use: those
// expose read-only reporting fields that have no place in a write body, and
// this one needs organization_id and exception, which they do not expose.
type apiDevice struct {
	ID             int64    `json:"id"`
	Name           string   `json:"name"`
	ServerGroupID  int64    `json:"server_group_id"`
	OrganizationID int64    `json:"organization_id"`
	Exception      bool     `json:"exception"`
	Tags           []string `json:"tags"`
	CustomName     string   `json:"custom_name"`
	IPAddrs        []string `json:"ip_addrs"`
}
