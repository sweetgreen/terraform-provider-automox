# Every server group in the organization.
data "automox_server_groups" "all" {}

# One group by exact name. A name that matches nothing is an error rather than
# an empty list, so a typo fails the plan instead of producing a confusing
# index-out-of-range later.
data "automox_server_groups" "workstations" {
  name = "Corporate Workstations"
}

output "default_group_id" {
  # The default group is identified by being its own parent; the provider
  # derives is_default for you.
  value = one([for g in data.automox_server_groups.all.server_groups : g.id if g.is_default])
}
