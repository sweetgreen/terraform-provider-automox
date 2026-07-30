# Changelog

## 0.1.0 (unreleased)

### Added

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
