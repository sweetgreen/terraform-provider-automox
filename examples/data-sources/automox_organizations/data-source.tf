data "automox_organizations" "all" {}

# How close the organization is to its licensed device limit.
output "device_headroom" {
  value = {
    for o in data.automox_organizations.all.organizations : o.name
    => o.device_limit - o.device_count
  }
}
