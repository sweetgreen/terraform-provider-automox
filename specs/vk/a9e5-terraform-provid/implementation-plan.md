# Implementation plan (task a9e5-terraform-provid)

1. **Red tests first.** In `internal/services/resources/policy/plan_modifiers_test.go`
   add `TestUnitStatus*` cases covering the matrix in `technical-spec.md`. They call
   `statusUseStateForUnknown()`, which does not exist yet. To prove they fail on the
   current code, run them once against a temporary shim that returns
   `stringplanmodifier.UseStateForUnknown()`. The shim is a temporary edit and is
   not committed. Record the red output.
2. **Modifier.** Add `statusUseStateForUnknown()` to `plan_modifiers.go`:
   null-state, known-plan and unknown-config guards, then a group-emptiness
   transition check (plan vs state), then an `auto_patch` change check (config vs
   state, with null config meaning unchanged), then copy the state value.
3. **Wire it.** In `resource.go`, swap the `status` modifier and update
   `MarkdownDescription`. Update the schema-wiring test so that it asserts the
   concrete modifier type.
4. **Acceptance test.** In `resource_acceptance_test.go`, add
   `TestAccPolicy_StatusFollowsGroupsAndAutoPatch` (patch policy, no groups, then an
   empty test group with `auto_patch = true`, schedule_days 0). Compile it with
   `go vet`, but do not run it: running it writes to Automox.
5. **Docs.** Regenerate or hand-edit `docs/resources/policy.md` for the `status`
   description. Run `make docs` if tfplugindocs works offline, and otherwise edit by
   hand.
6. **Changelog.** Add `## 0.1.9 (unreleased)` with a Fixed entry. The tag is
   pushed after merge. It is not pushed from this branch.
7. **Verify.** Run `go build ./...`, `go vet ./...`, `go test ./... -count=1`,
   `gofmt -l .`, and golangci-lint v2 (installed via `go run` if it is not on PATH).
8. **Review.** Run a Codex review of the diff and iterate.
9. **Knowledge.** Add a topic page in `terraform-infrastructure/.project-knowledge`
   on what drives `status`, refresh `INDEX.md`, and commit on the task branch
   there.
10. **PR.** Open a PR against `main` in `terraform-provider-automox` that notes the
    terraform-infrastructure pin (`0.1.6`, in root `automox.hcl`). Do not merge it,
    because David merges.
