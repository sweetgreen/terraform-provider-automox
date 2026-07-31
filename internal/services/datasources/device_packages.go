package datasources

import (
	"context"
	"fmt"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/sweetgreen/terraform-provider-automox/internal/client"
)

const DevicePackagesName = "automox_device_packages"

var (
	_ datasource.DataSource              = &devicePackagesDataSource{}
	_ datasource.DataSourceWithConfigure = &devicePackagesDataSource{}
)

func NewDevicePackages() datasource.DataSource { return &devicePackagesDataSource{} }

type devicePackagesDataSource struct {
	client *client.Client
}

type devicePackagesModel struct {
	DeviceID  types.Int64          `tfsdk:"device_id"`
	Installed types.Bool           `tfsdk:"installed"`
	Packages  []devicePackageModel `tfsdk:"packages"`
}

type devicePackageModel struct {
	ID           types.Int64   `tfsdk:"id"`
	Name         types.String  `tfsdk:"name"`
	DisplayName  types.String  `tfsdk:"display_name"`
	Version      types.String  `tfsdk:"version"`
	Repo         types.String  `tfsdk:"repo"`
	Installed    types.Bool    `tfsdk:"installed"`
	Ignored      types.Bool    `tfsdk:"ignored"`
	GroupIgnored types.Bool    `tfsdk:"group_ignored"`
	Severity     types.String  `tfsdk:"severity"`
	CVEScore     types.Float64 `tfsdk:"cve_score"`
	CVEs         types.List    `tfsdk:"cves"`
	RequiresRebt types.Bool    `tfsdk:"requires_reboot"`
	IsManaged    types.Bool    `tfsdk:"is_managed"`
	CreateTime   types.String  `tfsdk:"create_time"`
}

type apiDevicePackage struct {
	ID           int64   `json:"id"`
	Name         string  `json:"name"`
	DisplayName  string  `json:"display_name"`
	Version      string  `json:"version"`
	Repo         string  `json:"repo"`
	Installed    bool    `json:"installed"`
	Ignored      bool    `json:"ignored"`
	GroupIgnored bool    `json:"group_ignored"`
	Severity     *string `json:"severity"`
	// cve_score arrives as a quoted decimal string ("9.6"), not a JSON number,
	// so it is decoded as a string and parsed. Exposing it as a float is what
	// makes `cve_score >= 7` expressible in configuration.
	CVEScore       *string  `json:"cve_score"`
	CVEs           []string `json:"cves"`
	RequiresReboot bool     `json:"requires_reboot"`
	IsManaged      bool     `json:"is_managed"`
	CreateTime     string   `json:"create_time"`
}

func (d *devicePackagesDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = DevicePackagesName
}

func (d *devicePackagesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = configure(req, resp)
}

func (d *devicePackagesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Software packages and available patches for one device.\n\n" +
			"This is per-device by necessity — Automox exposes no fleet-wide package " +
			"listing — so reading it across many devices means one request each. " +
			"Prefer `automox_policy_stats` for fleet compliance.",
		Attributes: map[string]schema.Attribute{
			"device_id": schema.Int64Attribute{
				Required:            true,
				MarkdownDescription: "The device whose packages to read.",
			},
			"installed": schema.BoolAttribute{
				Optional: true,
				MarkdownDescription: "Filter to installed packages (`true`) or to " +
					"outstanding ones (`false`). Omit for both.",
			},
			"packages": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":           schema.Int64Attribute{Computed: true},
						"name":         schema.StringAttribute{Computed: true},
						"display_name": schema.StringAttribute{Computed: true},
						"version":      schema.StringAttribute{Computed: true},
						"repo": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Where the package comes from, such as `Apple`.",
						},
						"installed": schema.BoolAttribute{Computed: true},
						"ignored": schema.BoolAttribute{
							Computed:            true,
							MarkdownDescription: "Ignored on this device specifically.",
						},
						"group_ignored": schema.BoolAttribute{
							Computed:            true,
							MarkdownDescription: "Ignored for the device's whole server group.",
						},
						"severity": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Automox severity, such as `critical` or `no_known_cves`.",
						},
						"cve_score": schema.Float64Attribute{
							Computed:            true,
							MarkdownDescription: "Highest CVSS score among this package's CVEs, when scored.",
						},
						"cves": schema.ListAttribute{
							Computed:    true,
							ElementType: types.StringType,
						},
						"requires_reboot": schema.BoolAttribute{Computed: true},
						"is_managed":      schema.BoolAttribute{Computed: true},
						"create_time":     schema.StringAttribute{Computed: true},
					},
				},
			},
		},
	}
}

func (d *devicePackagesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if notConfigured(d.client, resp) {
		return
	}

	var config devicePackagesModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	deviceID := config.DeviceID.ValueInt64()

	var packages []apiDevicePackage
	if err := d.client.List(ctx, client.ListOptions{
		Path:     fmt.Sprintf("/servers/%d/packages", deviceID),
		OrgScope: client.OrgScopeQuery,
		Envelope: client.EnvelopeArray,
	}, &packages); err != nil {
		resp.Diagnostics.AddError(
			"Could not list packages for the Automox device",
			fmt.Sprintf("Reading packages for device %d failed: %s", deviceID, err),
		)
		return
	}

	state := devicePackagesModel{
		DeviceID:  config.DeviceID,
		Installed: config.Installed,
		Packages:  []devicePackageModel{},
	}

	for _, p := range packages {
		if !config.Installed.IsNull() && p.Installed != config.Installed.ValueBool() {
			continue
		}

		cves, diags := types.ListValueFrom(ctx, types.StringType, p.CVEs)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}

		state.Packages = append(state.Packages, devicePackageModel{
			ID:           types.Int64Value(p.ID),
			Name:         types.StringValue(p.Name),
			DisplayName:  types.StringValue(p.DisplayName),
			Version:      types.StringValue(p.Version),
			Repo:         optionalString(&p.Repo),
			Installed:    types.BoolValue(p.Installed),
			Ignored:      types.BoolValue(p.Ignored),
			GroupIgnored: types.BoolValue(p.GroupIgnored),
			Severity:     optionalString(p.Severity),
			CVEScore:     parseCVEScore(p, resp),
			CVEs:         cves,
			RequiresRebt: types.BoolValue(p.RequiresReboot),
			IsManaged:    types.BoolValue(p.IsManaged),
			CreateTime:   optionalString(&p.CreateTime),
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// parseCVEScore converts Automox's quoted score into a number.
//
// An unparseable score is reported as a warning and left null rather than
// failing the read: one malformed score should not make an entire device's
// package list unreadable, but it must not pass unnoticed either, since silently
// dropping it would understate a package's severity.
func parseCVEScore(p apiDevicePackage, resp *datasource.ReadResponse) types.Float64 {
	if p.CVEScore == nil || *p.CVEScore == "" {
		return types.Float64Null()
	}

	score, err := strconv.ParseFloat(*p.CVEScore, 64)
	if err != nil {
		resp.Diagnostics.AddWarning(
			"Unreadable CVE score",
			fmt.Sprintf("Package %q (id %d) reported cve_score %q, which is not a number. "+
				"It is reported as unset; the severity attribute is unaffected.",
				p.DisplayName, p.ID, *p.CVEScore),
		)
		return types.Float64Null()
	}
	return types.Float64Value(score)
}
