package datasources

import (
	"context"
	"fmt"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/sweetgreen/terraform-provider-automox/internal/client"
)

const (
	WorkletsName = "automox_worklets"
	WorkletName  = "automox_worklet"
)

var (
	_ datasource.DataSource              = &workletsDataSource{}
	_ datasource.DataSourceWithConfigure = &workletsDataSource{}
	_ datasource.DataSource              = &workletDataSource{}
	_ datasource.DataSourceWithConfigure = &workletDataSource{}
)

func NewWorklets() datasource.DataSource { return &workletsDataSource{} }
func NewWorklet() datasource.DataSource  { return &workletDataSource{} }

type workletsDataSource struct{ client *client.Client }
type workletDataSource struct{ client *client.Client }

type workletsModel struct {
	Query    types.String   `tfsdk:"query"`
	OSFamily types.String   `tfsdk:"os_family"`
	Category types.String   `tfsdk:"category"`
	Worklets []workletModel `tfsdk:"worklets"`
}

type workletModel struct {
	UUID            types.String `tfsdk:"uuid"`
	Name            types.String `tfsdk:"name"`
	Description     types.String `tfsdk:"description"`
	OSFamily        types.String `tfsdk:"os_family"`
	Language        types.String `tfsdk:"language"`
	Version         types.String `tfsdk:"version"`
	Categories      types.List   `tfsdk:"categories"`
	Keywords        types.List   `tfsdk:"keywords"`
	DeviceType      types.List   `tfsdk:"device_type"`
	Access          types.String `tfsdk:"access"`
	Verified        types.Bool   `tfsdk:"verified"`
	LicenseRequired types.Bool   `tfsdk:"license_required"`
	CreateTime      types.String `tfsdk:"create_time"`
	UpdateTime      types.String `tfsdk:"update_time"`
}

type apiWorklet struct {
	UUID            string   `json:"uuid"`
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	OSFamily        string   `json:"os_family"`
	Language        string   `json:"language"`
	Version         string   `json:"version"`
	Categories      []string `json:"categories"`
	Keywords        []string `json:"keywords"`
	DeviceType      []string `json:"device_type"`
	Access          string   `json:"access"`
	Verified        bool     `json:"verified"`
	LicenseRequired bool     `json:"license_required"`
	CreateTime      string   `json:"create_time"`
	UpdateTime      string   `json:"update_time"`
}

func workletAttributes(uuidRequired bool) map[string]schema.Attribute {
	uuid := schema.StringAttribute{Computed: true}
	if uuidRequired {
		uuid = schema.StringAttribute{
			Required:            true,
			MarkdownDescription: "The catalogue worklet's UUID.",
		}
	}

	return map[string]schema.Attribute{
		"uuid":        uuid,
		"name":        schema.StringAttribute{Computed: true},
		"description": schema.StringAttribute{Computed: true},
		"os_family": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "`Windows`, `Mac`, or `Linux`.",
		},
		"language": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Scripting language, such as `PowerShell` or `Bash`.",
		},
		"version":    schema.StringAttribute{Computed: true},
		"categories": schema.ListAttribute{Computed: true, ElementType: types.StringType},
		"keywords":   schema.ListAttribute{Computed: true, ElementType: types.StringType},
		"device_type": schema.ListAttribute{
			Computed:            true,
			ElementType:         types.StringType,
			MarkdownDescription: "`SERVER`, `WORKSTATION`, or both.",
		},
		"access": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "`free` or `premium`.",
		},
		"verified": schema.BoolAttribute{
			Computed:            true,
			MarkdownDescription: "Whether Automox has verified this worklet.",
		},
		"license_required": schema.BoolAttribute{Computed: true},
		"create_time":      schema.StringAttribute{Computed: true},
		"update_time":      schema.StringAttribute{Computed: true},
	}
}

// --- automox_worklets ---

func (d *workletsDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = WorkletsName
}

func (d *workletsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = configure(req, resp)
}

func (d *workletsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "The Automox Worklet Catalogue — community and Automox-" +
			"authored worklets available to this organization.\n\n" +
			"These are catalogue entries, not policies in your organization; use " +
			"`automox_policies` with `policy_type = \"custom\"` for those.\n\n" +
			"Worklet source code is not returned by this endpoint, so none is written " +
			"to state.",
		Attributes: map[string]schema.Attribute{
			"query": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Full-text search across the catalogue.",
			},
			"os_family": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Only worklets for this OS family.",
			},
			"category": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Only worklets in this category, such as `Security`.",
			},
			"worklets": schema.ListNestedAttribute{
				Computed:     true,
				NestedObject: schema.NestedAttributeObject{Attributes: workletAttributes(false)},
			},
		},
	}
}

func (d *workletsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if notConfigured(d.client, resp) {
		return
	}

	var config workletsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Only these three are offered because only these three work. A `language`
	// parameter is accepted and silently ignored -- it returns the full catalogue
	// -- so exposing it would look like a filter while doing nothing.
	query := url.Values{}
	if !config.Query.IsNull() {
		query.Set("q", config.Query.ValueString())
	}
	if !config.OSFamily.IsNull() {
		query.Set("os_family", config.OSFamily.ValueString())
	}
	if !config.Category.IsNull() {
		query.Set("category", config.Category.ValueString())
	}

	var worklets []apiWorklet
	if err := d.client.List(ctx, client.ListOptions{
		Path:     "/wis/search",
		Query:    query,
		OrgScope: client.OrgScopeQuery,
		Envelope: client.EnvelopeData,
	}, &worklets); err != nil {
		resp.Diagnostics.AddError("Could not search the Automox worklet catalogue", err.Error())
		return
	}

	state := workletsModel{
		Query:    config.Query,
		OSFamily: config.OSFamily,
		Category: config.Category,
		Worklets: make([]workletModel, 0, len(worklets)),
	}

	for _, w := range worklets {
		model, diags := flattenWorklet(ctx, w)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		state.Worklets = append(state.Worklets, model)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// --- automox_worklet ---

func (d *workletDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = WorkletName
}

func (d *workletDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = configure(req, resp)
}

func (d *workletDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "A single Worklet Catalogue entry by UUID.",
		Attributes:          workletAttributes(true),
	}
}

func (d *workletDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if notConfigured(d.client, resp) {
		return
	}

	var config workletModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	uuid := config.UUID.ValueString()

	var w apiWorklet
	// The single-worklet read hangs off the search path rather than a /wis/worklets
	// collection; /wis/worklets/{uuid} and /wis/{uuid} are both 404.
	if err := d.client.Do(ctx, client.Request{
		Method:   "GET",
		Path:     fmt.Sprintf("/wis/search/%s", url.PathEscape(uuid)),
		OrgScope: client.OrgScopeQuery,
	}, &w); err != nil {
		resp.Diagnostics.AddError(
			"Could not read the Automox worklet",
			fmt.Sprintf("Reading catalogue worklet %s failed: %s", uuid, err),
		)
		return
	}

	if w.UUID == "" {
		resp.Diagnostics.AddError(
			"No worklet has that UUID",
			fmt.Sprintf("The catalogue returned no worklet for %s.", uuid),
		)
		return
	}

	model, diags := flattenWorklet(ctx, w)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, model)...)
}

func flattenWorklet(ctx context.Context, w apiWorklet) (workletModel, diag.Diagnostics) {
	var diags diag.Diagnostics

	categories, d1 := types.ListValueFrom(ctx, types.StringType, w.Categories)
	diags.Append(d1...)
	keywords, d2 := types.ListValueFrom(ctx, types.StringType, w.Keywords)
	diags.Append(d2...)
	deviceType, d3 := types.ListValueFrom(ctx, types.StringType, w.DeviceType)
	diags.Append(d3...)

	return workletModel{
		UUID:            types.StringValue(w.UUID),
		Name:            types.StringValue(w.Name),
		Description:     optionalString(&w.Description),
		OSFamily:        types.StringValue(w.OSFamily),
		Language:        optionalString(&w.Language),
		Version:         optionalString(&w.Version),
		Categories:      categories,
		Keywords:        keywords,
		DeviceType:      deviceType,
		Access:          optionalString(&w.Access),
		Verified:        types.BoolValue(w.Verified),
		LicenseRequired: types.BoolValue(w.LicenseRequired),
		CreateTime:      optionalString(&w.CreateTime),
		UpdateTime:      optionalString(&w.UpdateTime),
	}, diags
}
