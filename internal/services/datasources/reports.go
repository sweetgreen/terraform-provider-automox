package datasources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/sweetgreen/terraform-provider-automox/internal/client"
)

// Automox's reports diverge from the rest of the API in two ways that matter
// here, both established live rather than taken from the document:
//
//  1. A report is a single JSON object, not the array the document describes.
//     One wrapper key names the report -- "nonCompliant" for needs-attention,
//     "prepatch" for prepatch -- and holds severity counts plus a devices list.
//     So these are read with Do rather than List: paging an object would return
//     the same object on every page until the page ceiling tripped.
//
//  2. Device records inside a report are camelCase (customName, groupId,
//     needsReboot, lastRefreshTime) where /servers is snake_case for the same
//     fields -- and os_family stays snake_case in both, so the report is not
//     even internally consistent. The JSON tags below follow the report.
//
//     What matters is the underscore, not the capitalisation: encoding/json
//     falls back to a case-insensitive match, so a `groupid` tag still binds
//     groupId, but `group_id` silently binds nothing and leaves the field at
//     zero. Measured, not assumed. A wrong tag here therefore produces a report
//     that decodes cleanly and is quietly empty, which is why the acceptance
//     test asserts these fields are populated rather than merely present.
const (
	NeedsAttentionReportName = "automox_needs_attention_report"
	PrepatchReportName       = "automox_prepatch_report"
)

var (
	_ datasource.DataSource              = &needsAttentionDataSource{}
	_ datasource.DataSourceWithConfigure = &needsAttentionDataSource{}
	_ datasource.DataSource              = &prepatchDataSource{}
	_ datasource.DataSourceWithConfigure = &prepatchDataSource{}
)

func NewNeedsAttentionReport() datasource.DataSource { return &needsAttentionDataSource{} }
func NewPrepatchReport() datasource.DataSource       { return &prepatchDataSource{} }

type needsAttentionDataSource struct{ client *client.Client }
type prepatchDataSource struct{ client *client.Client }

// apiSeverityCounts is the breakdown both reports carry.
type apiSeverityCounts struct {
	Total       int64 `json:"total"`
	Critical    int64 `json:"critical"`
	High        int64 `json:"high"`
	Medium      int64 `json:"medium"`
	Low         int64 `json:"low"`
	None        int64 `json:"none"`
	Unknown     int64 `json:"unknown"`
	NoKnownCVEs int64 `json:"no_known_cves"`
}

type severityModel struct {
	Total       types.Int64 `tfsdk:"total"`
	Critical    types.Int64 `tfsdk:"critical"`
	High        types.Int64 `tfsdk:"high"`
	Medium      types.Int64 `tfsdk:"medium"`
	Low         types.Int64 `tfsdk:"low"`
	None        types.Int64 `tfsdk:"none"`
	Unknown     types.Int64 `tfsdk:"unknown"`
	NoKnownCVEs types.Int64 `tfsdk:"no_known_cves"`
}

func flattenSeverity(c apiSeverityCounts) severityModel {
	return severityModel{
		Total:       types.Int64Value(c.Total),
		Critical:    types.Int64Value(c.Critical),
		High:        types.Int64Value(c.High),
		Medium:      types.Int64Value(c.Medium),
		Low:         types.Int64Value(c.Low),
		None:        types.Int64Value(c.None),
		Unknown:     types.Int64Value(c.Unknown),
		NoKnownCVEs: types.Int64Value(c.NoKnownCVEs),
	}
}

func severityAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"total": schema.Int64Attribute{
			Computed:            true,
			MarkdownDescription: "Devices covered by the report.",
		},
		"critical": schema.Int64Attribute{Computed: true},
		"high":     schema.Int64Attribute{Computed: true},
		"medium":   schema.Int64Attribute{Computed: true},
		"low":      schema.Int64Attribute{Computed: true},
		"none":     schema.Int64Attribute{Computed: true},
		"unknown":  schema.Int64Attribute{Computed: true},
		"no_known_cves": schema.Int64Attribute{
			Computed:            true,
			MarkdownDescription: "Devices whose outstanding patches carry no known CVEs.",
		},
	}
}

// reportDeviceAttributes are the device columns common to both reports.
func reportDeviceAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id":           schema.Int64Attribute{Computed: true},
		"name":         schema.StringAttribute{Computed: true},
		"os_family":    schema.StringAttribute{Computed: true},
		"compliant":    schema.BoolAttribute{Computed: true},
		"connected":    schema.BoolAttribute{Computed: true},
		"needs_reboot": schema.BoolAttribute{Computed: true},
	}
}

// --- needs attention ---

type needsAttentionModel struct {
	severityModel
	Devices []needsAttentionDeviceModel `tfsdk:"devices"`
}

type needsAttentionDeviceModel struct {
	ID                     types.Int64                 `tfsdk:"id"`
	Name                   types.String                `tfsdk:"name"`
	CustomName             types.String                `tfsdk:"custom_name"`
	OSFamily               types.String                `tfsdk:"os_family"`
	GroupID                types.Int64                 `tfsdk:"group_id"`
	Compliant              types.Bool                  `tfsdk:"compliant"`
	Connected              types.Bool                  `tfsdk:"connected"`
	NeedsReboot            types.Bool                  `tfsdk:"needs_reboot"`
	IsCompatible           types.Bool                  `tfsdk:"is_compatible"`
	DisconnectedThirtyDays types.Bool                  `tfsdk:"disconnected_thirty_days"`
	LastRefreshTime        types.String                `tfsdk:"last_refresh_time"`
	Policies               []needsAttentionPolicyModel `tfsdk:"policies"`
}

type needsAttentionPolicyModel struct {
	ID            types.Int64  `tfsdk:"id"`
	Name          types.String `tfsdk:"name"`
	Type          types.String `tfsdk:"type"`
	Severity      types.String `tfsdk:"severity"`
	ReasonForFail types.String `tfsdk:"reason_for_fail"`
}

type apiNeedsAttentionReport struct {
	NonCompliant struct {
		apiSeverityCounts
		Devices []struct {
			ID                     int64   `json:"id"`
			Name                   string  `json:"name"`
			CustomName             *string `json:"customName"`
			OSFamily               string  `json:"os_family"`
			GroupID                int64   `json:"groupId"`
			Compliant              bool    `json:"compliant"`
			Connected              bool    `json:"connected"`
			NeedsReboot            bool    `json:"needsReboot"`
			IsCompatible           bool    `json:"isCompatible"`
			DisconnectedThirtyDays bool    `json:"disconnectedThirtyDays"`
			LastRefreshTime        *string `json:"lastRefreshTime"`
			Policies               []struct {
				ID            int64   `json:"id"`
				Name          string  `json:"name"`
				Type          string  `json:"type"`
				Severity      *string `json:"severity"`
				ReasonForFail *string `json:"reasonForFail"`
			} `json:"policies"`
		} `json:"devices"`
	} `json:"nonCompliant"`
}

func (d *needsAttentionDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = NeedsAttentionReportName
}

func (d *needsAttentionDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = configure(req, resp)
}

func (d *needsAttentionDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	device := reportDeviceAttributes()
	device["custom_name"] = schema.StringAttribute{Computed: true}
	device["group_id"] = schema.Int64Attribute{Computed: true}
	device["is_compatible"] = schema.BoolAttribute{
		Computed:            true,
		MarkdownDescription: "False when the Automox agent cannot manage this device.",
	}
	device["disconnected_thirty_days"] = schema.BoolAttribute{Computed: true}
	device["last_refresh_time"] = schema.StringAttribute{Computed: true}
	device["policies"] = schema.ListNestedAttribute{
		Computed:            true,
		MarkdownDescription: "The policies this device is failing, and why.",
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"id":       schema.Int64Attribute{Computed: true},
				"name":     schema.StringAttribute{Computed: true},
				"type":     schema.StringAttribute{Computed: true},
				"severity": schema.StringAttribute{Computed: true},
				"reason_for_fail": schema.StringAttribute{
					Computed:            true,
					MarkdownDescription: "Automox's stated reason the device is non-compliant.",
				},
			},
		},
	}

	attrs := severityAttributes()
	attrs["devices"] = schema.ListNestedAttribute{
		Computed:            true,
		MarkdownDescription: "Devices Automox reports as needing attention.",
		NestedObject:        schema.NestedAttributeObject{Attributes: device},
	}

	resp.Schema = schema.Schema{
		MarkdownDescription: "Automox's needs-attention report: non-compliant devices, " +
			"with the policies they are failing and why.\n\n" +
			"This describes the fleet at a moment, not configuration. Referring to it " +
			"from a resource argument makes that resource change whenever the fleet does.",
		Attributes: attrs,
	}
}

func (d *needsAttentionDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	if notConfigured(d.client, resp) {
		return
	}

	var report apiNeedsAttentionReport
	if err := d.client.Do(ctx, client.Request{
		Method:   "GET",
		Path:     "/reports/needs-attention",
		OrgScope: client.OrgScopeQuery,
	}, &report); err != nil {
		resp.Diagnostics.AddError("Could not read the Automox needs-attention report", err.Error())
		return
	}

	r := report.NonCompliant
	state := needsAttentionModel{
		severityModel: flattenSeverity(r.apiSeverityCounts),
		Devices:       make([]needsAttentionDeviceModel, 0, len(r.Devices)),
	}

	for _, dev := range r.Devices {
		policies := make([]needsAttentionPolicyModel, 0, len(dev.Policies))
		for _, p := range dev.Policies {
			policies = append(policies, needsAttentionPolicyModel{
				ID:            types.Int64Value(p.ID),
				Name:          types.StringValue(p.Name),
				Type:          types.StringValue(p.Type),
				Severity:      optionalString(p.Severity),
				ReasonForFail: optionalString(p.ReasonForFail),
			})
		}

		state.Devices = append(state.Devices, needsAttentionDeviceModel{
			ID:                     types.Int64Value(dev.ID),
			Name:                   types.StringValue(dev.Name),
			CustomName:             optionalString(dev.CustomName),
			OSFamily:               types.StringValue(dev.OSFamily),
			GroupID:                types.Int64Value(dev.GroupID),
			Compliant:              types.BoolValue(dev.Compliant),
			Connected:              types.BoolValue(dev.Connected),
			NeedsReboot:            types.BoolValue(dev.NeedsReboot),
			IsCompatible:           types.BoolValue(dev.IsCompatible),
			DisconnectedThirtyDays: types.BoolValue(dev.DisconnectedThirtyDays),
			LastRefreshTime:        optionalString(dev.LastRefreshTime),
			Policies:               policies,
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// --- prepatch ---

type prepatchModel struct {
	severityModel
	NeedsAttention types.Int64           `tfsdk:"needs_attention"`
	Devices        []prepatchDeviceModel `tfsdk:"devices"`
}

type prepatchDeviceModel struct {
	ID          types.Int64          `tfsdk:"id"`
	Name        types.String         `tfsdk:"name"`
	OSFamily    types.String         `tfsdk:"os_family"`
	Group       types.String         `tfsdk:"group"`
	Compliant   types.Bool           `tfsdk:"compliant"`
	Connected   types.Bool           `tfsdk:"connected"`
	NeedsReboot types.Bool           `tfsdk:"needs_reboot"`
	CreateTime  types.String         `tfsdk:"create_time"`
	Patches     []prepatchPatchModel `tfsdk:"patches"`
}

type prepatchPatchModel struct {
	ID       types.Int64  `tfsdk:"id"`
	Name     types.String `tfsdk:"name"`
	Severity types.String `tfsdk:"severity"`
	// Exposed as cves, plural. Automox names the field `cve` but returns a list
	// of CVE identifiers, and those lists are what make this report roughly a
	// megabyte. The plural matches automox_device_packages.
	CVEs          types.List   `tfsdk:"cves"`
	NeedsApproval types.Bool   `tfsdk:"needs_approval"`
	PatchTime     types.String `tfsdk:"patch_time"`
	CreateTime    types.String `tfsdk:"create_time"`
}

type apiPrepatchReport struct {
	Prepatch struct {
		apiSeverityCounts
		NeedsAttention int64 `json:"needsAttention"`
		Devices        []struct {
			ID          int64   `json:"id"`
			Name        string  `json:"name"`
			OSFamily    string  `json:"os_family"`
			Group       *string `json:"group"`
			Compliant   bool    `json:"compliant"`
			Connected   bool    `json:"connected"`
			NeedsReboot bool    `json:"needsReboot"`
			CreateTime  *string `json:"createTime"`
			Patches     []struct {
				ID            int64    `json:"id"`
				Name          string   `json:"name"`
				Severity      *string  `json:"severity"`
				CVE           []string `json:"cve"`
				NeedsApproval bool     `json:"needsApproval"`
				PatchTime     *string  `json:"patchTime"`
				CreateTime    *string  `json:"createTime"`
			} `json:"patches"`
		} `json:"devices"`
	} `json:"prepatch"`
}

func (d *prepatchDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = PrepatchReportName
}

func (d *prepatchDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = configure(req, resp)
}

func (d *prepatchDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	device := reportDeviceAttributes()
	device["group"] = schema.StringAttribute{
		Computed:            true,
		MarkdownDescription: "The device's server group, by name rather than ID here.",
	}
	device["create_time"] = schema.StringAttribute{Computed: true}
	device["patches"] = schema.ListNestedAttribute{
		Computed:            true,
		MarkdownDescription: "Patches outstanding on this device.",
		NestedObject: schema.NestedAttributeObject{
			Attributes: map[string]schema.Attribute{
				"id":       schema.Int64Attribute{Computed: true},
				"name":     schema.StringAttribute{Computed: true},
				"severity": schema.StringAttribute{Computed: true},
				"cves": schema.ListAttribute{
					Computed:    true,
					ElementType: types.StringType,
					MarkdownDescription: "CVE identifiers this patch addresses. Automox " +
						"returns these under a singular `cve` key.",
				},
				"needs_approval": schema.BoolAttribute{
					Computed:            true,
					MarkdownDescription: "Whether the patch is held pending approval.",
				},
				"patch_time":  schema.StringAttribute{Computed: true},
				"create_time": schema.StringAttribute{Computed: true},
			},
		},
	}

	attrs := severityAttributes()
	attrs["needs_attention"] = schema.Int64Attribute{
		Computed:            true,
		MarkdownDescription: "Devices in this report that also need attention.",
	}
	attrs["devices"] = schema.ListNestedAttribute{
		Computed:            true,
		MarkdownDescription: "Devices with outstanding patches.",
		NestedObject:        schema.NestedAttributeObject{Attributes: device},
	}

	resp.Schema = schema.Schema{
		MarkdownDescription: "Automox's prepatch report: what would be applied on the " +
			"next patch run, by device and severity.\n\n" +
			"This is the largest read the provider offers — roughly a megabyte for a " +
			"fleet of 1,500 — and all of it lands in Terraform state. Use it for " +
			"reporting, not as an input to resources.",
		Attributes: attrs,
	}
}

func (d *prepatchDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	if notConfigured(d.client, resp) {
		return
	}

	var report apiPrepatchReport
	if err := d.client.Do(ctx, client.Request{
		Method:   "GET",
		Path:     "/reports/prepatch",
		OrgScope: client.OrgScopeQuery,
	}, &report); err != nil {
		resp.Diagnostics.AddError("Could not read the Automox prepatch report", err.Error())
		return
	}

	r := report.Prepatch
	state := prepatchModel{
		severityModel:  flattenSeverity(r.apiSeverityCounts),
		NeedsAttention: types.Int64Value(r.NeedsAttention),
		Devices:        make([]prepatchDeviceModel, 0, len(r.Devices)),
	}

	for _, dev := range r.Devices {
		patches := make([]prepatchPatchModel, 0, len(dev.Patches))
		for _, p := range dev.Patches {
			// A patch with no CVEs is genuinely empty rather than unknown, and
			// Automox sends null for it. Normalising to an empty list keeps
			// length(patch.cves) working in configuration for every patch.
			cveIDs := p.CVE
			if cveIDs == nil {
				cveIDs = []string{}
			}
			cves, diags := types.ListValueFrom(ctx, types.StringType, cveIDs)
			resp.Diagnostics.Append(diags...)
			if resp.Diagnostics.HasError() {
				return
			}
			patches = append(patches, prepatchPatchModel{
				ID:            types.Int64Value(p.ID),
				Name:          types.StringValue(p.Name),
				Severity:      optionalString(p.Severity),
				CVEs:          cves,
				NeedsApproval: types.BoolValue(p.NeedsApproval),
				PatchTime:     optionalString(p.PatchTime),
				CreateTime:    optionalString(p.CreateTime),
			})
		}

		state.Devices = append(state.Devices, prepatchDeviceModel{
			ID:          types.Int64Value(dev.ID),
			Name:        types.StringValue(dev.Name),
			OSFamily:    types.StringValue(dev.OSFamily),
			Group:       optionalString(dev.Group),
			Compliant:   types.BoolValue(dev.Compliant),
			Connected:   types.BoolValue(dev.Connected),
			NeedsReboot: types.BoolValue(dev.NeedsReboot),
			CreateTime:  optionalString(dev.CreateTime),
			Patches:     patches,
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
