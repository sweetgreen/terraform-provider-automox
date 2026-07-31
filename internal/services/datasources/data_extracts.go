package datasources

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/sweetgreen/terraform-provider-automox/internal/client"
)

const DataExtractsName = "automox_data_extracts"

var (
	_ datasource.DataSource              = &dataExtractsDataSource{}
	_ datasource.DataSourceWithConfigure = &dataExtractsDataSource{}
)

func NewDataExtracts() datasource.DataSource { return &dataExtractsDataSource{} }

type dataExtractsDataSource struct {
	client *client.Client
}

type dataExtractsModel struct {
	Type     types.String       `tfsdk:"type"`
	Extracts []dataExtractModel `tfsdk:"data_extracts"`
}

type dataExtractModel struct {
	ID                types.Int64  `tfsdk:"id"`
	Type              types.String `tfsdk:"type"`
	Status            types.String `tfsdk:"status"`
	IsCompleted       types.Bool   `tfsdk:"is_completed"`
	CreatedAt         types.String `tfsdk:"created_at"`
	DownloadExpiresAt types.String `tfsdk:"download_expires_at"`
	StartTime         types.String `tfsdk:"start_time"`
	EndTime           types.String `tfsdk:"end_time"`
	UserID            types.Int64  `tfsdk:"user_id"`
}

// apiDataExtract omits `download_url`.
//
// When an extract is ready that field holds a pre-signed URL granting anyone who
// has it access to an export of the organization's data. Data source attributes
// are written to Terraform state in plaintext, so exposing it would put a
// working credential into state files and CI artefacts -- the same reason the
// organization access key is dropped in organizations.go. Fetch a completed
// extract through the console or the API directly.
//
// `parameters` is flattened to start_time and end_time rather than mirrored as a
// free-form object: every extract observed carries exactly those two, and typed
// fields are usable in configuration where an opaque blob is not.
type apiDataExtract struct {
	ID                int64   `json:"id"`
	Type              string  `json:"type"`
	Status            string  `json:"status"`
	IsCompleted       bool    `json:"is_completed"`
	CreatedAt         string  `json:"created_at"`
	DownloadExpiresAt *string `json:"download_expires_at"`
	UserID            int64   `json:"user_id"`
	Parameters        struct {
		StartTime *string `json:"start_time"`
		EndTime   *string `json:"end_time"`
	} `json:"parameters"`
}

func (d *dataExtractsDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = DataExtractsName
}

func (d *dataExtractsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = configure(req, resp)
}

func (d *dataExtractsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Data extracts requested for the organization, such as " +
			"API activity and patch history exports.\n\n" +
			"The download URL is deliberately not exposed. For a completed extract it " +
			"is a pre-signed link to an export of the organization's data, and anything " +
			"a data source returns is written to Terraform state in plaintext.",
		Attributes: map[string]schema.Attribute{
			"type": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Return only extracts of this kind, such as " +
					"`api-activity` or `patch-history`.",
			},
			"data_extracts": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":   schema.Int64Attribute{Computed: true},
						"type": schema.StringAttribute{Computed: true},
						"status": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Automox's status, such as `expired` or `completed`.",
						},
						"is_completed": schema.BoolAttribute{Computed: true},
						"created_at":   schema.StringAttribute{Computed: true},
						"download_expires_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "When the export stops being downloadable.",
						},
						"start_time": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Start of the period the extract covers.",
						},
						"end_time": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "End of the period the extract covers.",
						},
						"user_id": schema.Int64Attribute{
							Computed:            true,
							MarkdownDescription: "The Automox user who requested the extract.",
						},
					},
				},
			},
		},
	}
}

func (d *dataExtractsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if notConfigured(d.client, resp) {
		return
	}

	var config dataExtractsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var extracts []apiDataExtract
	// {"results": [...], "size": N}, where size is the total rather than the
	// length of this page.
	if err := d.client.List(ctx, client.ListOptions{
		Path:     "/data-extracts",
		OrgScope: client.OrgScopeQuery,
		Envelope: client.EnvelopeResults,
	}, &extracts); err != nil {
		resp.Diagnostics.AddError("Could not list Automox data extracts", err.Error())
		return
	}

	state := dataExtractsModel{Type: config.Type, Extracts: []dataExtractModel{}}
	for _, e := range extracts {
		if !config.Type.IsNull() && e.Type != config.Type.ValueString() {
			continue
		}

		state.Extracts = append(state.Extracts, dataExtractModel{
			ID:                types.Int64Value(e.ID),
			Type:              types.StringValue(e.Type),
			Status:            optionalString(&e.Status),
			IsCompleted:       types.BoolValue(e.IsCompleted),
			CreatedAt:         optionalString(&e.CreatedAt),
			DownloadExpiresAt: optionalString(e.DownloadExpiresAt),
			StartTime:         optionalString(e.Parameters.StartTime),
			EndTime:           optionalString(e.Parameters.EndTime),
			UserID:            types.Int64Value(e.UserID),
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
