// Package device_group_assignment implements the
// automox_device_group_assignment resource.
//
// Every other resource in this provider manages an object Automox lets
// Terraform create and destroy: a server group, a policy, a scheduled window.
// A device is different. It comes into existence when the agent installs
// itself and registers, not through this API, and server_group_id is one of
// the few fields on an existing device record that Terraform can still change.
//
// That makes this resource's lifecycle asymmetric. Create does not create a
// device; it moves one Automox already knows about into the group this
// configuration names. Update moves it again. Delete deliberately does not
// move it anywhere: it only stops Terraform from tracking the assignment.
// Destroying this resource must never be the thing that sends a device back
// to the organization's unmanaged default group, so the API is not called at
// all on delete -- see the comment on Delete in crud.go.
package device_group_assignment

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"

	"github.com/sweetgreen/terraform-provider-automox/internal/client"
)

const ResourceName = "automox_device_group_assignment"

var (
	_ resource.Resource                = &deviceGroupAssignmentResource{}
	_ resource.ResourceWithConfigure   = &deviceGroupAssignmentResource{}
	_ resource.ResourceWithImportState = &deviceGroupAssignmentResource{}
)

// New returns the resource constructor for the provider registry.
func New() resource.Resource {
	return &deviceGroupAssignmentResource{}
}

type deviceGroupAssignmentResource struct {
	client *client.Client
}

func (r *deviceGroupAssignmentResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = ResourceName
}

func (r *deviceGroupAssignmentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		// Normal during early plan walks, before the provider is configured.
		return
	}

	c, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected provider data",
			fmt.Sprintf("Expected *client.Client, got %T. This is a bug in the provider.", req.ProviderData),
		)
		return
	}
	r.client = c
}

func (r *deviceGroupAssignmentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Places an existing Automox device into a server group.\n\n" +
			"Automox normally assigns a device's group when its agent installs, and the " +
			"console lets a device's group change at any time. This resource exists for a " +
			"device that already has the agent installed and needs to move into a group " +
			"Terraform manages, without reinstalling it and without moving it by hand in " +
			"the console -- either of which would be invisible to Terraform and show up as " +
			"drift the next time something else touches the device.\n\n" +
			"Destroying this resource does **not** move the device back to its previous " +
			"group, including the organization's default group. It only removes the " +
			"assignment from Terraform state. A destroy that silently returned a fleet of " +
			"devices to an unpatched default group would be far more dangerous than one " +
			"that leaves them exactly where they were.",
		Attributes: map[string]schema.Attribute{
			"device_id": schema.Int64Attribute{
				Required: true,
				MarkdownDescription: "The Automox server ID of the device to place in a group. " +
					"Forces replacement: this resource is keyed to one device record, not a " +
					"name or a filter, so a changed `device_id` means managing a different " +
					"assignment rather than updating this one.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.RequiresReplace(),
				},
			},
			"server_group_id": schema.Int64Attribute{
				Required: true,
				MarkdownDescription: "The Automox server group ID the device should belong to. " +
					"Changing this moves the device again; it does not replace the resource.",
			},
			"organization_id": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Organization this device belongs to, resolved from the provider's `organization_id`.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
		},
	}
}

// ImportState accepts the numeric Automox device (server) ID. The organization
// comes from provider configuration, as with every other resource here.
func (r *deviceGroupAssignmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id, err := parseImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			fmt.Sprintf("Expected a numeric Automox device ID, got %q: %s.\n\n"+
				"The ID appears in the Automox console URL for the device, and in the "+
				"automox_device and automox_devices data sources.", req.ID, err),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("device_id"), id)...)
}
