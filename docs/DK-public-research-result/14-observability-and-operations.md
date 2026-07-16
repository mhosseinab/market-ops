# Observability and operations

Track extraction success per page type, missing critical fields, HTTP status by
endpoint, selector failures, response key-set drift, queue depth, and batch
upload latency/failure. Keep structured logs local-first and include
`crawlRunId`, `connectorVersion`, and `schemaVersion`.

Alert after three consecutive canary non-200 responses, on a missing top-level
response key, and when queue backpressure engages. Roll metrics up by connector
version so a release regression is visible.

Use semver for connector versions. Keep canonical schema changes additive within
a major version; require a major schema bump and backend migration for breaking
changes. The evidence mentions `file 14` only; any referenced `file 17` drift
rules were not supplied.
