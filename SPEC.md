# Technical Specification: Repair Go Lint Action Pin

## Problem

The `Go Lint` GitHub Actions workflow references a commit that does not exist in
`golangci/golangci-lint-action`. GitHub cannot resolve the action during job
setup, so the workflow fails before checking out or linting the repository.

## Objective

Restore the workflow by pinning `golangci/golangci-lint-action` to the immutable
commit referenced by its `v9.0.0` tag, while preserving the repository's pinned
action and trailing-version-comment conventions.

## Scope

- Verify the upstream `v9.0.0` tag's full commit SHA from an authoritative
  source.
- Replace only the invalid `golangci-lint-action` action reference in
  `.github/workflows/go-lint.yml`.
- Retain `# v9.0.0` and the configured golangci-lint version `v2.6.0`.
- Run the workflow-equivalent linter locally and validate the workflow syntax.
- Confirm the resulting pull-request workflow run succeeds before merge.

## Non-goals

- Upgrading the action or golangci-lint beyond the versions already selected.
- Changing lint configuration, runner selection, workflow triggers, or provider
  implementation code unless a newly surfaced lint failure requires a focused
  correction.
- Addressing the unrelated Go standard-library CVE failure tracked separately.

## Requirements

1. The action reference MUST use the full commit SHA to which upstream tag
   `v9.0.0` resolves.
2. The line MUST retain the trailing `# v9.0.0` comment.
3. The workflow MUST remain least-privileged with `contents: read`.
4. `golangci-lint` v2.6.0 MUST report no findings for the repository.
5. The pull request's `Go Lint` check MUST complete successfully before merge.

## Verification

- Resolve `refs/tags/v9.0.0` from the upstream repository and record the full
  commit SHA.
- Inspect the workflow diff to ensure no unrelated workflow behavior changed.
- Run golangci-lint v2.6.0 using the repository configuration.
- Validate the YAML/workflow structure with available local tooling.
- Review the final diff independently and address all significant findings.
- Open a pull request against the repository's base branch, observe green CI,
  and merge it.

## Risks and Mitigations

- **Hidden lint failures:** the workflow has never executed. Run the exact pinned
  linter locally before opening the pull request and fix only confirmed findings.
- **Incorrect lightweight/annotated tag resolution:** verify the tag through an
  authoritative upstream Git reference or API and use its complete commit ID.
- **Unrelated CI failure:** distinguish the Go Lint check from separately tracked
  repository failures and do not expand this task's scope.
