package datasources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/sweetgreen/terraform-provider-automox/internal/client"
)

const OrganizationsName = "automox_organizations"

var (
	_ datasource.DataSource              = &organizationsDataSource{}
	_ datasource.DataSourceWithConfigure = &organizationsDataSource{}
)

func NewOrganizations() datasource.DataSource { return &organizationsDataSource{} }

type organizationsDataSource struct {
	client *client.Client
}

type organizationsModel struct {
	Organizations []organizationModel `tfsdk:"organizations"`
}

type organizationModel struct {
	ID              types.Int64  `tfsdk:"id"`
	UUID            types.String `tfsdk:"uuid"`
	Name            types.String `tfsdk:"name"`
	DeviceCount     types.Int64  `tfsdk:"device_count"`
	DeviceLimit     types.Int64  `tfsdk:"device_limit"`
	SoftDeviceLimit types.Int64  `tfsdk:"soft_device_limit"`
	CreateTime      types.String `tfsdk:"create_time"`
}

// apiOrganization deliberately omits `access_key`.
//
// Automox returns a live organization access key on this endpoint. Anything a
// data source exposes is written to Terraform state in plaintext, and state gets
// committed, copied into CI artefacts, and shared far more freely than a
// credential should be. There is no legitimate configuration use for it -- the
// provider authenticates with an API key the practitioner supplies -- so it is
// dropped at the wire boundary rather than merely hidden behind Sensitive, which
// would still place it in state.
type apiOrganization struct {
	ID              int64  `json:"id"`
	UUID            string `json:"uuid"`
	Name            string `json:"name"`
	DeviceCount     int64  `json:"device_count"`
	DeviceLimit     int64  `json:"device_limit"`
	SoftDeviceLimit int64  `json:"soft_device_limit"`
	CreateTime      string `json:"create_time"`
}

func (d *organizationsDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = OrganizationsName
}

func (d *organizationsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = configure(req, resp)
}

func (d *organizationsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Every Automox organization the API key can see.\n\n" +
			"Useful for reading the device count against the licensed limit, and for " +
			"discovering an organization's UUID: some endpoints scope by integer id and " +
			"others by UUID for the same organization.\n\n" +
			"The organization access key Automox returns here is deliberately not " +
			"exposed. Data source attributes are stored in Terraform state in plaintext, " +
			"and that value is a credential.",
		Attributes: map[string]schema.Attribute{
			"organizations": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":   schema.Int64Attribute{Computed: true},
						"uuid": schema.StringAttribute{Computed: true},
						"name": schema.StringAttribute{Computed: true},
						"device_count": schema.Int64Attribute{
							Computed:            true,
							MarkdownDescription: "Devices currently enrolled.",
						},
						"device_limit": schema.Int64Attribute{
							Computed:            true,
							MarkdownDescription: "Licensed device ceiling.",
						},
						"soft_device_limit": schema.Int64Attribute{Computed: true},
						"create_time":       schema.StringAttribute{Computed: true},
					},
				},
			},
		},
	}
}

func (d *organizationsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	if notConfigured(d.client, resp) {
		return
	}

	var orgs []apiOrganization
	// /orgs is not organization-scoped: it lists what the key can reach, and
	// passing ?o= would be asking an organization to list itself.
	if err := d.client.List(ctx, client.ListOptions{
		Path:     "/orgs",
		OrgScope: client.OrgScopeNone,
		Envelope: client.EnvelopeArray,
	}, &orgs); err != nil {
		resp.Diagnostics.AddError("Could not list Automox organizations", err.Error())
		return
	}

	state := organizationsModel{Organizations: make([]organizationModel, 0, len(orgs))}
	for _, o := range orgs {
		state.Organizations = append(state.Organizations, organizationModel{
			ID:              types.Int64Value(o.ID),
			UUID:            types.StringValue(o.UUID),
			Name:            types.StringValue(o.Name),
			DeviceCount:     types.Int64Value(o.DeviceCount),
			DeviceLimit:     types.Int64Value(o.DeviceLimit),
			SoftDeviceLimit: types.Int64Value(o.SoftDeviceLimit),
			CreateTime:      types.StringValue(o.CreateTime),
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
