# automox_device_group_assignment moves a device that already has the agent
# installed into a group Terraform manages, without reinstalling the agent and
# without moving it by hand in the console.

resource "automox_server_group" "pos_ring_1" {
  name                   = "POS Ring 1"
  refresh_interval       = 1440
  parent_server_group_id = 230685
}

resource "automox_device_group_assignment" "register_1" {
  # The Automox server ID of a device the agent already registered.
  device_id       = 5818967
  server_group_id = automox_server_group.pos_ring_1.id
}
