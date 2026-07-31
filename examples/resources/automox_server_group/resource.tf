# Server groups are how Automox targets devices: policies attach to groups
# rather than to individual endpoints.

data "automox_server_groups" "default" {
  # The organization's default group has an empty name; the console shows it as
  # "Default". Every other group is parented to it.
  name = ""
}

resource "automox_server_group" "workstations" {
  name                   = "Corporate Workstations"
  parent_server_group_id = data.automox_server_groups.default.server_groups[0].id
  refresh_interval       = 1440
  ui_color               = "#059F1D"
  notes                  = "Managed by Terraform"

  # enable_os_auto_update and enable_wsus are three-state. Leaving one unset
  # means each device keeps its current behaviour, which is NOT the same as
  # setting it to false and actively changing every device in the group.
  enable_os_auto_update = false
}
