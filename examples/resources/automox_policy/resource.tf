# A patch policy. Schedules can be written in plain terms rather than as the
# encoded integer Automox stores.

resource "automox_policy" "monthly_patching" {
  name          = "Monthly Patching"
  policy_type   = "patch"
  notes         = "Managed by Terraform"
  server_groups = [automox_server_group.workstations.id]

  schedule_time         = "03:00"
  schedule_days_of_week = ["saturday"]

  configuration = {
    patch_rule = "all"

    # Automox requires filter_type on every patch policy, not only when
    # patch_rule is "filter".
    filter_type = "include"
    filters     = []

    auto_patch  = true
    auto_reboot = false
    notify_user = true
  }
}

# A worklet. Automox calls this policy type "custom"; "worklet" is rejected.
resource "automox_policy" "disk_space_check" {
  name          = "Report Low Disk Space"
  policy_type   = "custom"
  server_groups = [automox_server_group.workstations.id]

  schedule_time         = "06:00"
  schedule_days_of_week = ["monday"]

  configuration = {
    # Required for worklets, and matched exactly: "windows" is rejected.
    os_family = "Windows"

    evaluation_code = <<-EOT
      $free = (Get-PSDrive C).Free / 1GB
      if ($free -lt 10) { exit 0 } else { exit 1 }
    EOT

    remediation_code = <<-EOT
      Write-Output "Low disk space on $env:COMPUTERNAME"
    EOT

    auto_reboot = false
  }
}
