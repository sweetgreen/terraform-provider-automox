// This file cannot be named scheduled_windows.go.
//
// Go reads a trailing _windows before the extension as an implicit GOOS build
// constraint, so scheduled_windows.go compiles only on Windows and is silently
// dropped everywhere else -- it lands in IgnoredGoFiles, the package still
// builds, and the only symptom is that the constructor appears undefined from
// another package. The same trap applies to _linux, _darwin, _test and the rest
// of the GOOS and GOARCH names.
package datasources

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/sweetgreen/terraform-provider-automox/internal/client"
)

const ScheduledWindowsName = "automox_scheduled_windows"

// windowPageSize is the page size requested from the search endpoint.
//
// The default is 20 and cannot be raised through query parameters -- they are
// ignored -- so it has to be set in the request body. 100 keeps a typical
// organization to a single request.
const windowPageSize = 100

// maxWindowPages bounds the walk so a server that stops advancing cannot loop
// forever. At the page size above this is 100,000 windows.
const maxWindowPages = 1000

var (
	_ datasource.DataSource              = &scheduledWindowsDataSource{}
	_ datasource.DataSourceWithConfigure = &scheduledWindowsDataSource{}
)

func NewScheduledWindows() datasource.DataSource { return &scheduledWindowsDataSource{} }

type scheduledWindowsDataSource struct {
	client *client.Client
}

type scheduledWindowsModel struct {
	ScheduledWindows []scheduledWindowModel `tfsdk:"scheduled_windows"`
}

type scheduledWindowModel struct {
	WindowUUID  types.String `tfsdk:"window_uuid"`
	Name        types.String `tfsdk:"window_name"`
	Description types.String `tfsdk:"window_description"`
	WindowType  types.String `tfsdk:"window_type"`
	OrgUUID     types.String `tfsdk:"org_uuid"`
	GroupUUIDs  types.List   `tfsdk:"group_uuids"`
	Recurrence  types.String `tfsdk:"recurrence"`
	RRule       types.String `tfsdk:"rrule"`
	DTStart     types.String `tfsdk:"dtstart"`
	Duration    types.Int64  `tfsdk:"duration_minutes"`
	Status      types.String `tfsdk:"status"`
	UseLocalTZ  types.Bool   `tfsdk:"use_local_tz"`
	CreatedAt   types.String `tfsdk:"created_at"`
	UpdatedAt   types.String `tfsdk:"updated_at"`
}

type apiScheduledWindow struct {
	WindowUUID  string   `json:"window_uuid"`
	Name        string   `json:"window_name"`
	Description *string  `json:"window_description"`
	WindowType  string   `json:"window_type"`
	OrgUUID     string   `json:"org_uuid"`
	GroupUUIDs  []string `json:"group_uuids"`
	Recurrence  string   `json:"recurrence"`
	RRule       string   `json:"rrule"`
	DTStart     string   `json:"dtstart"`
	Duration    *int64   `json:"duration_minutes"`
	Status      *string  `json:"status"`
	UseLocalTZ  bool     `json:"use_local_tz"`
	CreatedAt   *string  `json:"created_at"`
	UpdatedAt   *string  `json:"updated_at"`
}

// windowSearchPage is a fourth collection envelope, distinct from the three in
// the client.
//
// Maintenance windows live behind a different service from the rest of the API,
// and it returns a Spring Data page: the records are under `content`, and the
// paging state is spread across `pageable`, `last`, `number`, and
// `total_elements`. It is also POST-only -- GET on the collection is 405 -- so
// the shared List helper, which is GET-based and knows three envelopes, cannot
// read it. Hence the walk below.
type windowSearchPage struct {
	Content  []apiScheduledWindow `json:"content"`
	Last     bool                 `json:"last"`
	Number   int                  `json:"number"`
	Total    int                  `json:"total_elements"`
	Pageable struct {
		PageSize int `json:"page_size"`
	} `json:"pageable"`
}

func (d *scheduledWindowsDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = ScheduledWindowsName
}

func (d *scheduledWindowsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = configure(req, resp)
}

func (d *scheduledWindowsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Maintenance windows in the configured organization.\n\n" +
			"Windows are scoped by organization UUID and identify server groups by " +
			"UUID, where the rest of the API uses integer IDs; the provider resolves " +
			"that for you.\n\n" +
			"No filter arguments are offered because the search endpoint has none: it " +
			"accepts a request body but ignores any filter in it, so a filter here " +
			"would look like it worked while returning everything.",
		Attributes: map[string]schema.Attribute{
			"scheduled_windows": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"window_uuid": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Windows are identified by UUID; Automox issues no integer ID.",
						},
						"window_name":        schema.StringAttribute{Computed: true},
						"window_description": schema.StringAttribute{Computed: true},
						"window_type": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "`exclude` — the only value Automox accepts.",
						},
						"org_uuid": schema.StringAttribute{Computed: true},
						"group_uuids": schema.ListAttribute{
							Computed:            true,
							ElementType:         types.StringType,
							MarkdownDescription: "UUIDs of the server groups this window covers.",
						},
						"recurrence": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "`ONCE` or `RECURRING`.",
						},
						"rrule":   schema.StringAttribute{Computed: true},
						"dtstart": schema.StringAttribute{Computed: true},
						"duration_minutes": schema.Int64Attribute{
							Computed:            true,
							MarkdownDescription: "Unset on `ONCE` windows, where Automox derives it.",
						},
						"status":       schema.StringAttribute{Computed: true},
						"use_local_tz": schema.BoolAttribute{Computed: true},
						"created_at":   schema.StringAttribute{Computed: true},
						"updated_at":   schema.StringAttribute{Computed: true},
					},
				},
			},
		},
	}
}

func (d *scheduledWindowsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	if notConfigured(d.client, resp) {
		return
	}

	orgUUID, err := d.client.DefaultOrganizationUUID(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Could not resolve the Automox organization UUID", err.Error())
		return
	}

	var windows []apiScheduledWindow
	for page := 0; ; page++ {
		if page >= maxWindowPages {
			resp.Diagnostics.AddError(
				"Too many pages of maintenance windows",
				fmt.Sprintf("The search endpoint returned more than %d pages; refusing to "+
					"continue in case it is ignoring pagination.", maxWindowPages),
			)
			return
		}

		var result windowSearchPage
		// Paging goes in the body. Query parameters are accepted and ignored: the
		// page size stays at its default of 20 whatever ?size= says.
		if err := d.client.Do(ctx, client.Request{
			Method:   "POST",
			Path:     fmt.Sprintf("/policy-windows/org/%s/search", orgUUID),
			OrgScope: client.OrgScopeNone,
			Body:     map[string]any{"page": page, "size": windowPageSize},
		}, &result); err != nil {
			resp.Diagnostics.AddError("Could not list Automox maintenance windows", err.Error())
			return
		}

		windows = append(windows, result.Content...)

		if result.Last || len(result.Content) == 0 {
			break
		}

		// The endpoint reports which page it served. If that does not advance, the
		// walk would repeat the same records forever, so stop and say so rather
		// than looping or returning duplicates.
		if result.Number != page {
			resp.Diagnostics.AddError(
				"Maintenance window paging did not advance",
				fmt.Sprintf("Asked for page %d and the API reported page %d. Refusing to "+
					"continue rather than return duplicated or partial results.",
					page, result.Number),
			)
			return
		}
	}

	state := scheduledWindowsModel{
		ScheduledWindows: make([]scheduledWindowModel, 0, len(windows)),
	}

	for _, w := range windows {
		groups, diags := types.ListValueFrom(ctx, types.StringType, w.GroupUUIDs)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}

		state.ScheduledWindows = append(state.ScheduledWindows, scheduledWindowModel{
			WindowUUID:  types.StringValue(w.WindowUUID),
			Name:        types.StringValue(w.Name),
			Description: optionalString(w.Description),
			WindowType:  types.StringValue(w.WindowType),
			OrgUUID:     types.StringValue(w.OrgUUID),
			GroupUUIDs:  groups,
			Recurrence:  types.StringValue(w.Recurrence),
			RRule:       types.StringValue(w.RRule),
			DTStart:     types.StringValue(w.DTStart),
			Duration:    optionalInt(w.Duration),
			Status:      optionalString(w.Status),
			UseLocalTZ:  types.BoolValue(w.UseLocalTZ),
			CreatedAt:   optionalString(w.CreatedAt),
			UpdatedAt:   optionalString(w.UpdatedAt),
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
