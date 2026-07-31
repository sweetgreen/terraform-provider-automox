# Reading the whole fleet is a large read, so filter where you can. group_id is
# applied by the API and reduces how much is fetched; the rest narrow the
# results.
data "automox_devices" "windows_in_group" {
  group_id  = automox_server_group.workstations.id
  os_family = "Windows"
  connected = true
}

output "devices_needing_reboot" {
  value = [
    for d in data.automox_devices.windows_in_group.devices : d.display_name
    if d.needs_reboot
  ]
}
