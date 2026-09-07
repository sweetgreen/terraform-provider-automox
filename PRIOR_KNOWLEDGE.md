# Prior Knowledge: Repair Go Lint Action Pin

## Knowledge-base search

No project knowledge base exists yet in `terraform-provider-automox`; this is the
first task in this checkout with reusable project knowledge to consult.

A sibling repository's knowledge base was searched for `golangci`, GitHub
Actions, workflow, Automox, and lint references. It contains an unrelated
Automox infrastructure migration page but no guidance relevant to this
provider's Go lint workflow or action pinning, so it was not used as project
knowledge.

## Applicable context supplied with this task

- The workflow has failed during job setup on every run because the referenced
  action commit cannot be resolved.
- The intended action release is `golangci/golangci-lint-action` v9.0.0; action
  pins should remain full immutable SHAs with trailing version comments.
- The configured linter is golangci-lint v2.6.0.
- The failure predates and is independent of PR #9.
- A separate Go standard-library CVE failure is out of scope.

## Implications for specification and planning

- Verify the tag target independently before editing.
- Keep the change narrowly limited to the invalid pin unless the exact CI linter
  reveals real findings.
- Test the linter locally because resolving the action will expose its output for
  the first time.
- Record the setup-failure diagnostic pattern in the new project knowledge base
  after the change ships.
