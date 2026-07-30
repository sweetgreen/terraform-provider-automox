// Package server_group implements the automox_server_group resource.
//
// Server groups are how Automox targets devices: a policy attaches to groups
// rather than to individual endpoints, and a device inherits its scan interval
// and OS-update behaviour from the group it sits in. That makes this the
// foundation resource — policies and maintenance windows both reference it.
package server_group

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/sweetgreen/terraform-provider-automox/internal/client"
)

const ResourceName = "automox_server_group"

var (
	_ resource.Resource                = &serverGroupResource{}
	_ resource.ResourceWithConfigure   = &serverGroupResource{}
	_ resource.ResourceWithImportState = &serverGroupResource{}
)

// New returns the resource constructor for the provider registry.
func New() resource.Resource {
	return &serverGroupResource{}
}

type serverGroupResource struct {
	client *client.Client
}

func (r *serverGroupResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = ResourceName
}

func (r *serverGroupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *serverGroupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An Automox server group. Devices inherit their scan interval and " +
			"OS-update behaviour from the group they belong to, and policies target groups rather " +
			"than individual devices.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Server group ID.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"uuid": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "Server group UUID. Required by `automox_scheduled_window`, " +
					"which identifies groups by UUID rather than by ID.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Server group name.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"refresh_interval": schema.Int64Attribute{
				Required: true,
				MarkdownDescription: "How often devices in this group scan for updates, in minutes. " +
					"Automox requires between 240 (4 hours) and 1440 (24 hours).",
				Validators: []validator.Int64{
					int64validator.Between(240, 1440),
				},
			},
			"parent_server_group_id": schema.Int64Attribute{
				Required: true,
				MarkdownDescription: "ID of the parent group. Use the organization's default group " +
					"to create a top-level group; the default group is the one whose parent is itself.",
			},
			"ui_color": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Colour used for this group in the Automox console, as `#RRGGBB`.",
				Validators: []validator.String{
					stringvalidator.RegexMatches(hexColour, "must be a hex colour such as #059F1D"),
				},
			},
			"notes": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Free-text notes.",
			},
			"enable_os_auto_update": schema.BoolAttribute{
				Optional: true,
				MarkdownDescription: "Whether devices in this group use the operating system's own " +
					"automatic updates.\n\n" +
					"Leaving this unset is **not** the same as `false`: unset means each device keeps " +
					"whatever it is already configured to do, while `false` actively disables OS " +
					"auto-update on every device in the group.",
			},
			"enable_wsus": schema.BoolAttribute{
				Optional: true,
				MarkdownDescription: "Whether Windows devices in this group patch from a WSUS server " +
					"rather than from Windows Update.\n\n" +
					"As with `enable_os_auto_update`, unset and `false` differ: unset leaves each " +
					"device's existing behaviour alone, `false` forces Windows Update.",
			},
			"wsus_server": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "WSUS server URL, for example `https://wsus.example.com:8530`. " +
					"Used with `enable_wsus`.",
			},
			"policies": schema.ListAttribute{
				Optional:    true,
				Computed:    true,
				ElementType: types.Int64Type,
				MarkdownDescription: "IDs of policies attached to this group.\n\n" +
					"This relationship has two sides: setting `server_groups` on an " +
					"`automox_policy` also adds that policy here. The attribute is therefore " +
					"computed as well as optional, so a group whose configuration does not " +
					"mention policies accepts whatever Automox reports rather than trying to " +
					"detach policies attached from the other side.",
			},
			"organization_id": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Organization this group belongs to.",
				PlanModifiers: []planmodifier.Int64{
					int64planmodifier.UseStateForUnknown(),
				},
			},
			"server_count": schema.Int64Attribute{
				Computed: true,
				MarkdownDescription: "Number of devices currently in this group. Changes as devices " +
					"enroll and move, independently of this configuration.",
			},
		},
	}
}

// ImportState accepts the numeric server group ID. The organization comes from
// provider configuration, so it is not part of the import ID.
//
// The ID is parsed here rather than passed through as a string, because the
// attribute is an Int64 and a non-numeric import would otherwise fail deep in
// state decoding with a message that does not mention importing.
func (r *serverGroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id, err := parseImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			fmt.Sprintf("Expected a numeric Automox server group ID, got %q: %s.\n\n"+
				"The ID appears in the Automox console URL for the group, and in the "+
				"automox_server_groups data source.", req.ID, err),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}
