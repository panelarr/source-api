# The source API

`panelarr.source.v1` ([proto](../proto/panelarr/source/v1/source_plugin.proto)) is what every source plugin implements. The proto comments are the reference; this page gives the overview.

## Concepts

- **Plugin:** one process (or remote endpoint). `GetInfo` describes it: identity, version, capabilities, limits, the configuration it expects (a JSON Schema) and the common services it can use (network profiles, solvers).
- **Source:** a catalogue inside a plugin, e.g. one site in one language. `ListSources` and `GetSource` list them with their capabilities and health.
- **Series, chapters, pages:** browse with `Popular`, `Latest` and `Search` (with typed filters from `GetFilters`); then `GetSeries`, `ListChapters`, `ListPages`, and `FetchImage` streams a page's bytes.
- **Keys are opaque:** series keys, chapter keys and page refs are chosen by the plugin. Callers store them and pass them back unchanged, and never parse them.
- **Settings:** per source, as JSON Schema plus values (`GetSettingsSchema`, `GetSettings`, `SetSettings`).
- **Network profiles:** the host owns proxies, solvers and rate limits, and pushes the profile for each source with `SetNetworkProfile`.

## Errors

Errors are gRPC status codes. When the upstream source is the problem, the status carries a `google.rpc.ErrorInfo` with domain `source.panelarr` and a reason from `ErrorReason` without its prefix: `CLOUDFLARE_CHALLENGE`, `RATE_LIMITED` (with a `google.rpc.RetryInfo`), `SOURCE_DOWN`, `NOT_FOUND`, `LOGIN_REQUIRED`, `UNSUPPORTED`.

## Wire

gRPC is the canonical wire: every plugin must speak it, and hosts call plugins over it. The `google.api.http` annotations define an HTTP/JSON mapping ([OpenAPI](../gen/openapi/source.openapi.yaml)) that plugins may also serve.

## Compatibility

The package is versioned (`v1`). Changes within it are backwards compatible only: fields and RPCs are added, never removed, renumbered or retyped. `make breaking` enforces this against `main`.
