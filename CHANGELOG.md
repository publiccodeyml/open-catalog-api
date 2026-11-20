# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- Bundle endpoints at `/v1/bundles` to create, list, read, update and
  delete bundles. A bundle holds up to 255 software.
- An audit stream recording the entity, change, actor and timestamp,
  readable at `GET /v1/events` and `GET /v1/events/:id`.
- `EVENT_RETENTION_DAYS` to purge older audit events at startup and
  daily. It defaults to `365`. Set it to `0` to keep events forever.
- A webhook `format` field accepting `default`, `standard-webhooks`
  and `github`, for the existing payload, Standard Webhooks deliveries
  and GitHub repository dispatches. Existing webhooks keep `default`.
- `GET /v1/publishers/:id/logs` and `POST /v1/publishers/:id/logs`.
- RFC 6902 JSON Patch support on `PATCH /v1/logs/:id`, alongside JSON
  merge patches.
- A security policy, `SECURITY.md`, saying how to report a vulnerability
  privately and which versions get fixes.
- CI checks for the Helm chart with `helm lint` and `kubeconform`, and for
  GitHub Actions workflows with `actionlint`.
- CI checks for the Dockerfiles with `hadolint`, and a weekly `trivy`
  scan of the published container image for Alpine package
  vulnerabilities.
- `entityType`, `entityId`, `type` and `actor` filters on `GET /v1/events`.
- An `extraEnv` value on the Helm chart, for environment variables
  without a dedicated chart value.
- `webhook_delivery_failures_total` on `/metrics`, counting failed
  webhook deliveries by `reason`: `transport` when no response arrived,
  `status` for a non-2xx response. It carries no webhook ID or URL.

### Changed

- The rate limit counts IPv6 clients per /64, so a client can no longer
  avoid it by sending each request from a different address of its
  network. IPv4-mapped IPv6 addresses count as their IPv4 address.

- Event creation times are indexed to speed up retention purges and date filters.

- The Helm chart runs the API as UID/GID 10001 with the security defaults
  required by the restricted Pod Security Standard. SQLite storage must
  be writable by that user.
- Requests must finish sending within 30 seconds. Idle keep-alive
  connections close after 120 seconds.
- Responses must be read within 5 minutes, so a client that stops
  reading no longer keeps its response in server memory.
- PostgreSQL connections are limited to 10 per replica and kept open for
  reuse. Set `DATABASE_MAX_CONNECTIONS` to change the limit, and size it
  across all replicas within PostgreSQL's `max_connections`.
- Container builds now use GoReleaser's `dockers_v2` configuration.
- Webhooks targeting RFC 6598 shared address space are now refused.
- Webhooks targeting a NAT64 (`64:ff9b::/96`) or 6to4 (`2002::/16`)
  address are now refused when the embedded IPv4 address is refused.
- Negative `MAX_REQUESTS`, `WEBHOOK_DEBOUNCE_MS`, `WEBHOOK_DEBOUNCE_MAX_MS`
  and `EVENT_RETENTION_DAYS` settings now prevent startup. Use zero to disable
  the corresponding behavior.
- JSON Patch operations on `id`, `createdAt`, `updatedAt` and
  `catalogId` are refused with `422`, as they already were in a JSON
  merge patch. `test` operations on those fields are still allowed.
  An operation on the document root or on a code hosting entry is
  refused too when its value changes one of those fields.
- Every `500` carries the same generic detail. A few answered `db error`.
- The events model no longer has a `deleted_at` column. An existing
  database keeps the column, unused.
- Software URLs and aliases are unique within each catalog instead of
  globally, so several catalogs can list the same repository.
  `GET /v1/software?url=` returns every match across catalogs.
- Prometheus metrics carry `service="open-catalog-api"` instead of the
  path of the running executable, which changed between builds and
  restarts. Update queries and alerts that select the old value.
- `POST` and `PATCH` requests with a Content-Type that is not JSON, such
  as XML or form bodies, are refused with `415`. They used to be decoded
  without the unknown field check.

### Removed

- `PATCH /v1/webhooks/:id`. Delete the webhook and create a new one
  instead of updating it. Changing its URL used to keep the secret
  and send it to the new host.

### Fixed

- The OpenAPI JSON Patch schema now lists the `move` and `copy`
  operations, which the API already accepted.
- The OpenAPI spec now documents the `url` filter on
  `GET /v1/catalogs/{catalogId}/software`, which the API already applied.
- The OpenAPI spec now accepts a publisher `alternativeId` in the path of
  `/v1/publishers/{publisherId}`, which the API already resolved.
- The OpenAPI spec now documents the `Retry-After` header on `429`
  responses, which the API already sent.
- Webhook events from requests still running during shutdown are drained before exit.
- List requests with both `page[after]` and `page[before]` return `422`
  instead of ignoring `page[before]`.
- An analysis namespace nested more than 1000 levels deep made
  `PATCH /v1/software/{id}/analysis` and `PATCH /v1/catalogs/{id}/analysis`
  answer `500` on SQLite. Namespaces may now nest at most 64 objects and
  arrays, and deeper ones get `422` on both databases.
- The analysis of a software or catalog grew without limit when each
  `PATCH` added a new namespace. A merged document larger than 1 MiB is
  now rejected with `422` and nothing is written. Patches that only remove
  namespaces are still accepted.
- Publisher routes now decode a percent encoded `alternativeId` in the
  path, so a publisher whose `alternativeId` has a space or a non ASCII
  letter can be read, updated and deleted by it.
- URL length limits apply to the URL as stored, after normalization
  percent-encodes non ASCII letters and spaces. A software or publisher
  created with such a URL could grow past the limit and then fail every
  PATCH with `422`.
- A NUL character (U+0000) in a request body string now gets `422`, and
  one in the path or a query parameter gets `400`. On PostgreSQL these
  requests answered `500`, including reads that need no token.
- `POST /v1/catalogs` and `PATCH /v1/catalogs/{id}` return `409` when
  `alternativeId` is the `id` of another catalog, as publishers already
  did. Requests for that `id` reached the catalog holding it as
  `alternativeId` instead.
- `PATCH /v1/catalogs/{id}` returns `422` when it sets `alternativeId`
  to `∅` on a catalog that is not the root. The catalog took the place
  of the root, its writes went to the root, and it could not go back.
- `DELETE /v1/catalogs/{id}` no longer succeeds alongside a concurrent
  create of software, a publisher or a log in that catalog. The new
  resource kept the id of the deleted catalog. Now the delete answers
  `409` or the create answers `404`.
- `DELETE /v1/software/{id}` and `DELETE /v1/publishers/{id}` no longer
  leave behind a log or webhook created on the same resource at the same
  time. It kept the id of the deleted resource and stayed listed. Now the
  create answers `404` or its log or webhook goes with the resource.
- `TRUSTED_PROXIES` ignores spaces around its entries, and the API
  refuses to start on an entry that is not an IP address or a CIDR. A
  padded entry, a hostname or a bad CIDR never matched the proxy, so
  every client behind it shared one rate limit.

- Publisher PATCH responses include stored timestamps and the default group flag
  for newly added code hosting entries.

- JSON Patch documents that add unknown members, or members with a
  different casing, now return `422` instead of being silently dropped or
  overwriting server owned fields.

- Unknown `entityType` filters on `GET /v1/events` now return `422`.

- Event and webhook reads require a token with any path casing.

- `token create` uses `PASETO_KEY` when `--key` is empty instead of
  generating a random key that the running API cannot use. Invalid
  environment keys are rejected with an error naming `PASETO_KEY`.

- Pagination links preserve encoded query names instead of turning them
  into extra filters.

- Analysis PATCH requests can remove a namespace by setting it to null.

- Bundle requests with duplicate `softwareIds` now return 422 with an
  error saying the list contains duplicates.
- Rejected webhook URLs now explain that HTTPS, a public address and no
  credentials are required. The OpenAPI spec documents these restrictions.
- A request body with a field of the wrong type now gets a `400` that
  names the field, like `wrong type for /active`, instead of `invalid or
  malformed JSON`.
- Request validation now follows the OpenAPI length and list size limits.
  Log messages can be a single character.
- The OpenAPI `Software` schema now allows a null `vitality` and an empty
  `aliases` list, matching the values returned by the API.
- `helm upgrade` now restarts the API pods when it changes `databaseDSN`
  or `pasetoKey` in the Secret generated by the chart.
- Catalog reads, updates, deletions and analysis requests now resolve
  alternative IDs containing percent escapes correctly.
- Software URLs containing quotes or backslashes now round trip correctly
  in JSON responses.
- JSON Patch writes to software and catalog `analysis` now return `422`
  instead of silently discarding the value. Use the dedicated analysis
  endpoints.
- Software aliases now keep the same URL order in responses and JSON Patch
  documents, so operations by index affect the expected alias.
- Deleting a publisher or software now removes its resource webhooks while
  preserving delivery of the final delete event.
- Startup now removes the webhooks, and their secrets, that an earlier
  version kept for deleted publishers and software.
- Startup now removes the logs that an earlier version kept for deleted
  publishers, software and catalogs, so `GET /logs` stops listing them.
- `token create` now rejects negative `--expiry` durations instead of
  creating a token without expiration. Zero still means never expires.
- Rate limited requests got a plain text `429` body. They now get the
  `application/problem+json` document the OpenAPI spec declares, and the
  `401` problem document carries the required `detail`.
- Software creation rejects aliases that normalize to the canonical URL or
  to another alias with `422` naming the alias, instead of `409`.
- Publisher creation rejects `codeHosting` URLs that normalize to another
  entry with `422` naming the URL, instead of `409`.
- Catalog POST and PATCH requests reject duplicate source URLs after
  normalization with `422`, preventing sources that cannot be fully removed
  by a later patch.
- Deleting a catalog did not delete its logs.
- Requests whose path and query joined to the same text, like
  `/v1/publishers/a?b` and `/v1/publishers/ab`, could get each other's
  cached response.
- `PATCH /v1/publishers/{publisherId}` ignored the `group` flag of
  `codeHosting` entries, and new entries always became groups.
- Token creation output now states that a token authorizes every authenticated
  API operation instead of listing an incomplete set of permissions.
- The weekly fuzz job now reports errors when listing Go packages or their
  fuzz test functions (`Fuzz...`).
- `?search` on a list other than the logs answered `500`. The parameter
  is now ignored there.
- `%`, `_` and `\` in `?search` on the logs now match literally. `%` and
  `_` matched any characters, so `search=%` returned every log.
- The Helm chart now renders a valid `autoscaling/v2` HPA and quotes the
  numeric `MAX_REQUESTS` environment value.
- Concurrent `PATCH` requests to the `analysis` endpoint of the same software
  or catalog no longer discard namespaces written by other scanners.
- A JSON Patch replacing the `id` of a software answered `200` but did
  not save the software.
- A JSON Patch could store a malformed URL or email. The patched entity
  now goes through the same validation as a `POST`.
- A `DELETE` of a missing software answered `404` but still recorded a
  delete event and notified the webhooks.
- Two concurrent `DELETE` of the same software or publisher both
  answered `204` and recorded two delete events.
- The event recorded by an analysis `PATCH` had no actor.
- `?from` and `?to` refused RFC 3339 values with a zone offset.
- `?from` and `?to` were ignored on the per entity log lists and on the
  webhook lists, although the OpenAPI spec declares them there.
- Catalogs, bundles and webhooks wrote no event to the audit trail.
- A webhook `POST` with the url of an existing one answered `500` instead
  of `409`.
- The `409` for a duplicate catalog `alternativeId` now names the field.
- An error the API did not classify answered `404` with the raw error
  text. It is now a `500` with a generic detail.
- Deleting a publisher or software did not delete its logs.
- The `filter` parameter of `GET /v1/logs` was missing from the OpenAPI
  spec, and the README named a Go version older than the one required.
- The sources of a patched catalog were returned in a random order.
- A `PATCH` on the analysis of a software or catalog that rolled back
  still notified the webhooks of an update that never happened.
- On SQLite, a stored analysis namespace that was not a JSON object
  made every later `PATCH` on that row answer `500`.
- A `PATCH` on the analysis dropped a namespace that another writer had
  changed since the client read it.
- An empty `PATCH` on the analysis recorded an update event and fired
  the webhooks.
- On shutdown, the process could exit before delivering the webhooks of
  events still in the debounce queue.
- Two concurrent `PATCH` requests on the same software, publisher,
  catalog, bundle or log both answered `200`, but the later one put back
  the fields the earlier one had changed. A JSON Patch `test` operation
  now also checks the stored row.

### Security

- The Helm chart disables Kubernetes API token mounting in the API pod
  by default. Set `serviceAccount.automount` to `true` to enable it.
- PASETO tokens now enforce expiration, not before and issued at claims
  when present. Expired tokens and tokens dated in the future are refused.
- Webhook read endpoints now require authentication, including lists
  for all publishers or software and for an individual entity.
- The released container image runs as an unprivileged user instead of
  root.
- A JSON Patch could set `createdAt` on a software or a catalog, moving
  it in the listings and breaking the pagination cursors near it.
- The database image used for local development is pinned by digest,
  and is the same one the tests run against.
- A JSON Patch of `copy` operations that each doubled the document
  could make the API allocate gigabytes. Copies may now add at most
  4 MiB in total, and a patch going over gets `422`.
- Public `GET` requests that differed only in their query string were
  each kept in the response cache, so one client could grow memory by
  hundreds of megabytes in seconds. The cache now keeps at most 10000
  responses and 32 MiB of bodies, and serves later responses uncached
  until older ones expire.

## [1.4.0] - 2026-08-19

### Added

- An `analysis` field on catalogs, so a catalog carries the same
  analysis data software already did.

### Fixed

- The migration of the `logs` table failed on a column type mismatch,
  which left an upgraded database unusable.

### Security

- The release pipeline now pins every GitHub action by digest, denies
  workflow permissions by default, and verifies in CI that the released
  binary can be rebuilt byte for byte from source. Each release ships an
  SBOM and a build provenance attestation.
- The development image is pinned by digest.

## [1.3.0] - 2026-05-08

### Added

- Scopes on catalogs, so a token can be limited to one catalog.
- A root catalog, reachable through the `∅` alternativeId, which lets
  the catalog routes address the whole collection.
- Debouncing of webhook notifications, so a burst of writes produces one
  delivery instead of one per write. Tunable with `WEBHOOK_DEBOUNCE_MS`
  and `WEBHOOK_DEBOUNCE_MAX_MS`.
- Size and range limits on catalog sources, analysis values and JSON
  Patch bodies, rejected at validation instead of reaching the database.
- Stricter validation of publisher CodeHosting URLs.

### Changed

- Indexes added on the foreign key columns, which speeds up the list
  endpoints on a large catalog.

### Fixed

- Webhook dispatch had no timeout and could hold a request open
  indefinitely. It is now capped at 10 seconds.
- Every webhook was signed with the same secret rather than its own, so
  a receiver could verify a payload meant for a different endpoint.
- Pagination links dropped the query parameters of the original request,
  so following `next` silently widened the result set.
- A `fiber.Error` was classified as an internal error, turning a 4xx
  into a 500.
- Several OpenAPI defects: invalid examples, duplicate description keys
  and the wrong media type on the analysis PATCH endpoint.

## [1.2.0] - 2026-04-22

### Added

- A catalogs resource, with a `publishersNamespace` that scopes
  publisher ids to one catalog.
- `POST /catalogs/:id/logs`.
- A `token` subcommand that creates API tokens, so a deployment no
  longer needs a separate tool to issue one.

### Changed

- The project is now called software-catalog-api. The binary, the
  OpenAPI file and the documentation use the new name. The old binary
  name stays as a symlink, so existing deployments keep working.

### Removed

- The `analysis` payload no longer ships inside the software object. It
  moved to `GET /software/:id/analysis`, which keeps the software
  response small. Clients reading `software.analysis` must follow the
  new endpoint.

### Fixed

- The `url` query filter was ignored when listing the software of a
  catalog.
- Filtering by url returned inactive software.

## [1.1.0] - 2026-03-22

### Added

- A healthcheck endpoint, `/livez`, for readiness probes.
- JSON Patch support on the Publishers resource.
- Normalization of the URLs stored on publishers and software, so the
  same repository submitted in two spellings resolves to one record.

### Fixed

- A duplicate key when patching a publisher returned 500 instead of 409,
  and the 409 response now names the field that collided.
- The check for a conflicting `alternativeId` was not atomic, so two
  concurrent writes could both pass it.
- The paginator configuration was mutated globally, so one request could
  change the page size seen by another.

## [1.0.1] - 2026-02-25

### Changed

- Dependency and CI updates only, no behaviour change. Includes
  `golang.org/x/crypto` 0.36.0 to 0.45.0 and Fiber 2.52.9 to 2.52.12.

## [1.0.0] - 2025-09-09

First stable release. The API had already been running in production for
some time, and this tag declares the interface stable.

### Changed

- Go 1.24.

[Unreleased]: https://github.com/publiccodeyml/open-catalog-api/compare/v1.4.0...HEAD
[1.4.0]: https://github.com/publiccodeyml/open-catalog-api/compare/v1.3.0...v1.4.0
[1.3.0]: https://github.com/publiccodeyml/open-catalog-api/compare/v1.2.0...v1.3.0
[1.2.0]: https://github.com/publiccodeyml/open-catalog-api/compare/v1.1.0...v1.2.0
[1.1.0]: https://github.com/publiccodeyml/open-catalog-api/compare/v1.0.1...v1.1.0
[1.0.1]: https://github.com/publiccodeyml/open-catalog-api/compare/v1.0.0...v1.0.1
[1.0.0]: https://github.com/publiccodeyml/open-catalog-api/compare/v0.12.1...v1.0.0
