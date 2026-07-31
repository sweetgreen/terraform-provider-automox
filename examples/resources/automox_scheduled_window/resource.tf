# A maintenance window is an exclusion: it prevents patching during the period
# it covers. window_type is always "exclude"; Automox accepts no other value.

resource "automox_scheduled_window" "holiday_freeze" {
  window_name        = "Holiday Change Freeze"
  window_description = "No patching over the holiday period"

  recurrence = "RECURRING"
  dtstart    = "2026-12-20T00:00:00Z"
  rrule      = "FREQ=YEARLY;BYMONTH=12;BYDAY=+3MO"

  # Permitted on a RECURRING window; rejected on a ONCE window, where Automox
  # derives the duration itself.
  duration_minutes = 10080

  # Windows identify groups by UUID, where every other endpoint uses integer
  # IDs. automox_server_group exposes both.
  group_uuids = [automox_server_group.workstations.uuid]
}
