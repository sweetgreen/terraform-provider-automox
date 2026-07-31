package datasources

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/sweetgreen/terraform-provider-automox/internal/client"
)

const DeviceName = "automox_device"

var (
	_ datasource.DataSource              = &deviceDataSource{}
	_ datasource.DataSourceWithConfigure = &deviceDataSource{}
)

func NewDevice() datasource.DataSource { return &deviceDataSource{} }

type deviceDataSource struct {
	client *client.Client
}

// singleDeviceModel embeds the same attributes the list returns, so both data
// sources describe a device identically.
type singleDeviceModel struct {
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

func (d *deviceDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = DeviceName
}

func (d *deviceDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = configure(req, resp)
}

func (d *deviceDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := deviceAttributes()
	// id is the lookup key here rather than a computed result.
	attrs["id"] = schema.Int64Attribute{
		Required:            true,
		MarkdownDescription: "The device's Automox ID.",
	}

	resp.Schema = schema.Schema{
		MarkdownDescription: "A single device by ID.\n\n" +
			"The logged-in user, serial number, and hardware `detail` object are " +
			"deliberately not exposed; see `automox_devices` for why.",
		Attributes: attrs,
	}
}

func (d *deviceDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if notConfigured(d.client, resp) {
		return
	}

	var config singleDeviceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := config.ID.ValueInt64()

	var dev apiDevice
	if err := d.client.Do(ctx, client.Request{
		Method:   "GET",
		Path:     fmt.Sprintf("/servers/%d", id),
		OrgScope: client.OrgScopeQuery,
	}, &dev); err != nil {
		// A missing device is reported rather than yielding an empty object that
		// configuration would then read zero values out of.
		resp.Diagnostics.AddError(
			"Could not read the Automox device",
			fmt.Sprintf("Reading device %d failed: %s", id, err),
		)
		return
	}

	model, diags := flattenDevice(ctx, dev)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, singleDeviceModel(model))...)
}

// flattenDevice converts one API device into the shared model.
func flattenDevice(ctx context.Context, dev apiDevice) (deviceModel, diag.Diagnostics) {
	var diags diag.Diagnostics

	ips, ipDiags := types.ListValueFrom(ctx, types.StringType, dev.IPAddrs)
	diags.Append(ipDiags...)

	tags, tagDiags := types.ListValueFrom(ctx, types.StringType, dev.Tags)
	diags.Append(tagDiags...)

	return deviceModel{
		ID:                 types.Int64Value(dev.ID),
		UUID:               types.StringValue(dev.UUID),
		Name:               types.StringValue(dev.Name),
		DisplayName:        types.StringValue(dev.DisplayName),
		CustomName:         optionalString(&dev.CustomName),
		ServerGroupID:      types.Int64Value(dev.ServerGroupID),
		OSFamily:           types.StringValue(dev.OSFamily),
		OSName:             types.StringValue(dev.OSName),
		OSVersion:          types.StringValue(dev.OSVersion),
		OrganizationalUnit: optionalString(&dev.OrganizationalUnit),
		IPAddrs:            ips,
		Tags:               tags,
		Connected:          types.BoolValue(dev.Connected),
		Compliant:          types.BoolValue(dev.Compliant),
		NeedsReboot:        types.BoolValue(dev.NeedsReboot),
		NeedsAttention:     types.BoolValue(dev.NeedsAttention),
		PendingPatches:     types.Int64Value(dev.PendingPatches),
		AgentVersion:       types.StringValue(dev.AgentVersion),
		LastRefreshTime:    optionalString(&dev.LastRefreshTime),
		CreateTime:         optionalString(&dev.CreateTime),
	}, diags
}
