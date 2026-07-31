package datasources

import (
	"context"
	"net/url"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/sweetgreen/terraform-provider-automox/internal/client"
)

const DevicesName = "automox_devices"

var (
	_ datasource.DataSource              = &devicesDataSource{}
	_ datasource.DataSourceWithConfigure = &devicesDataSource{}
)

func NewDevices() datasource.DataSource { return &devicesDataSource{} }

type devicesDataSource struct {
	client *client.Client
}

type devicesModel struct {
	GroupID   types.Int64   `tfsdk:"group_id"`
	OSFamily  types.String  `tfsdk:"os_family"`
	Connected types.Bool    `tfsdk:"connected"`
	Tag       types.String  `tfsdk:"tag"`
	Devices   []deviceModel `tfsdk:"devices"`
}

type deviceModel struct {
	ID                 types.Int64  `tfsdk:"id"`
	UUID               types.String `tfsdk:"uuid"`
	Name               types.String `tfsdk:"name"`
	DisplayName        types.String `tfsdk:"display_name"`
	CustomName         types.String `tfsdk:"custom_name"`
	ServerGroupID      types.Int64  `tfsdk:"server_group_id"`
	OSFamily           types.String `tfsdk:"os_family"`
	OSName             types.String `tfsdk:"os_name"`
	OSVersion          types.String `tfsdk:"os_version"`
	OrganizationalUnit types.String `tfsdk:"organizational_unit"`
	IPAddrs            types.List   `tfsdk:"ip_addrs"`
	Tags               types.List   `tfsdk:"tags"`
	Connected          types.Bool   `tfsdk:"connected"`
	Compliant          types.Bool   `tfsdk:"compliant"`
	NeedsReboot        types.Bool   `tfsdk:"needs_reboot"`
	NeedsAttention     types.Bool   `tfsdk:"needs_attention"`
	PendingPatches     types.Int64  `tfsdk:"pending_patches"`
	AgentVersion       types.String `tfsdk:"agent_version"`
	LastRefreshTime    types.String `tfsdk:"last_refresh_time"`
	CreateTime         types.String `tfsdk:"create_time"`
}

// apiDevice is the subset of the device record this provider exposes.
//
// Automox returns considerably more, and the omissions are deliberate rather
// than incidental. `last_logged_in_user`, `serial_number`, and the `detail`
// object -- which carries LAST_USER_LOGON, SERIAL, SERVICETAG, FQDNS,
// DISTINGUISHED_NAME and MDM server names -- together form an identity and asset
// fingerprint of a specific employee's laptop. Data source attributes are
// written to Terraform state in plaintext, so reading the fleet would otherwise
// deposit that for every device into a file that gets committed, copied into CI
// artefacts, and shared.
//
// The line drawn is: expose what configuration can act on. `ip_addrs` and
// `organizational_unit` are here because policy `device_filters` match on them.
// Nothing above is needed to target a group or a policy, which is what
// configuration does.
type apiDevice struct {
	ID                 int64    `json:"id"`
	UUID               string   `json:"uuid"`
	Name               string   `json:"name"`
	DisplayName        string   `json:"display_name"`
	CustomName         string   `json:"custom_name"`
	ServerGroupID      int64    `json:"server_group_id"`
	OSFamily           string   `json:"os_family"`
	OSName             string   `json:"os_name"`
	OSVersion          string   `json:"os_version"`
	OrganizationalUnit string   `json:"organizational_unit"`
	IPAddrs            []string `json:"ip_addrs"`
	Tags               []string `json:"tags"`
	Connected          bool     `json:"connected"`
	Compliant          bool     `json:"compliant"`
	NeedsReboot        bool     `json:"needs_reboot"`
	NeedsAttention     bool     `json:"needs_attention"`
	PendingPatches     int64    `json:"pending_patches"`
	AgentVersion       string   `json:"agent_version"`
	LastRefreshTime    string   `json:"last_refresh_time"`
	CreateTime         string   `json:"create_time"`
}

func (d *devicesDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = DevicesName
}

func (d *devicesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = configure(req, resp)
}

func (d *devicesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Devices enrolled in the configured organization.\n\n" +
			"Reading the whole fleet is a large read and puts a record for every device " +
			"into Terraform state, so filter where you can. `group_id` is applied by " +
			"the API; the rest are applied to the results.\n\n" +
			"The logged-in user, serial number, and the hardware `detail` object are " +
			"deliberately not exposed: together they identify a specific person's " +
			"machine, and state is stored in plaintext. Attributes that policy " +
			"`device_filters` can match on — `ip_addrs`, `organizational_unit`, " +
			"`tags` — are included.",
		Attributes: map[string]schema.Attribute{
			"group_id": schema.Int64Attribute{
				Optional: true,
				MarkdownDescription: "Only devices in this server group. Applied by the " +
					"API, so it also reduces how much is fetched.",
			},
			"os_family": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Only devices with this OS family, such as `Windows` or `Mac`.",
			},
			"connected": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Only devices whose agent is currently connected.",
			},
			"tag": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Only devices carrying this tag.",
			},
			"devices": schema.ListNestedAttribute{
				Computed:     true,
				NestedObject: schema.NestedAttributeObject{Attributes: deviceAttributes()},
			},
		},
	}
}

func deviceAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id":   schema.Int64Attribute{Computed: true},
		"uuid": schema.StringAttribute{Computed: true},
		"name": schema.StringAttribute{Computed: true},
		"display_name": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "The custom name if one is set, otherwise the hostname.",
		},
		"custom_name":     schema.StringAttribute{Computed: true},
		"server_group_id": schema.Int64Attribute{Computed: true},
		"os_family": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "`Windows`, `Mac`, or `Linux`.",
		},
		"os_name":    schema.StringAttribute{Computed: true},
		"os_version": schema.StringAttribute{Computed: true},
		"organizational_unit": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Matched by policy `device_filters`.",
		},
		"ip_addrs": schema.ListAttribute{
			Computed:            true,
			ElementType:         types.StringType,
			MarkdownDescription: "Public addresses. Matched by policy `device_filters`.",
		},
		"tags": schema.ListAttribute{
			Computed:    true,
			ElementType: types.StringType,
		},
		"connected":       schema.BoolAttribute{Computed: true},
		"compliant":       schema.BoolAttribute{Computed: true},
		"needs_reboot":    schema.BoolAttribute{Computed: true},
		"needs_attention": schema.BoolAttribute{Computed: true},
		"pending_patches": schema.Int64Attribute{Computed: true},
		"agent_version":   schema.StringAttribute{Computed: true},
		"last_refresh_time": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "When the agent last checked in.",
		},
		"create_time": schema.StringAttribute{Computed: true},
	}
}

func (d *devicesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if notConfigured(d.client, resp) {
		return
	}

	var config devicesModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	query := url.Values{}
	if !config.GroupID.IsNull() {
		// Applied by the API. The other filters have no query parameter, so they
		// are applied to the results below.
		query.Set("groupId", strconv.FormatInt(config.GroupID.ValueInt64(), 10))
	}

	var devices []apiDevice
	if err := d.client.List(ctx, client.ListOptions{
		Path:     "/servers",
		Query:    query,
		OrgScope: client.OrgScopeQuery,
		Envelope: client.EnvelopeArray,
	}, &devices); err != nil {
		resp.Diagnostics.AddError("Could not list Automox devices", err.Error())
		return
	}

	state := devicesModel{
		GroupID:   config.GroupID,
		OSFamily:  config.OSFamily,
		Connected: config.Connected,
		Tag:       config.Tag,
		Devices:   []deviceModel{},
	}

	for _, dev := range devices {
		if !config.OSFamily.IsNull() && dev.OSFamily != config.OSFamily.ValueString() {
			continue
		}
		if !config.Connected.IsNull() && dev.Connected != config.Connected.ValueBool() {
			continue
		}
		if !config.Tag.IsNull() && !hasTag(dev.Tags, config.Tag.ValueString()) {
			continue
		}

		model, diags := flattenDevice(ctx, dev)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		state.Devices = append(state.Devices, model)
	}

	// An empty result is not an error here: "no devices match" is a legitimate
	// answer for a filter over a fleet, unlike a named lookup that finds nothing.
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func hasTag(tags []string, want string) bool {
	for _, t := range tags {
		if t == want {
			return true
		}
	}
	return false
}
