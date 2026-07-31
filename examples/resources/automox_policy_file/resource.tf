# A file a worklet needs at run time — an installer, or a script it calls.

resource "automox_policy_file" "installer" {
  policy_id = automox_policy.disk_space_check.id
  filename  = "collect.ps1"
  content   = file("${path.module}/scripts/collect.ps1")
}

# Automox cannot return a file's contents, so Terraform records the hash of what
# it uploaded. Changing `content` replaces the file; a change made in the Automox
# console is invisible to Terraform.
output "installer_hash" {
  value = automox_policy_file.installer.content_sha256
}
