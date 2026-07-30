package policy

import (
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Policy types Automox supports. The API calls the worklet type "custom".
const (
	TypePatch            = "patch"
	TypeWorklet          = "custom"
	TypeRequiredSoftware = "required_software"
)

// patch_rule values.
const (
	PatchRuleAll      = "all"
	PatchRuleFilter   = "filter"
	PatchRuleManual   = "manual"
	PatchRuleAdvanced = "advanced"
)

type policyModel struct {
	ID     types.Int64  `tfsdk:"id"`
	UUID   types.String `tfsdk:"uuid"`
	Name   types.String `tfsdk:"name"`
	Type   types.String `tfsdk:"policy_type"`
	Notes  types.String `tfsdk:"notes"`
	Groups types.List   `tfsdk:"server_groups"`

	ScheduleTime         types.String `tfsdk:"schedule_time"`
	ScheduleDays         types.Int64  `tfsdk:"schedule_days"`
	ScheduleDaysOfWeek   types.Set    `tfsdk:"schedule_days_of_week"`
	ScheduleMonths       types.Int64  `tfsdk:"schedule_months"`
	ScheduleMonthsOfYear types.Set    `tfsdk:"schedule_months_of_year"`
	ScheduleWeeksOfMonth types.Int64  `tfsdk:"schedule_weeks_of_month"`
	UseScheduledTimezone types.Bool   `tfsdk:"use_scheduled_timezone"`
	ScheduledTimezone    types.String `tfsdk:"scheduled_timezone"`

	Configuration types.Object `tfsdk:"configuration"`

	OrganizationID  types.Int64  `tfsdk:"organization_id"`
	Status          types.String `tfsdk:"status"`
	ServerCount     types.Int64  `tfsdk:"server_count"`
	CreateTime      types.String `tfsdk:"create_time"`
	NextRemediation types.String `tfsdk:"next_remediation"`
}

// apiPolicy is the wire shape.
//
// `configuration` is deliberately a loose map rather than a struct. The live API
// returns a superset of what its own document describes -- patch policies come
// back carrying worklet fields, plus four attributes absent from the document
// entirely -- so a fixed struct would silently drop whatever Automox adds next.
// The schema below still declares each attribute explicitly; this type only
// avoids the round-trip losing anything.
type apiPolicy struct {
	ID                   int64          `json:"id"`
	UUID                 string         `json:"uuid"`
	Name                 string         `json:"name"`
	PolicyTypeName       string         `json:"policy_type_name"`
	Notes                string         `json:"notes"`
	OrganizationID       int64          `json:"organization_id"`
	ServerGroups         []int64        `json:"server_groups"`
	ScheduleDays         int64          `json:"schedule_days"`
	ScheduleWeeksOfMonth int64          `json:"schedule_weeks_of_month"`
	ScheduleMonths       int64          `json:"schedule_months"`
	ScheduleTime         string         `json:"schedule_time"`
	UseScheduledTimezone *bool          `json:"use_scheduled_timezone"`
	ScheduledTimezone    *string        `json:"scheduled_timezone"`
	Configuration        map[string]any `json:"configuration"`

	Status          *string `json:"status"`
	ServerCount     *int64  `json:"server_count"`
	CreateTime      *string `json:"create_time"`
	NextRemediation *string `json:"next_remediation"`
}
