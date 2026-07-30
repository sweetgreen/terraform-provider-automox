// Package scheduled_window implements the automox_scheduled_window resource.
//
// A scheduled window is a blackout: a period during which Automox will not patch
// the server groups it covers. It is the counterpart to a policy's schedule, and
// the reason it matters operationally is that a window protects a business
// period — a holiday freeze, a store's trading hours — from a policy that would
// otherwise run straight through it.
//
// This resource uses a different scoping convention to every other one here.
// Automox scopes it by organization UUID in the path rather than by the integer
// `?o=` query parameter, and it identifies server groups by UUID rather than by
// the integer ID the group endpoints return. The provider resolves both, so
// configuration only ever references an `automox_server_group` directly.
package scheduled_window

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/sweetgreen/terraform-provider-automox/internal/client"
)

const ResourceName = "automox_scheduled_window"

var (
	_ resource.Resource                   = &windowResource{}
	_ resource.ResourceWithConfigure      = &windowResource{}
	_ resource.ResourceWithImportState    = &windowResource{}
	_ resource.ResourceWithValidateConfig = &windowResource{}
)

func New() resource.Resource {
	return &windowResource{}
}

type windowResource struct {
	client *client.Client
}

func (r *windowResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = ResourceName
}

func (r *windowResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
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

func (r *windowResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A maintenance exclusion window: a period during which Automox will " +
			"not patch the server groups it covers.",
		Attributes: map[string]schema.Attribute{
			"window_uuid": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Window UUID, and the resource identifier.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"window_name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Window name, up to 100 characters.",
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 100),
				},
			},
			"window_description": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Description, up to 500 characters. Automox requires this.",
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 500),
				},
			},
			"window_type": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString(WindowTypeExclude),
				MarkdownDescription: "Window kind. `exclude` is the only value Automox accepts, and " +
					"is the default.\n\n" +
					"Automox's own published example uses `exclusion`, which the API rejects.",
				Validators: []validator.String{
					stringvalidator.OneOf(WindowTypeExclude),
				},
			},
			"recurrence": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "`ONCE` for a single window, or `RECURRING` for one that " +
					"repeats annually. This determines which `rrule` forms are legal and whether " +
					"`duration_minutes` may be set.",
				Validators: []validator.String{
					stringvalidator.OneOf(RecurrenceOnce, RecurrenceRecurring),
				},
			},
			"dtstart": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "When the window starts, as an RFC 3339 timestamp.",
			},
			"rrule": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Recurrence rule. Automox accepts only a narrow subset of " +
					"RFC 5545:\n\n" +
					"- With `recurrence = \"ONCE\"`: `FREQ=DAILY;UNTIL=<YYYYMMDDThhmmssZ>`. " +
					"`COUNT` and `INTERVAL` are rejected.\n" +
					"- With `recurrence = \"RECURRING\"`: `FREQ=YEARLY` with `BYMONTH` and " +
					"`BYDAY`, where `BYDAY` may carry an ordinal such as `+2TU`.\n\n" +
					"Example: `FREQ=YEARLY;BYMONTH=1;BYDAY=+1MO` for the first Monday of January.",
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
				},
			},
			"duration_minutes": schema.Int64Attribute{
				Optional: true,
				MarkdownDescription: "How long the window lasts, in minutes.\n\n" +
					"**Only valid with `recurrence = \"RECURRING\"`.** For a `ONCE` window Automox " +
					"derives the duration from `dtstart` to the `UNTIL` in the rule, and rejects " +
					"the request if this is supplied.",
				Validators: []validator.Int64{
					int64validator.AtLeast(1),
				},
			},
			"group_uuids": schema.SetAttribute{
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				MarkdownDescription: "UUIDs of the server groups this window covers. Use the `uuid` " +
					"attribute of an `automox_server_group`, not its `id` — this endpoint is one of " +
					"the few that identifies groups by UUID.\n\n" +
					"An empty set means the window covers nothing and has no effect.",
			},
			"status": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "`active` or `inactive`. New windows are active.",
				Validators: []validator.String{
					stringvalidator.OneOf("active", "inactive"),
				},
			},
			"org_uuid": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "Organization UUID the window belongs to, resolved from the " +
					"provider's `organization_id`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{Computed: true},
			"updated_at": schema.StringAttribute{Computed: true},
		},
	}
}

// ImportState takes the window UUID. The organization comes from provider
// configuration, as it does for every other resource here.
func (r *windowResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := strings.TrimSpace(req.ID)
	if id == "" {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			"Expected an Automox window UUID, got an empty string.",
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("window_uuid"), id)...)
}
