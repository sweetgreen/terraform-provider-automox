# Implementation Plan: Repair Go Lint Action Pin

1. Inspect repository guidance, the current workflow, branch state, and available
   validation tooling.
2. Verify the complete upstream commit referenced by
   `golangci/golangci-lint-action` tag `v9.0.0` using an authoritative GitHub
   reference.
3. Refresh the SpecKit project constitution and generate the feature spec,
   clarification record, technical plan, tasks, and consistency analysis.
4. Update `.github/workflows/go-lint.yml` to use the verified full SHA without
   changing the version comment or any other workflow behavior.
5. Run the exact configured golangci-lint v2.6.0 against the repository and run
   available workflow/YAML validation.
6. Review the diff with an independent Codex CLI invocation, address confirmed
   significant findings, and repeat validation/review until clean.
7. Add reusable action-pin and setup-failure diagnostic knowledge to the project
   knowledge base, tag it for this task, refresh its index, and commit it.
8. Commit the implementation, push the branch, open a pull request against the
   detected base branch, monitor the Go Lint check to green, and merge the pull
   request.

## Dependencies and parallelism

- Upstream SHA verification and local tooling inspection can be performed in the
  same research phase.
- The workflow edit depends on verified tag resolution and completed SpecKit
  analysis.
- Validation depends on the workflow edit; independent review depends on the
  complete diff.
- Knowledge capture depends on the final reviewed behavior.
- Pull-request creation depends on committed changes; merge depends on required
  CI completion.
