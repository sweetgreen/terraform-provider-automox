// Package policy implements the automox_policy resource.
//
// A policy is what actually runs on an endpoint: it installs patches, or executes
// a worklet script, or deploys required software, on a schedule, against the
// devices in its server groups. It is the highest-consequence object this
// provider manages, which is why the schedule encoding and the create path are
// treated as carefully as they are.
package policy

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
	"github.com/sweetgreen/terraform-provider-automox/internal/schedule"
)

const ResourceName = "automox_policy"

var (
	_ resource.Resource                   = &policyResource{}
	_ resource.ResourceWithConfigure      = &policyResource{}
	_ resource.ResourceWithImportState    = &policyResource{}
	_ resource.ResourceWithValidateConfig = &policyResource{}
)

func New() resource.Resource {
	return &policyResource{}
}

type policyResource struct {
	client *client.Client
}

func (r *policyResource) Metadata(_ context.Context, _ resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = ResourceName
}

func (r *policyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *policyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "An Automox policy: a patch policy, a worklet, or a required-software " +
			"deployment, run on a schedule against the devices in its server groups.",
		Attributes: map[string]schema.Attribute{
			"id": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Policy ID.",
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"uuid": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "Policy UUID. Automox does not return one for " +
					"`required_software` policies, where this stays null.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Policy name. **Must be unique within the organization.** " +
					"Automox returns no identifier when a policy is created, so the provider finds " +
					"the new policy by name; a duplicate name makes that lookup ambiguous and the " +
					"apply fails rather than guessing.",
				Validators: []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"policy_type": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "One of `patch`, `custom` (a worklet), or `required_software`. " +
					"Changing this replaces the policy.",
				Validators: []validator.String{
					stringvalidator.OneOf(TypePatch, TypeWorklet, TypeRequiredSoftware),
				},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"notes": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Free-text notes. Automox requires this even though it is descriptive.",
			},
			"server_groups": schema.ListAttribute{
				Required:            true,
				ElementType:         types.Int64Type,
				MarkdownDescription: "IDs of the server groups this policy targets.",
			},

			"schedule_time": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Time of day the policy runs, as `HH:MM` on a 24-hour clock. " +
					"Interpreted in each device's local time unless `use_scheduled_timezone` is set.",
				Validators: []validator.String{
					stringvalidator.RegexMatches(clockTime, "must be a 24-hour time such as 03:00 or 14:30"),
				},
			},
			"schedule_days": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Days of the week the policy runs, as Automox's encoded bitmask. " +
					"Prefer `schedule_days_of_week`, which is readable; this is set automatically from " +
					"it. `0` means the policy never runs on a day-of-week schedule.\n\n" +
					"The encoding is 1-indexed with bit 0 unused: bit 1 is Monday through bit 7 " +
					"Sunday, so `254` is every day.",
				Validators: []validator.Int64{int64validator.Between(0, schedule.AllDays)},
			},
			"schedule_days_of_week": schema.SetAttribute{
				Optional:    true,
				ElementType: types.StringType,
				MarkdownDescription: "Days of the week the policy runs, for example " +
					"`[\"monday\", \"thursday\"]`. Conflicts with `schedule_days`, which is the " +
					"encoded form of the same thing.",
				Validators: []validator.Set{
					setNoneOf(schedule.DayNames()),
				},
			},
			"schedule_months": schema.Int64Attribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Months the policy runs, as Automox's encoded bitmask. Prefer `schedule_months_of_year`.",
				Validators:          []validator.Int64{int64validator.Between(0, schedule.AllMonths)},
			},
			"schedule_months_of_year": schema.SetAttribute{
				Optional:    true,
				ElementType: types.StringType,
				MarkdownDescription: "Months the policy runs, for example `[\"march\", \"september\"]`. " +
					"Conflicts with `schedule_months`.",
				Validators: []validator.Set{
					setNoneOf(schedule.MonthNames()),
				},
			},
			"schedule_weeks_of_month": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Weeks of the month the policy runs, as Automox's encoded " +
					"bitmask, where `62` is every week.\n\n" +
					"There is no named equivalent for this field. The bit order is believed to be " +
					"1-indexed from the first week, but one live policy contradicts that and the " +
					"cause is unresolved, so the provider does not offer a friendly form it cannot " +
					"guarantee. A wrong week order would silently move when patching runs.",
				Validators: []validator.Int64{int64validator.Between(0, schedule.AllWeeks)},
			},
			"use_scheduled_timezone": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "When true, `schedule_time` is interpreted in " +
					"`scheduled_timezone` rather than in each device's local time.",
			},
			"scheduled_timezone": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "UTC offset used when `use_scheduled_timezone` is true, in " +
					"Automox's `UTC+0000` form — not an IANA zone name.",
				Validators: []validator.String{
					stringvalidator.RegexMatches(utcOffset, "must be a UTC offset such as UTC+0000 or UTC-0500"),
				},
			},

			"configuration": configurationSchema(),

			"organization_id": schema.Int64Attribute{
				Computed:      true,
				PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"status": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`active` or `inactive`.",
			},
			"server_count": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Devices this policy currently applies to.",
			},
			"create_time": schema.StringAttribute{
				Computed: true,
			},
			"next_remediation": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "When Automox expects to run this policy next. Null when the " +
					"schedule means it will never run.",
			},
		},
	}
}

func (r *policyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id, err := parseImportID(req.ID)
	if err != nil {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			fmt.Sprintf("Expected a numeric Automox policy ID, got %q: %s.\n\n"+
				"The ID appears in the Automox console URL for the policy.", req.ID, err),
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
}
