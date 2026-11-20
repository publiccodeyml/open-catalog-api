<!-- markdownlint-disable no-inline-html -->

<h1 align="center">Open Catalog API</h1>

<p align="center">
  <img width="200" src=".github/logo.svg" alt="open-catalog-api logo">
</p>

<p align="center">
  <a href="https://www.bestpractices.dev/projects/14158">
    <img src="https://www.bestpractices.dev/projects/14158/badge">
  </a>

  <img alt="License" src="https://img.shields.io/github/license/publiccodeyml/open-catalog-api?color=brightgreen">
</p>

<div align="center">
  <h3>
    <a href="https://publiccodeyml.github.io/open-catalog-api/">
      API documentation
    </a>
  </h3>
</div>

<p align="center">
  <strong>Open Catalog API</strong> is a RESTful API for public software
  catalogs.
  It powers national open source catalogs for public administrations:
</p>

- 🇮🇹 [developers.italia.it](https://developers.italia.it)
- 🇨🇭 [swiss/oss-catalog](https://github.com/swiss/oss-catalog)

## Requirements

* Golang 1.27+
* [PostgreSQL](https://www.postgresql.org/)

## Development

To start developing:

1. Clone the repo
2. Build and start the containers

   ```shell
   docker compose up
   ```

Docker Compose will bring up the app and PostgreSQL containers.

Wait until the Docker logs explicitly say the API is up and you can use
its endpoints at `http://localhost:3000/v1/`.

## Configuration

You can configure the API with environment variables:

* `DATABASE_DSN`: the URI used to connect to the database,
  fe `postgres://user:password@host:5432/dbname`.
  Release binaries and container images support PostgreSQL only.
  At startup the API creates the `pg_trgm` extension to index log
  searches, which needs the `CREATE` privilege on the database. Without
  it the API starts and log searches run unindexed.
  SQLite requires a build with cgo enabled (`CGO_ENABLED=1`), for tests
  and local development.

* `DATABASE_MAX_CONNECTIONS` (optional): maximum PostgreSQL connections per
  API replica. Must be positive. Default: `10`.
  Leave room for all replicas and other database clients.

* `PASETO_KEY` (optional): Base64 encoded 32 bytes key used to check the
  [PASETO](https://paseto.io/) authentication tokens. You can generate a key
  and a token with the built-in subcommand:

  ```console
  open-catalog-api token create
  ```

  The command uses `PASETO_KEY` when set. Pass `--key` to override it.
  If both are empty, it generates a random key and prints it to stderr.
  Use `--sub` to identify the caller and `--expiry` to set a custom
  duration (default: 1 year, `0` = never expires).

  If not set, the API will run in read only mode.

* `ENVIRONMENT` (optional): `test` turns the rate limiter and the event
  purge off, for the test suite. Any other value is a normal run.
  Default `production`.

* `MAX_REQUESTS` (optional): number of requests per minute after which
  responses will be ratelimited. Set to 0 to disable rate limiting.
  Each IPv4 address counts on its own, IPv6 addresses count per /64.
  Default: `0`.

* `TRUSTED_PROXIES` (optional): comma separated list of addresses or
  CIDRs of the reverse proxies in front of the API,
  fe `10.0.0.0/8,192.168.1.7`. Spaces around the entries are ignored,
  and the API refuses to start on an entry that is not an IP address or
  a CIDR, such as a hostname.
  The `X-Forwarded-For` header is trusted only on requests coming from
  these addresses, and its first entry is the client the rate limit
  counts. Leave it empty when the API is exposed directly: the header
  is then ignored and the client is the peer of the connection.
  Default: empty.

* `WEBHOOK_DEBOUNCE_MS` (optional): delay in milliseconds before a
  webhook is dispatched after the last write on the same resource and
  event. Set to 0 to disable debouncing entirely. Debouncing is per
  replica.
  Default: `1000`.

* `WEBHOOK_DEBOUNCE_MAX_MS` (optional): hard cap in milliseconds on how
  long a delivery can be deferred by repeated writes on the same
  resource and event. Set to 0 to disable the cap. Ignored when
  `WEBHOOK_DEBOUNCE_MS` is 0.
  Default: `10000`.

* `EVENT_RETENTION_DAYS` (optional): how many days the audit trail is
  kept. Every create, update and delete of a catalog entity is an
  event recording the entity, the kind of change, who made it and
  when, readable at `GET /v1/events`. Older events are deleted at
  startup and once a day. Set to `0` to keep them forever.
  Values must be between `0` and `106751` days.
  Default: `365`.

## Production with Docker Compose

`compose.prod.yml` runs the `stable` image, the latest release that is
not a prerelease, with a PostgreSQL container. Copy `.env.example` to
`.env`, set at least `POSTGRES_PASSWORD`, then start it:

```shell
docker compose -f compose.prod.yml up -d
```

The API listens on `127.0.0.1:3000`. Put a reverse proxy in front of it
and set `TRUSTED_PROXIES` as described above.

## Contributing

This project exists also thanks to your contributions! Here is a list of
people who already contributed to this repository:

<a href="https://github.com/publiccodeyml/open-catalog-api/graphs/contributors">
  <img
  src="https://contributors-img.web.app/image?repo=publiccodeyml/open-catalog-api"
  />
</a>

## License

The source code is released under the AGPL version 3.

The version control system provides attribution for specific lines of code.
