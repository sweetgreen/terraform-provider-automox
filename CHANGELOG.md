# Changelog

## 0.1.1 (2026-08-03)

### Fixed

- Existing policies can now be brought under Terraform management. Two defects
  made most of a real organization's policies impossible to import, and neither
  was visible until the provider was pointed at policies it had not created
  itself. Of the 26 policies in the organization this was tested against, 16
  could not be managed at all.

  A policy that targets no server groups was unmanageable. Automox returns an
  explicit null rather than an empty list for those, which became a null in
  Terraform, and `server_groups` is a required attribute — so importing one
  failed asking for a value that no configuration could supply, because the
  rejected value came from reading the policy rather than from anything written
  by hand. Such a policy now reads as an empty list and states it plainly, as
  `server_groups = []`.

  Patch policies using the advanced patch rule were rejected outright. Automox
  records `filter_type = "all"` for them, and the provider accepted only
  `include`, `exclude`, and `severity` — values inferred from policies the
  provider had itself created, which never produce `all`. Because the attribute
  is read back as well as written, the provider refused these policies on import
  and not only on write.

## 0.1.0 (2026-07-31)

### Security

- All seven vulnerabilities reachable from provider code are resolved: gRPC is
  upgraded past an authorization bypass that sat directly beneath the plugin
  server, `golang.org/x/net` and `golang.org/x/text` are upgraded, and the Go
  floor is raised to 1.26.5 to pick up standard library fixes in `crypto/tls`,
  `crypto/x509`, and `net/textproto`. `govulncheck` reports none remaining.

  Dependabot reports a larger number because it flags any module present in
  `go.sum` without checking whether the code can reach it. The remainder come
  from `terraform-plugin-testing`, which pulls in `go-git`, `ProtonMail/go-crypto`,
  `circl`, `hc-install`, and `terraform-exec` so that it can install Terraform and
  clone modules during acceptance runs. None of them ship: the released binary
  embeds 24 modules and none is from that tree, so the exposure is limited to CI
  runners executing acceptance tests, not to anyone using the provider.

### Fixed

- A rate-limited request could stall an apply indefinitely. `Retry-After` comes
  from the server and bypassed the provider's own backoff ceiling, so a header of
  `86400` would have slept for a day — three times over before giving up — with
  Terraform printing nothing meanwhile, which is indistinguishable from a hang.
  Automox's documented one-minute penalty is still honoured; anything beyond five
  minutes is now reported instead of waited out.

- Acceptance tests could skip silently when the write-scope probe failed for a
  reason unrelated to permissions. A transient outage during the probe would have
  turned every write test into a no-op while the suite reported success. Only a
  refused write now counts as a read-only credential; anything else fails.

### Added

- A static guard asserting every acceptance test refuses to run without
  `TF_ACC`. These tests create real objects in a live Automox organization, so a
  new one that forgot the check would mean continuous integration writing to
  production on every pull request rather than merely a failing test.

- `automox_policy_file` resource for the files a worklet needs at run time — an
  installer, or a script it calls.

  Automox provides no way to read a file's contents back, so Terraform records
  the hash of what it uploaded. Changing `content` replaces the file; a change
  made in the Automox console is invisible to Terraform, and the documentation
  says so rather than implying a fidelity the API cannot support. Every attribute
  replaces the file, because Automox rejects a second upload under a name already
  present — an update is a delete and an upload either way.

- Documentation for every resource and data source, generated from the provider
  schemas and published to `registry.sweetgreen.engineering`, with runnable
  examples for the provider, each resource, and the common data sources.
  Continuous integration regenerates the docs and fails on any difference, so
  what practitioners read cannot drift from what the provider accepts.

- Data sources `automox_scheduled_windows` and `automox_data_extracts`.

  Maintenance windows are served by a different system from the rest of the API:
  the collection is POST-only, returns a fourth envelope shape, and takes its
  paging from the request body — query parameters are accepted and ignored. The
  provider handles that, and stops with an explicit error if the endpoint ever
  stops advancing rather than returning duplicated or partial results. No filter
  arguments are offered because the endpoint accepts filters and ignores them.

  Data extracts report status and the period each covers. The download URL is
  deliberately not exposed: for a completed extract it is a pre-signed link to an
  export of the organization's data, and state is plaintext.

- Data sources `automox_needs_attention_report` and `automox_prepatch_report`:
  Automox's compliance and pre-patch views, with severity breakdowns, the devices
  involved, and — for each device — the policies it is failing or the patches
  waiting for it, including their CVEs.

  These describe the fleet at a moment rather than configuration, so referring to
  one from a resource argument makes that resource change whenever the fleet
  does. The prepatch report is the largest read the provider offers, around a
  megabyte for a fleet of 1,500, and all of it is written to Terraform state.

- Data sources `automox_events`, `automox_worklets`, and `automox_worklet`.
  Events can be filtered by type, device, or policy; worklets search the Automox
  Worklet Catalogue by text, OS family, or category.

  The event log has no natural end — it runs to tens of thousands of records —
  so the read is bounded. It returns the most recent `max_results` events,
  default 250, and sets `truncated` when more matched than were returned, rather
  than handing back a prefix that looks like the whole log.

  An event's free-form `data` object is not exposed. What it holds depends on the
  event type: device addresses on patch events, employee names on user events,
  and nothing bounds what a future event type adds.

- Data sources `automox_devices`, `automox_device`, and
  `automox_device_packages`, for reading the fleet. Devices can be filtered by
  server group, OS family, connection state, and tag; `automox_device_packages`
  reports installed software and outstanding patches with their CVEs and CVSS
  scores.

  These read real endpoints, so what they expose is limited on purpose. The
  logged-in user, serial number, service tag, private addresses, and the hardware
  `detail` object together identify whose laptop a device is, and Terraform state
  is plaintext — so none of them are read. Attributes policy `device_filters` can
  match on, including `ip_addrs`, `organizational_unit`, and `tags`, are
  included, because configuration acts on those.

  Filter by `group_id` where you can: it is applied by the API, so it reduces how
  much is fetched rather than just what is returned.

- Data sources `automox_organizations`, `automox_server_groups`,
  `automox_policies`, and `automox_policy_stats`, for reading an organization
  without managing it. Server groups and policies can be filtered by exact name,
  and policies by kind; a filter that matches nothing fails and says so rather
  than returning an empty list to index into.

  Two things are deliberately not exposed. Automox returns a live organization
  access key from its organizations endpoint, and policy listings carry worklet
  source code — every data source attribute is written to Terraform state in
  plaintext, so neither is read at all. Marking them sensitive would still put
  them in state.

  `automox_server_groups` reports which group is the organization default.
  Automox marks it by making the group its own parent rather than with a flag,
  so the provider derives it for you.

- Release pipeline. Tagging `vX.Y.Z` builds, signs, and publishes a release that
  `registry.sweetgreen.engineering` can serve, for linux, macOS, and Windows on
  amd64 and arm64. Signing material is read from AWS SSM using the runner's own
  instance credentials, so no private key is copied into this repository.
- Continuous integration on pull requests: build, vet, gofmt, unit tests, a
  `go mod tidy` check, and a vulnerability scan. Acceptance tests are excluded
  from CI by design — they create real objects in a live Automox organization.

- `automox_scheduled_window` resource for maintenance exclusion windows, with
  full create, read, update, delete, and import.

  Windows are scoped by organization UUID and identify server groups by UUID,
  where every other endpoint uses integer IDs; the provider resolves both, so
  configuration references an `automox_server_group` directly through its `uuid`
  attribute. Recurrence rules are validated before the request, since Automox
  accepts only a narrow subset of RFC 5545 and reports violations with a message
  naming a field the practitioner never wrote.

- `automox_policy` resource covering all three Automox policy kinds — patch,
  worklet, and required software — with full create, read, update, delete, and
  import.

  Schedules can be written in plain terms: `schedule_days_of_week = ["monday",
  "thursday"]` rather than the encoded `18` Automox stores.

  Weeks of the month remain encoded-only, and a controlled experiment now shows
  why: the intuitive reading — bit 2 means "the second Tuesday" — matched only
  eight of twelve live cases. Automox partitions the month into calendar weeks
  rather than counting occurrences of a weekday, so a friendly form built on the
  obvious interpretation would have silently moved when patching ran.

  Policy names must be unique within an organization. Automox returns no
  identifier when a policy is created, so the provider locates the new policy by
  name; where that would be ambiguous it fails and says so rather than adopting
  an arbitrary match.

  Worklet and required-software policies are checked before the request rather
  than after. Automox reports a missing worklet script as "the
  configuration.evaluation code field is required" — a spaced name matching
  nothing in your configuration — and requires `os_family` on both kinds without
  documenting it for either. Those, and the fact that `os_family` is matched
  exactly so `windows` and `macOS` are rejected, are now plan-time errors naming
  the attribute to fix.

- `automox_server_group` resource with full create, read, update, delete, and
  import. Scan interval, parent group, colour, notes, attached policies, and the
  OS auto-update and WSUS settings are all managed.

  `enable_os_auto_update` and `enable_wsus` are three-state: leaving one unset
  means each device keeps its current behaviour, which is not the same as setting
  it to `false` and actively changing every device in the group. WSUS settings are
  written as `enable_wsus`/`wsus_server` and read back from a nested
  `wsus_config` object, which the provider maps so the configuration round-trips
  without a permanent diff.

- Provider configuration: `api_key`, `organization_id`, and `base_url`, each
  settable from the environment so credentials stay out of configuration files
  and state. The API key is marked sensitive and is masked in logs. A value that
  is not known at plan time reports which attribute to make concrete rather than
  failing generically.
- Acceptance-test guards. Tests requiring write access skip with a stated reason
  when the credential is read-only, rather than passing without verifying
  anything, and refuse to run at all until the operator acknowledges that there
  is no Automox sandbox and objects are created in a live organization.
- Schedule bitmask codec translating Automox's encoded `schedule_days` and
  `schedule_months` integers to and from named days and months, so a policy schedule
  can be written as `["monday", "tuesday"]` instead of `6`.
- Automox API error handling covering all five response envelopes the service uses,
  with per-field detail where the API provides it and a bounded body excerpt when it
  returns something unrecognized.
- Automox API client with bearer authentication, automatic organization scoping,
  and retry that honours the service's documented one-minute rate-limit penalty on
  device listing. Query parameters are passed through verbatim, so the API's
  colon- and bracket-suffixed filter names work as written.
- HTTP request logging through Terraform's own log stream, visible under
  `TF_LOG=DEBUG`. Every request records method, URL, status, and duration, plus
  the server's `Retry-After` when rate limited. Credentials are redacted: the
  `Authorization` header is replaced with `REDACTED`, and response bodies are not
  logged at all, since Automox responses can carry device inventories and
  organization access keys.
- Paginated collection reads that return complete result sets. Automox wraps
  collections in three different envelope shapes and pages them three different
  ways, with no relationship between which endpoint uses which; data sources
  declare the shape and get every record without needing to know the convention.
  A failure part-way through a walk aborts rather than returning a partial
  collection, because a truncated fleet listing is indistinguishable from a fleet
  that shrank.
- Organization identifier resolution. Automox scopes some endpoints by integer id
  and others by UUID for the same organization; the provider takes only the
  integer and resolves the UUID once, so configuration cannot carry two
  identifiers that drift apart.
- Deletion detection that accounts for Automox reporting absent resources
  inconsistently: policies return 404, but server groups return 403 for an id that
  was deleted or never existed. A 403 from those endpoints is confirmed against the
  collection listing before the resource is dropped from state, so a credential
  problem is never mistaken for a deletion.
