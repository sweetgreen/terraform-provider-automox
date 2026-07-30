# Changelog

## 0.1.0 (unreleased)

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

### Added

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
