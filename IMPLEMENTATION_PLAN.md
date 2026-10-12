# Implementation Plan: Ignore the whitespace Automox trims from policy scripts

See `SPEC.md` for the measured trim set and requirements.

## Provider (sweetgreen/terraform-provider-automox, branch `vk/282a-automox-provider`)

1. **Script type** — add `internal/services/resources/policy/script.go`:
   - `automoxTrim(s string) string`: `strings.TrimFunc` with `isAutomoxTrimmed`,
     an explicit rune switch for the measured set (ASCII whitespace + NUL, the
     PCRE UCP `\s` code points, Laravel's invisible characters).
   - `scriptStringType` embedding `basetypes.StringType`: `Equal`, `String`,
     `ValueType`, `ValueFromString`, `ValueFromTerraform`.
   - `scriptStringValue` embedding `basetypes.StringValue`: `Equal`, `Type`,
     `StringSemanticEquals` comparing `automoxTrim` of both sides.
   - Constructors `scriptStringNull()` / `scriptStringValueOf(s)`.
2. **Schema** — `configuration.go`: `CustomType: scriptStringType{}` on
   `evaluation_code`, `remediation_code`, `installation_code`; extend their
   descriptions with the trimming note.
3. **Conversion** — `convert.go`: `nullOf` / `goToAttr` handle
   `basetypes.StringTypable` that is not the plain type by going through
   `ValueFromString`. `state.go` `attrToGo` and `validators.go` `stringOf`:
   accept `basetypes.StringValuable`.
4. **Unit tests** — `script_test.go`: table test for semantic equality and
   `automoxTrim`; type round-trip (`ValueFromTerraform`, `ToTerraformValue`);
   `configurationFromAPI` produces the custom type for the three attributes and
   `configurationToAPI` sends the configured string unchanged (untrimmed).
   Update any existing test that asserts on these attributes' Go types.
5. **Acceptance tests** — `worklet_acceptance_test.go`:
   `TestAccPolicy_ScriptTrailingNewline` (worklet: `\n` and `\r\n`, state keeps
   configured values, plan-only empty step, import with `ImportStateCheck`
   asserting trimmed values and `ImportStateVerifyIgnore` for the two scripts)
   and a required-software variant for `installation_code`. Run with
   `TF_ACC=1` against org 183625 using the Test key.
6. **Docs** — `make docs` equivalent (`tfplugindocs generate`) to refresh
   `docs/resources/policy.md`.
7. **CHANGELOG** — `## 0.1.7 (2026-10-12)` Fixed entry.
8. **Verify** — `go build ./...`, `go vet ./...`, `go test ./... -count=1`,
   `golangci-lint run` if available, acceptance tests above plus the existing
   policy acceptance tests touching scripts.
9. **Review** — Codex review loop until no significant findings.
10. **Ship** — commit, push, PR, wait for checks, merge, tag `v0.1.7` on the
    merge commit, push tag, confirm the release workflow publishes and the
    registry serves 0.1.7 (cold `tofu init`).

## terraform-infrastructure (branch `vk/282a-automox-provider`)

11. Bump `automox.hcl` provider version `0.1.6` → `0.1.7`. Keep the module's
    `chomp()` (comment updated to say it is now a backstop).
12. Plan every Automox unit in both orgs (locally if credentials allow, and via
    Atlantis on the PR). Expect "No changes" everywhere; stop on any diff.
13. PR → Atlantis plan → approve (sg-infra-machine) → `atlantis apply` → merge.

## Knowledge base

14. Update `automox-worklet-trailing-newline.md` with the measured trim set and
    the v0.1.7 fix; tag with `282a-automox-provider`; refresh `INDEX.md`;
    commit with the infra change.

## Risks

- The framework must apply semantic equality to attributes nested in a
  `SingleNestedAttribute`. It does (terraform-plugin-framework ≥ 1.3); the
  acceptance test proves it end to end.
- A custom type changes `configurationAttrTypes()`; every place that switches
  on `basetypes.StringType` / `types.String` must be updated or values silently
  become null. Covered by the round-trip unit test.
