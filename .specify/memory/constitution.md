# Terraform Provider Automox Constitution

## Core Principles

### I. Small, reviewed changes

Every change lands through a focused feature branch and pull request. Do not mix
unrelated provider, dependency, workflow, or release changes. Preserve existing
behavior unless the specification explicitly requires a change.

### II. Immutable and accurate supply-chain references

Third-party CI actions and build inputs must be pinned to immutable identifiers.
Version comments must accurately describe pinned action commits, and a pin must
be verified against the authoritative upstream reference before it is changed.

### III. Reproducible Go validation

The Go version, tool versions, modules, and lint configuration used by CI must be
represented in the repository. Local verification should use the same versions
and configuration as CI so failures are reproducible before a pull request is
merged.

### IV. Least privilege and secure defaults

Workflows and provider behavior use only the permissions they require. Security
controls, immutable action pins, and dependency scanning are preserved unless a
reviewed replacement provides equivalent or stronger protection.

### V. Evidence before merge

Acceptance criteria must be concrete and checkable. Relevant formatting, tests,
lint, workflow validation, and independent diff review must complete before
merge. External checks that cannot run locally must be observed on the pull
request.

### VI. Checked-in project knowledge

Non-obvious failure modes, operational constraints, and reusable implementation
findings must be recorded in the project knowledge base after the implementation
is verified. Avoid filler and keep task-specific narrative in the task artifacts.

## Quality Gates

- Run formatting, unit tests, and static analysis appropriate to changed files.
- For workflow-only changes, validate syntax and run the underlying tool locally
  at the CI-configured version.
- Review the exact diff for accidental behavior or permission changes.
- Resolve significant findings from an independent review.
- Require the affected pull-request check to succeed before merge.

## Governance

This constitution governs feature specs, plans, task lists, implementation, and
review. Conflicts must be corrected before implementation or explicitly recorded
as unresolved. Amendments must explain the changed rule and update the version.

**Version**: 1.0.0  
**Ratified**: 2026-09-02  
**Amended**: 2026-09-02
