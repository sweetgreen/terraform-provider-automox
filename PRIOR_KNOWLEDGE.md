# Prior Knowledge: Automox script trimming (task 282a-automox-provider)

Source: `terraform-infrastructure/.project-knowledge` (read-only). Pages searched
for automox, provider, worklet, newline, release, atlantis, registry.

## Directly relevant

- **automox-worklet-trailing-newline** (`23e5-add-automox-test`): Automox drops
  the final newline from `evaluation_code` / `remediation_code`; a `file()`
  script ending in `\n` fails with "Provider produced inconsistent result after
  apply" and leaves the policy tainted (#1804, policy 1026975 → 1026979).
  `modules/automox/worklet-policy` now `chomp()`s both scripts; every existing
  worklet unit planned "No changes" after that. Advice: wrap scripts in
  `chomp()` outside the module. This page should be updated once v0.1.7 ships
  (the provider fix makes it unnecessary, and the API trims far more than one
  newline; see SPEC.md).
- **automox-test-organization**: Sweetgreen-Test org is `183625`
  (`environments/automox/sweetgreen-test/`), production is `120547`
  (`environments/automox/sweetgreen/`). API keys are org-scoped: a key used on
  the other org returns 403. Test key: 1Password Homelab / "Automox Terraform
  Provider" / Test. `automox.hcl` picks the key via `automox_api_key_env` in
  `account.hcl`. A provider bump in `automox.hcl` therefore plans units in
  both orgs.
- **atlantis-apply-workflow**: approve → `atlantis apply` → merge. Apply is
  refused until the PR is approved; Atlantis does not apply on merge here, so
  merging first causes drift. Locks are per project.
- **terraform-registry-github-token**: the private registry
  (`registry.sweetgreen.engineering`) serves releases by `v${version}` tag and
  hides pre-release-flagged releases. A cold `tofu init` is the real test that
  a new version resolves.

## Background

- **automox-worklet-os-family**: `os_family` is case-sensitive (`Windows`,
  `Mac`, `Linux`) and caller-owned in the module.
- **automox-cross-state-unit-migration**: Automox units run .tf files in place
  (no `terraform { source }`), so a plan of every unit is the bump's proof.
- **automox-organization-wide-worklet-safety**: worklets run code on endpoints;
  acceptance tests and probes must target no devices and have no schedule.

## Gaps

No page covers the provider repository itself (release flow, acceptance test
envelope, semantic-equality patterns). The provider repo has no knowledge base
of its own.
