# Changelog

## 0.1.0 (unreleased)

### Added

- Schedule bitmask codec translating Automox's encoded `schedule_days` and
  `schedule_months` integers to and from named days and months, so a policy schedule
  can be written as `["monday", "tuesday"]` instead of `6`.
- Automox API error handling covering all five response envelopes the service uses,
  with per-field detail where the API provides it and a bounded body excerpt when it
  returns something unrecognized.
- Deletion detection that accounts for Automox reporting absent resources
  inconsistently: policies return 404, but server groups return 403 for an id that
  was deleted or never existed. A 403 from those endpoints is confirmed against the
  collection listing before the resource is dropped from state, so a credential
  problem is never mistaken for a deletion.
