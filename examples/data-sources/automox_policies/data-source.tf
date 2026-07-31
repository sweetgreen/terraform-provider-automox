data "automox_policies" "worklets" {
  # "custom" is the wire name for a worklet.
  policy_type = "custom"
}

output "worklet_names" {
  value = [for p in data.automox_policies.worklets.policies : p.name]
}
