# Technical Specification: Ignore the whitespace Automox trims from policy scripts

## Problem

Automox trims policy script fields when it saves them. Provider v0.1.6 writes the
API's value straight into state, so a configured script that ends in a newline
(normal for `file()`) is read back one or more characters shorter than planned.
Terraform then fails the apply with "Provider produced inconsistent result after
apply" and taints a policy that was in fact created (terraform-infrastructure
#1804: policy 1026975 tainted, replaced by 1026979).

Affected attributes, all `Optional + Computed` with `UseStateForUnknown`, nested
under `automox_policy.configuration`:

- `evaluation_code`
- `remediation_code`
- `installation_code`

terraform-infrastructure #1811 works around it with `chomp()` in
`modules/automox/worklet-policy`, which only protects module callers, only
removes newlines, and does not cover `installation_code`.

## What Automox actually trims (measured 2026-10-12)

Probed live in the Sweetgreen-Test organization (183625) by creating a worklet
and a required-software policy with no server groups and no schedule, PUTting
each candidate script, reading it back, and deleting the policy.

- Trimming happens on **both ends**, repeatedly, on create and update, for all
  three script fields (and for `notes`; see Out of scope).
- Interior characters are never touched: `"a \nb\t\n c"` and `"exit​ 0"`
  round-trip unchanged.
- A script that is only whitespace is rejected with HTTP 400.
- The trimmed set matches Laravel's `Str::trim` (the `TrimStrings` middleware):
  - ASCII: space, `\t`, `\n`, `\v`, `\f`, `\r`, NUL.
  - PCRE `\s` in UCP mode: U+0085, U+00A0, U+1680, U+2000–U+200A, U+2028,
    U+2029, U+202F, U+205F, U+3000.
  - Laravel's `INVISIBLE_CHARACTERS`: U+00AD, U+034F, U+061C, U+115F, U+1160,
    U+17B4, U+17B5, U+180E, U+2000–U+200F, U+202F, U+205F, U+2060–U+2065,
    U+206A–U+206F, U+2800, U+3000, U+3164, U+FEFF, U+FFA0, U+1D159,
    U+1D173–U+1D17A, U+E0020.
- Confirmed **not** trimmed: U+001C, U+001F, U+0001, U+000E, U+001B, U+007F,
  U+2066, U+2069, U+FFF9, U+E0001, U+E0021, U+E000, U+0600, U+17B6, U+1D17B.

Every member of the set above was either probed directly or predicted from the
Laravel list and then confirmed (U+2800, U+1D159, U+E0020, U+17B4, U+2064,
U+2065, U+1160, U+1D17A, U+206A all trimmed; U+2069, U+1D17B, U+E0021 kept).

## Requirements

- **FR-1** The three script attributes use a custom string type whose semantic
  equality treats two values as equal when they are identical after removing
  the Automox trim set (above) from both ends.
- **FR-2** When the API value is semantically equal to the planned (create,
  update) or prior (read) value, the planned/prior value is kept in state, so
  the apply succeeds and the next plan shows no changes.
- **FR-3** Any difference other than leading/trailing trimmed characters is
  still a diff: interior changes, additions, and trailing characters Automox
  keeps (for example U+007F).
- **FR-4** The trim set is exactly the measured one: never trim more than the
  API does.
- **FR-5** Existing state written by v0.1.6 loads without a schema version bump
  or state upgrade; values are still strings.
- **FR-6** Everything else that handles configuration values (request body
  construction, API-to-state conversion, validators) treats the custom type as
  a string.
- **FR-7** Docs for the three attributes say that leading/trailing whitespace is
  ignored and why.

## Design

- New `scriptStringType` / `scriptStringValue` in
  `internal/services/resources/policy/script.go`, embedding
  `basetypes.StringType` / `basetypes.StringValue` and implementing
  `basetypes.StringValuableWithSemanticEquals`. Helper `automoxTrim(string)`
  with the explicit rune set (not `unicode.IsSpace`, which differs from Automox
  on U+180E, U+200B–U+200F, U+FEFF, NUL and others).
- `configuration.go`: set `CustomType: scriptStringType{}` on the three
  attributes. The plan modifier `UseStateForUnknown` keeps working because the
  framework converts through `StringValuable`.
- `convert.go`: `nullOf` and `goToAttr` build values via
  `basetypes.StringTypable` so custom string types round-trip.
- `state.go` `attrToGo` and `validators.go` `stringOf`: accept any
  `basetypes.StringValuable`.
- The framework applies semantic equality to nested attributes after Create,
  Update and Read, so no CRUD changes are needed.

Rejected alternative: preserving the planned/prior value by hand in
Create/Update/Read. It duplicates what the framework already does, needs three
call sites kept in sync, and is easier to get wrong on import.

## Known limitation: import

Import has no prior value, so state gets the trimmed API value. A configuration
whose script ends in a newline then plans one in-place update of that
attribute; applying it writes the same trimmed text and keeps the configured
value in state, after which plans are empty. Terraform requires a planned value
to equal a non-null configured value, so a provider cannot hide this.
The acceptance test checks the imported values equal the trimmed script.

## Testing

- Unit (`script_test.go`): equality for LF, CRLF, multiple newlines, spaces and
  tabs, leading whitespace, BOM and other invisible characters, no newline;
  inequality for body changes, interior whitespace changes and kept characters
  (U+007F, U+2066); type conversion and the `configurationFromAPI` /
  `configurationToAPI` round trip with the custom type.
- Acceptance (`worklet_acceptance_test.go`): create a worklet whose
  `evaluation_code` ends in `\n` and `remediation_code` ends in `\r\n`; assert
  state keeps the configured values; re-plan is empty; import gives the
  trimmed values. The same for `installation_code` on a required-software
  policy. Run against the Sweetgreen-Test organization.

## Release and rollout

- CHANGELOG `0.1.7`, tag `v0.1.7` after merge (the tag push triggers the
  release workflow).
- terraform-infrastructure: bump `automox.hcl` to `0.1.7`. Every Automox unit
  in both organizations must plan "No changes"; stop on any diff. Keep the
  module's `chomp()` as a backstop.

## Out of scope

- `notes` (and probably `name`) are trimmed the same way by Automox. They are
  not part of this change; record it as a follow-up.
