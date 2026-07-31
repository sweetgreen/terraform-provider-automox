package datasources

import (
	"context"
	"net/url"
	"strconv"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/sweetgreen/terraform-provider-automox/internal/client"
)

const EventsName = "automox_events"

// defaultEventLimit bounds the read when the practitioner does not choose one.
//
// /events is an audit log: it runs past 200 pages of 250 records in this
// organization alone and has no natural end. Reading it whole would take minutes
// and put tens of thousands of records into Terraform state on every plan, so
// the default is a recent slice rather than everything.
const defaultEventLimit = 250

var (
	_ datasource.DataSource              = &eventsDataSource{}
	_ datasource.DataSourceWithConfigure = &eventsDataSource{}
)

func NewEvents() datasource.DataSource { return &eventsDataSource{} }

type eventsDataSource struct {
	client *client.Client
}

type eventsModel struct {
	EventName  types.String `tfsdk:"event_name"`
	DeviceID   types.Int64  `tfsdk:"device_id"`
	PolicyID   types.Int64  `tfsdk:"policy_id"`
	MaxResults types.Int64  `tfsdk:"max_results"`
	Truncated  types.Bool   `tfsdk:"truncated"`
	Events     []eventModel `tfsdk:"events"`
}

type eventModel struct {
	ID         types.Int64  `tfsdk:"id"`
	Name       types.String `tfsdk:"name"`
	CreateTime types.String `tfsdk:"create_time"`
	DeviceID   types.Int64  `tfsdk:"device_id"`
	DeviceName types.String `tfsdk:"device_name"`
	PolicyID   types.Int64  `tfsdk:"policy_id"`
	PolicyName types.String `tfsdk:"policy_name"`
	PolicyType types.String `tfsdk:"policy_type"`
}

// apiEvent omits the event's `data` object.
//
// `data` is free-form and its contents depend entirely on the event type. In
// this organization alone it carries device addresses and hostnames on
// system.patch.* events and employee first and last names on saml.user.create.
// There is no schema bounding what a future event type might put there, and data
// source attributes are written to Terraform state in plaintext, so mirroring an
// open-ended field would mean promising not to leak something this provider does
// not control. The structured columns below carry what identifies the event.
type apiEvent struct {
	ID             int64   `json:"id"`
	Name           string  `json:"name"`
	CreateTime     string  `json:"create_time"`
	ServerID       *int64  `json:"server_id"`
	ServerName     *string `json:"server_name"`
	PolicyID       *int64  `json:"policy_id"`
	PolicyName     *string `json:"policy_name"`
	PolicyTypeName *string `json:"policy_type_name"`
}

func (d *eventsDataSource) Metadata(_ context.Context, _ datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = EventsName
}

func (d *eventsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	d.client = configure(req, resp)
}

func (d *eventsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Recent activity from the organization's event log.\n\n" +
			"This is an audit log with no natural end, so the read is bounded: it " +
			"returns the most recent `max_results` events and sets `truncated` when " +
			"more exist. Filters are applied by the API, so they narrow what is " +
			"fetched rather than what is returned.\n\n" +
			"An event's free-form `data` object is not exposed. What it contains " +
			"depends on the event type — device addresses and hostnames on patch " +
			"events, employee names on user events — and nothing bounds what a future " +
			"event type might add.",
		Attributes: map[string]schema.Attribute{
			"event_name": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Only events of this type, such as " +
					"`system.patch.applied` or `system.policy.action`.",
			},
			"device_id": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Only events for this device.",
			},
			"policy_id": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Only events for this policy.",
			},
			"max_results": schema.Int64Attribute{
				Optional: true,
				MarkdownDescription: "How many events to return at most. Defaults to " +
					"250. Raising this is a slow read against a rate-limited API.",
				Validators: []validator.Int64{int64validator.AtLeast(1)},
			},
			"truncated": schema.BoolAttribute{
				Computed: true,
				MarkdownDescription: "True when more events matched than were returned. " +
					"Narrow the filters or raise `max_results` to see the rest.",
			},
			"events": schema.ListNestedAttribute{
				Computed: true,
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.Int64Attribute{Computed: true},
						"name": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "The event type.",
						},
						"create_time": schema.StringAttribute{Computed: true},
						"device_id":   schema.Int64Attribute{Computed: true},
						"device_name": schema.StringAttribute{Computed: true},
						"policy_id":   schema.Int64Attribute{Computed: true},
						"policy_name": schema.StringAttribute{Computed: true},
						"policy_type": schema.StringAttribute{Computed: true},
					},
				},
			},
		},
	}
}

func (d *eventsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if notConfigured(d.client, resp) {
		return
	}

	var config eventsModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Every filter here is applied by the API, verified live: eventName returns
	// only that type, and serverId and policyId return only that object's events.
	query := url.Values{}
	if !config.EventName.IsNull() {
		query.Set("eventName", config.EventName.ValueString())
	}
	if !config.DeviceID.IsNull() {
		query.Set("serverId", strconv.FormatInt(config.DeviceID.ValueInt64(), 10))
	}
	if !config.PolicyID.IsNull() {
		query.Set("policyId", strconv.FormatInt(config.PolicyID.ValueInt64(), 10))
	}

	max := int64(defaultEventLimit)
	if !config.MaxResults.IsNull() {
		max = config.MaxResults.ValueInt64()
	}

	var events []apiEvent
	truncated, err := d.client.ListBounded(ctx, client.ListOptions{
		Path:       "/events",
		Query:      query,
		OrgScope:   client.OrgScopeQuery,
		Envelope:   client.EnvelopeArray,
		MaxResults: int(max),
	}, &events)
	if err != nil {
		resp.Diagnostics.AddError("Could not read the Automox event log", err.Error())
		return
	}

	state := eventsModel{
		EventName:  config.EventName,
		DeviceID:   config.DeviceID,
		PolicyID:   config.PolicyID,
		MaxResults: config.MaxResults,
		Truncated:  types.BoolValue(truncated),
		Events:     make([]eventModel, 0, len(events)),
	}

	for _, e := range events {
		state.Events = append(state.Events, eventModel{
			ID:         types.Int64Value(e.ID),
			Name:       types.StringValue(e.Name),
			CreateTime: types.StringValue(e.CreateTime),
			DeviceID:   optionalInt(e.ServerID),
			DeviceName: optionalString(e.ServerName),
			PolicyID:   optionalInt(e.PolicyID),
			PolicyName: optionalString(e.PolicyName),
			PolicyType: optionalString(e.PolicyTypeName),
		})
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}
