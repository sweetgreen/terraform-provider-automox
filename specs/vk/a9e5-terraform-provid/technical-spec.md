# Technical spec: `automox_policy.status` must not be promised across a change that flips it

Task: `a9e5-terraform-provid`. Repo: `sweetgreen/terraform-provider-automox`.

## Problem

`status` is `Computed` with `stringplanmodifier.UseStateForUnknown()`. Every
update plans `status` as the prior state value. Automox derives `status` from
the policy's configuration, so an update that changes the driving input returns
a different `status` and Terraform rejects the apply after the write has
already landed:

```
module.patch.automox_policy.this["retail_boh_windows_11_windows_updates_policy_phase_3"]
Error: Provider produced inconsistent result after apply
  .status: was cty.StringVal("inactive"), but now cty.StringVal("active").
```

Seen 2026-11-10 on terraform-infrastructure#2149 (provider 0.1.6), policy
726918: `server_groups` `[] -> [446992]` and `configuration.auto_patch`
`false -> true` in one apply.

## What drives `status` (read-only survey, org 120547, 2026-11-17)

All 42 production policies (32 `patch`, 10 `custom`) were listed with
`GET /api/policies?o=120547`:

| policy type | `auto_patch` | groups | `schedule_days` | status | count |
|---|---|---|---|---|---|
| patch | true | non-empty | non-zero | active | 29 |
| patch | true | non-empty | **0** (778510) | active | 1 |
| patch | false | **non-empty** (554218) | non-zero | inactive | 1 |
| patch | false | empty (714954) | non-zero | inactive | 1 |
| custom | (absent) | empty or non-empty | 0 or non-zero | active | 10 |

Conclusions:

- For patch policies `status == (auto_patch ? "active" : "inactive")` on every
  policy. `auto_patch` is the evidenced driver.
- A zero schedule does not make a policy inactive (778510, five worklets).
  Not covered by the modifier.
- Server groups alone do not decide it (554218 has groups and is inactive). No
  production policy has `auto_patch = true` with no groups, so the
  empty-to-non-empty transition cannot be ruled out from read-only data. The
  task asks for it to be covered and doing so is cheap, so it is covered.
- Worklets are always `active`.
- The test org (183625) was not readable with the available key (403).

## Design

A custom `planmodifier.String`, `statusUseStateForUnknown()`, in
`plan_modifiers.go`, modelled on `serverCountUseStateForUnknown`:

1. Create (null state): no-op, value stays unknown.
2. Plan value already known, or config value unknown: no-op.
3. `server_groups`: if the plan list is unknown, or its emptiness differs from
   the state list's emptiness, leave `status` unknown. A change between two
   non-empty lists keeps the prior value, because the survey shows group
   membership does not drive `status` and that change is common (ring moves).
4. `configuration.auto_patch`: leave `status` unknown when it is changing.
   `req.Plan` is the pre-modifier plan, so an unconfigured `auto_patch` shows
   as unknown there even though its own `UseStateForUnknown` will keep the
   state value. The modifier therefore reads `auto_patch` from **config**:
   - config `configuration` unknown, or config `auto_patch` unknown: changing.
   - config `auto_patch` null: not changing (its modifier keeps state).
   - config `auto_patch` known: changing if it differs from state (state null
     counts as different).
5. Otherwise copy the state value into the plan.

`status` stays `Computed` only. No `lifecycle { ignore_changes }` anywhere.

## Tests

`plan_modifiers_test.go`, offline, driving the modifier with real
`tfsdk.State/Plan/Config` built from the schema:

- unchanged policy: prior `status` kept
- groups `[] -> [x]`: unknown
- groups `[x] -> []`: unknown
- groups `[x] -> [y]`: prior kept
- `auto_patch` `false -> true` and `true -> false`: unknown
- `auto_patch` omitted in config while another field changes: prior kept
- unrelated field (`notes`, `schedule_time`) changed: prior kept
- the #2149 shape (groups and `auto_patch` both change): unknown
- create: stays unknown
- schema wiring: `status` uses the new modifier, not the generic one

Each must fail against the current code: the "unknown" cases fail because
`UseStateForUnknown` copies state. The wiring test fails because the attribute
carries the generic modifier.

An acceptance test, `TestAccPolicy_StatusFollowsGroupsAndAutoPatch`, goes in
`resource_acceptance_test.go`: a patch policy is created with `server_groups = []`
and `auto_patch = false`, then given a test-created empty group with
`auto_patch = true`. The test asserts the apply succeeds and that `status` is
`active`. It runs only under `TF_ACC` with the existing production-org guard.

## Release

Version 0.1.9, the next patch after 0.1.8, which is already tagged. The
CHANGELOG entry goes under `## 0.1.9`. terraform-infrastructure pins 0.1.6 in
the retail BOH unit, and bumping that pin is a separate PR.
