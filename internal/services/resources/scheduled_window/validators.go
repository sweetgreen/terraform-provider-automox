package scheduled_window

import (
	"context"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
)

// Automox accepts only a narrow subset of RFC 5545, and rejects the rest with a
// 400 that names the field but not the rule. Validating here turns that into a
// plan-time message explaining what the API will accept.
var (
	rruleOnce      = regexp.MustCompile(`(?i)^FREQ=DAILY;UNTIL=\d{8}T\d{6}Z$`)
	rruleRecurring = regexp.MustCompile(`(?i)^FREQ=YEARLY(;(BYMONTH|BYDAY)=[^;]+)+$`)
)

// ValidateConfig enforces the window rules before the API is called.
//
// Both rules below were established by being rejected live, and both produce
// errors that are hard to act on without knowing the rule: the duration message
// names a camelCase field the practitioner never wrote, and the rrule message
// does not say which forms are legal.
func (r *windowResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config windowModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	recurrence := strings.ToUpper(config.Recurrence.ValueString())
	if recurrence == "" || config.Recurrence.IsUnknown() {
		return
	}

	durationSet := !config.DurationMinutes.IsNull() && !config.DurationMinutes.IsUnknown()

	switch recurrence {
	case RecurrenceOnce:
		if durationSet {
			resp.Diagnostics.AddAttributeError(
				path.Root("duration_minutes"),
				"duration_minutes cannot be set on a ONCE window",
				"Automox derives the duration of a ONCE window from dtstart to the UNTIL value "+
					"in rrule, and rejects the request when duration_minutes is supplied.\n\n"+
					"Remove duration_minutes, and set the window's end through UNTIL instead.",
			)
		}
		if rule := config.RRule.ValueString(); rule != "" && !config.RRule.IsUnknown() && !rruleOnce.MatchString(rule) {
			resp.Diagnostics.AddAttributeError(
				path.Root("rrule"),
				"Unsupported rrule for a ONCE window",
				"Automox accepts only FREQ=DAILY;UNTIL=<YYYYMMDDThhmmssZ> for a ONCE window, and "+
					"rejects COUNT and INTERVAL.\n\n"+
					"Got: "+rule+"\n"+
					"Example: FREQ=DAILY;UNTIL=20261201T020000Z",
			)
		}

	case RecurrenceRecurring:
		if rule := config.RRule.ValueString(); rule != "" && !config.RRule.IsUnknown() && !rruleRecurring.MatchString(rule) {
			resp.Diagnostics.AddAttributeError(
				path.Root("rrule"),
				"Unsupported rrule for a RECURRING window",
				"Automox accepts only FREQ=YEARLY with BYMONTH and BYDAY for a RECURRING window.\n\n"+
					"Got: "+rule+"\n"+
					"Example: FREQ=YEARLY;BYMONTH=1;BYDAY=+1MO for the first Monday of January.",
			)
		}
	}

	// A window covering no groups is legal and creates cleanly, but it protects
	// nothing — worth saying, since the failure is silent.
	if !config.GroupUUIDs.IsNull() && !config.GroupUUIDs.IsUnknown() && len(config.GroupUUIDs.Elements()) == 0 {
		resp.Diagnostics.AddAttributeWarning(
			path.Root("group_uuids"),
			"Window covers no server groups",
			"group_uuids is empty, so this window excludes nothing from patching. Reference the "+
				"uuid attribute of the automox_server_group resources it should protect.",
		)
	}
}
