# Prior knowledge: automox_policy status flip (task a9e5-terraform-provid)

Source: `terraform-infrastructure/.project-knowledge` (39 topic pages, read-only),
plus the provider repo's own `PRIOR_KNOWLEDGE.md`, `SPEC.md` and CHANGELOG.
Searched for automox, provider, status, server_count, inconsistent, release,
registry, atlantis.

## Directly relevant

- **automox-worklet-trailing-newline**: the same class of failure, "Provider
  produced inconsistent result after apply". The write lands in Automox and the
  apply still fails. Fixed in provider 0.1.7 with semantic equality. That
  precedent is to fix this in the provider, not with a workaround in the
  callers.
- **Provider CHANGELOG / `serverCountUseStateForUnknown`**: `server_count` hit
  the same shape on terraform-infrastructure#1687 (groups `[]` to one group).
  The fix was a custom modifier that leaves the value unknown when
  `server_groups` changes. The test pattern it established was to build
  `tfsdk.State/Plan` from `flatten(apiPolicy)` and call the modifier directly.
  This task copies that pattern.
- **terraform-registry-github-token**: the private registry serves releases by
  their `v${version}` tag and hides pre-release-flagged releases. A cold
  `tofu init` is the real test that a new version resolves. A release is
  therefore a `v0.1.9` tag, built by goreleaser in CI.
- **automox-test-organization**: production org `120547`, test org `183625`.
  API keys are org-scoped. The key in this environment reads 120547 and gets
  403 on 183625. The provider pin lives in the root `automox.hcl` (`0.1.6` at
  the time of the failure), so a bump plans every Automox unit in both orgs.
- **atlantis-apply-workflow**: approve, then `atlantis apply`, then merge. This
  task does none of those (David merges).

## Background

- **automox-retail-patching**: BOH rings are one policy per group at 03:00 PT,
  with phases. #2149 attached the phase-3 ring group and enabled `auto_patch`
  in one change. That is the normal way a staged ring goes live, so the
  failure will recur on every ring activation until the provider is fixed.
- **automox-unassigned-policy-lifecycle**: policies with no groups and
  `auto_patch = false` exist on purpose, as templates and staged rings. Those
  are exactly the policies that later flip.
- **automox-organization-wide-worklet-safety**: acceptance tests must target an
  empty, test-created group and must not be able to run on devices.

## Gaps

No page records what drives `status`. The read-only survey in
`technical-spec.md` is new knowledge, and stage 12 records it.
