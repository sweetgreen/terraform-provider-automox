package datasources

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/sweetgreen/terraform-provider-automox/internal/client"
	"github.com/sweetgreen/terraform-provider-automox/internal/services/resources/policy"
)

const PoliciesName = "automox_policies"

var (
	_ datasource.DataSource              = &policiesDataSource{}
	_ datasource.DataSourceWithConfigure = &policiesDataSource{}
)

func NewPolicies() datasource.DataSource { return &policiesDataSource{} }

type policiesDataSource struct {
	client *client.Client
}

type policiesModel struct {
	Name       types.String  `tfsdk:"name"`
	PolicyType types.String  `tfsdk:"policy_type"`
	Policies   []policyModel `tfsdk:"policies"`
}

type policyModel struct {
	ID              types.Int64  `tfsdk:"id"`
	UUID            types.String `tfsdk:"uuid"`
	Name            types.String `tfsdk:"name"`
	PolicyType      types.String `tfsdk:"policy_type"`
	Notes           types.String `tfsdk:"notes"`
	ServerGroups    types.List   `tfsdk:"server_groups"`
	ServerCount     types.Int64  `tfsdk:"server_count"`
	Status          types.String `tfsdk:"status"`
	ScheduleDays    types.Int64  `tfsdk:"schedule_days"`
	ScheduleMonths  types.Int64  `tfsdk:"schedule_months"`
	ScheduleTime    types.String `tfsdk:"schedule_time"`
	NextRemediation types.String `tfsdk:"next_remediation"`
	CreateTime      types.String `tfsdk:"create_time"`
}

// apiPolicyListEntry omits `configuration` on purpose.
//
// The configuration object differs by policy type and holds worklet source code,
// so reproducing it here would mean either a second flat superset schema to keep
// in step with the resource, or dumping scripts into state for every policy in
// the organization. Anything needing configuration should read the specific
// policy through the resource instead.
type apiPolicyListEntry struct {
	ID              int64   `json:"id"`
	UUID            string  `json:"uuid"`
	Name            string  `json:"name"`
	PolicyTypeName  string  `json:"policy_type_name"`
	Notes           string  `json:"notes"`
	ServerGroups    []int64 `json:"server_groups"`
	ServerCount     *int64  `json:"server_count"`
	Status          *string `json:"status"`
	ScheduleDays    int64   `json:"schedule_days"`
	ScheduleMonths  int64   `json:"schedule_months"`
	ScheduleTime    string  `json:"schedule_time"`
	NextRemediation *string `json:"next_remediation"`
	CreateTime      *string `json:"create_time"`
}

func (d *policiesDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = PoliciesName
}

func (d *policiesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = configure(req, resp)
}

func (d *policiesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Policies in the configured organization.\n\n" +
			"Policy `configuration` is not returned. It varies by policy type and " +
			"carries worklet source code, which would then be written to Terraform " +
			"state for every policy in the organization.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Return only the policy with this exact name.",
			},
			"policy_type": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Return only policies of this kind: `patch`, " +
					"`custom` (worklet), or `required_software`.",
				Validators: []validator.String{
					stringvalidator.OneOf(policy.TypePatch, policy.TypeWorklet, policy.TypeRequiredSoftware),
				},
			},
			"policies": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":   schema.Int64Attribute{Computed: true},
						"uuid": schema.StringAttribute{Computed: true},
						"name": schema.StringAttribute{Computed: true},
						"policy_type": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "`patch`, `custom` (worklet), or `required_software`.",
						},
						"notes": schema.StringAttribute{Computed: true},
						"server_groups": schema.ListAttribute{
							Computed:            true,
							ElementType:         types.Int64Type,
							MarkdownDescription: "IDs of the groups this policy targets.",
						},
						"server_count": schema.Int64Attribute{
							Computed:            true,
							MarkdownDescription: "Devices this policy currently applies to.",
						},
						"status": schema.StringAttribute{Computed: true},
						"schedule_days": schema.Int64Attribute{
							Computed: true,
							MarkdownDescription: "Encoded day-of-week bitmask. 0 means the " +
								"policy is never scheduled.",
						},
						"schedule_months":  schema.Int64Attribute{Computed: true},
						"schedule_time":    schema.StringAttribute{Computed: true},
						"next_remediation": schema.StringAttribute{Computed: true},
						"create_time":      schema.StringAttribute{Computed: true},
					},
				},
			},
		},
	}
}

func (d *policiesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if notConfigured(d.client, resp) {
		return
	}

	var config policiesModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var policies []apiPolicyListEntry
	if err := d.client.List(ctx, client.ListOptions{
		Path:     "/policies",
		OrgScope: client.OrgScopeQuery,
		Envelope: client.EnvelopeArray,
	}, &policies); err != nil {
		resp.Diagnostics.AddError("Could not list Automox policies", err.Error())
		return
	}

	state := policiesModel{
		Name:       config.Name,
		PolicyType: config.PolicyType,
		Policies:   []policyModel{},
	}

	for _, p := range policies {
		if !config.Name.IsNull() && p.Name != config.Name.ValueString() {
			continue
		}
		if !config.PolicyType.IsNull() && p.PolicyTypeName != config.PolicyType.ValueString() {
			continue
		}

		groups, diags := types.ListValueFrom(ctx, types.Int64Type, p.ServerGroups)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}

		state.Policies = append(state.Policies, policyModel{
			ID:              types.Int64Value(p.ID),
			UUID:            optionalString(&p.UUID),
			Name:            types.StringValue(p.Name),
			PolicyType:      types.StringValue(p.PolicyTypeName),
			Notes:           types.StringValue(p.Notes),
			ServerGroups:    groups,
			ServerCount:     optionalInt(p.ServerCount),
			Status:          optionalString(p.Status),
			ScheduleDays:    types.Int64Value(p.ScheduleDays),
			ScheduleMonths:  types.Int64Value(p.ScheduleMonths),
			ScheduleTime:    types.StringValue(p.ScheduleTime),
			NextRemediation: optionalString(p.NextRemediation),
			CreateTime:      optionalString(p.CreateTime),
		})
	}

	if !config.Name.IsNull() && len(state.Policies) == 0 {
		resp.Diagnostics.AddError(
			"No Automox policy has that name",
			fmt.Sprintf("No policy in this organization is named %q. Names are matched exactly.",
				config.Name.ValueString()),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// optionalString keeps an absent field null rather than turning it into "",
// which would be indistinguishable from a value the API genuinely returned empty.
func optionalString(s *string) types.String {
	if s == nil || *s == "" {
		return types.StringNull()
	}
	return types.StringValue(*s)
}

func optionalInt(i *int64) types.Int64 {
	if i == nil {
		return types.Int64Null()
	}
	return types.Int64Value(*i)
}
