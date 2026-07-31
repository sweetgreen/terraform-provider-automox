package datasources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/sweetgreen/terraform-provider-automox/internal/client"
)

const ServerGroupsName = "automox_server_groups"

var (
	_ datasource.DataSource              = &serverGroupsDataSource{}
	_ datasource.DataSourceWithConfigure = &serverGroupsDataSource{}
)

func NewServerGroups() datasource.DataSource { return &serverGroupsDataSource{} }

type serverGroupsDataSource struct {
	client *client.Client
}

type serverGroupsModel struct {
	Name         types.String       `tfsdk:"name"`
	ServerGroups []serverGroupModel `tfsdk:"server_groups"`
}

type serverGroupModel struct {
	ID              types.Int64  `tfsdk:"id"`
	UUID            types.String `tfsdk:"uuid"`
	Name            types.String `tfsdk:"name"`
	Notes           types.String `tfsdk:"notes"`
	RefreshInterval types.Int64  `tfsdk:"refresh_interval"`
	ParentID        types.Int64  `tfsdk:"parent_server_group_id"`
	ServerCount     types.Int64  `tfsdk:"server_count"`
	UIColor         types.String `tfsdk:"ui_color"`
	Policies        types.List   `tfsdk:"policies"`
	IsDefault       types.Bool   `tfsdk:"is_default"`
}

type apiServerGroup struct {
	ID              int64   `json:"id"`
	UUID            string  `json:"uuid"`
	Name            string  `json:"name"`
	Notes           string  `json:"notes"`
	RefreshInterval int64   `json:"refresh_interval"`
	ParentID        int64   `json:"parent_server_group_id"`
	ServerCount     int64   `json:"server_count"`
	UIColor         string  `json:"ui_color"`
	Policies        []int64 `json:"policies"`
}

func (d *serverGroupsDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = ServerGroupsName
}

func (d *serverGroupsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = configure(req, resp)
}

func (d *serverGroupsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Server groups in the configured organization.\n\n" +
			"Commonly used to find the default group to parent a new group to, or to " +
			"look up an existing group's `uuid` for `automox_scheduled_window`.",
		Attributes: map[string]schema.Attribute{
			"name": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Return only the group with this exact name. " +
					"Omit to return every group.",
			},
			"server_groups": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":   schema.Int64Attribute{Computed: true},
						"uuid": schema.StringAttribute{Computed: true},
						"name": schema.StringAttribute{
							Computed: true,
							MarkdownDescription: "The default group's name is an empty " +
								"string; the console displays it as \"Default\".",
						},
						"notes":            schema.StringAttribute{Computed: true},
						"refresh_interval": schema.Int64Attribute{Computed: true},
						"parent_server_group_id": schema.Int64Attribute{
							Computed: true,
						},
						"server_count": schema.Int64Attribute{Computed: true},
						"ui_color":     schema.StringAttribute{Computed: true},
						"policies": schema.ListAttribute{
							Computed:            true,
							ElementType:         types.Int64Type,
							MarkdownDescription: "IDs of the policies attached to this group.",
						},
						"is_default": schema.BoolAttribute{
							Computed: true,
							MarkdownDescription: "True for the organization's default group. " +
								"Automox marks it by making the group its own parent rather " +
								"than with a flag, so the provider derives this.",
						},
					},
				},
			},
		},
	}
}

func (d *serverGroupsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if notConfigured(d.client, resp) {
		return
	}

	var config serverGroupsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var groups []apiServerGroup
	if err := d.client.List(ctx, client.ListOptions{
		Path:     "/servergroups",
		OrgScope: client.OrgScopeQuery,
		Envelope: client.EnvelopeArray,
	}, &groups); err != nil {
		resp.Diagnostics.AddError("Could not list Automox server groups", err.Error())
		return
	}

	// Filtering happens here rather than through a query parameter because
	// /servergroups has no name filter; asking for one would silently return
	// everything.
	wantName, filtering := "", !config.Name.IsNull()
	if filtering {
		wantName = config.Name.ValueString()
	}

	state := serverGroupsModel{Name: config.Name, ServerGroups: []serverGroupModel{}}
	for _, g := range groups {
		if filtering && g.Name != wantName {
			continue
		}

		policies, diags := types.ListValueFrom(ctx, types.Int64Type, g.Policies)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}

		state.ServerGroups = append(state.ServerGroups, serverGroupModel{
			ID:              types.Int64Value(g.ID),
			UUID:            types.StringValue(g.UUID),
			Name:            types.StringValue(g.Name),
			Notes:           types.StringValue(g.Notes),
			RefreshInterval: types.Int64Value(g.RefreshInterval),
			ParentID:        types.Int64Value(g.ParentID),
			ServerCount:     types.Int64Value(g.ServerCount),
			UIColor:         types.StringValue(g.UIColor),
			Policies:        policies,
			IsDefault:       types.BoolValue(g.ID == g.ParentID),
		})
	}

	if filtering && len(state.ServerGroups) == 0 {
		resp.Diagnostics.AddError(
			"No Automox server group has that name",
			"No server group in this organization is named "+wantName+". Names are matched "+
				"exactly. The default group's name is the empty string, not \"Default\".",
		)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
