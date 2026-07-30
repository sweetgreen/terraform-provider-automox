package scheduled_window

import "github.com/hashicorp/terraform-plugin-framework/types"

// Recurrence values. Automox uppercases these on write, so comparisons are
// case-insensitive but the stored form is always upper.
const (
	RecurrenceOnce      = "ONCE"
	RecurrenceRecurring = "RECURRING"
)

// WindowTypeExclude is the only value Automox accepts.
//
// Its published example uses "exclusion", which the API rejects with
// "Invalid window type: exclusion. Valid values are: exclude".
const WindowTypeExclude = "exclude"

type windowModel struct {
	WindowUUID      types.String `tfsdk:"window_uuid"`
	Name            types.String `tfsdk:"window_name"`
	Description     types.String `tfsdk:"window_description"`
	WindowType      types.String `tfsdk:"window_type"`
	Recurrence      types.String `tfsdk:"recurrence"`
	DTStart         types.String `tfsdk:"dtstart"`
	RRule           types.String `tfsdk:"rrule"`
	DurationMinutes types.Int64  `tfsdk:"duration_minutes"`
	GroupUUIDs      types.Set    `tfsdk:"group_uuids"`
	Status          types.String `tfsdk:"status"`

	OrgUUID   types.String `tfsdk:"org_uuid"`
	CreatedAt types.String `tfsdk:"created_at"`
	UpdatedAt types.String `tfsdk:"updated_at"`
}

type apiWindow struct {
	WindowUUID      string   `json:"window_uuid"`
	Name            string   `json:"window_name"`
	Description     string   `json:"window_description"`
	WindowType      string   `json:"window_type"`
	Recurrence      string   `json:"recurrence"`
	DTStart         string   `json:"dtstart"`
	RRule           string   `json:"rrule"`
	DurationMinutes *int64   `json:"duration_minutes"`
	GroupUUIDs      []string `json:"group_uuids"`
	Status          string   `json:"status"`
	UseLocalTZ      *bool    `json:"use_local_tz"`

	OrgUUID   string `json:"org_uuid"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}
