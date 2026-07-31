package datasources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/sweetgreen/terraform-provider-automox/internal/client"
)

const PolicyStatsName = "automox_policy_stats"

var (
	_ datasource.DataSource              = &policyStatsDataSource{}
	_ datasource.DataSourceWithConfigure = &policyStatsDataSource{}
)

func NewPolicyStats() datasource.DataSource { return &policyStatsDataSource{} }

type policyStatsDataSource struct {
	client *client.Client
}

type policyStatsModel struct {
	PolicyStats []policyStatModel `tfsdk:"policy_stats"`
}

type policyStatModel struct {
	PolicyID     types.Int64  `tfsdk:"policy_id"`
	PolicyName   types.String `tfsdk:"policy_name"`
	PolicyType   types.String `tfsdk:"policy_type"`
	Compliant    types.Int64  `tfsdk:"compliant"`
	NonCompliant types.Int64  `tfsdk:"noncompliant"`
	Pending      types.Int64  `tfsdk:"pending"`
}

type apiPolicyStat struct {
	PolicyID       int64  `json:"policy_id"`
	PolicyName     string `json:"policy_name"`
	PolicyTypeName string `json:"policy_type_name"`
	Compliant      int64  `json:"compliant"`
	NonCompliant   int64  `json:"noncompliant"`
	Pending        int64  `json:"pending"`
}

func (d *policyStatsDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = PolicyStatsName
}

func (d *policyStatsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = configure(req, resp)
}

func (d *policyStatsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Per-policy compliance counts for the configured " +
			"organization.\n\n" +
			"These are point-in-time counts read at plan and apply. They describe the " +
			"fleet rather than configuration, so referring to them from a resource " +
			"argument makes that resource change whenever the fleet does.",
		Attributes: map[string]schema.Attribute{
			"policy_stats": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"policy_id":   schema.Int64Attribute{Computed: true},
						"policy_name": schema.StringAttribute{Computed: true},
						"policy_type": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "`patch`, `custom` (worklet), or `required_software`.",
						},
						"compliant": schema.Int64Attribute{
							Computed:            true,
							MarkdownDescription: "Devices meeting the policy.",
						},
						"noncompliant": schema.Int64Attribute{
							Computed:            true,
							MarkdownDescription: "Devices not meeting it.",
						},
						"pending": schema.Int64Attribute{
							Computed:            true,
							MarkdownDescription: "Devices with remediation outstanding.",
						},
					},
				},
			},
		},
	}
}

func (d *policyStatsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	if notConfigured(d.client, resp) {
		return
	}

	var stats []apiPolicyStat
	if err := d.client.List(ctx, client.ListOptions{
		Path:     "/policystats",
		OrgScope: client.OrgScopeQuery,
		Envelope: client.EnvelopeArray,
	}, &stats); err != nil {
		resp.Diagnostics.AddError("Could not read Automox policy statistics", err.Error())
		return
	}

	state := policyStatsModel{PolicyStats: make([]policyStatModel, 0, len(stats))}
	for _, s := range stats {
		state.PolicyStats = append(state.PolicyStats, policyStatModel{
			PolicyID:     types.Int64Value(s.PolicyID),
			PolicyName:   types.StringValue(s.PolicyName),
			PolicyType:   types.StringValue(s.PolicyTypeName),
			Compliant:    types.Int64Value(s.Compliant),
			NonCompliant: types.Int64Value(s.NonCompliant),
			Pending:      types.Int64Value(s.Pending),
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
