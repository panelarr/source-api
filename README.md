# source-api

The source plugin API of [Panelarr](https://github.com/panelarr): the `panelarr.source.v1` protos, the code generated from them, and a Go SDK for writing and hosting source plugins.

A **source plugin** serves one or more sources: catalogues of series that can be browsed (popular, latest, search with filters), with chapters made of pages. Plugins run out of process. The Panelarr server starts them as child processes (managed) or connects to them by URL (standalone), and talks to them over gRPC.

## What's here

| Path | What |
|---|---|
| [`proto/panelarr/source/v1`](proto/panelarr/source/v1) | The protos: `SourcePluginService` and its messages |
| [`gen/`](gen) | Generated Go (messages, gRPC, Connect) and the OpenAPI document of the HTTP/JSON mapping |
| [`sdk/plugin`](sdk/plugin) | Write a plugin: `plugin.Main` runs it managed (go-plugin) or standalone (`--listen`), errors with reasons, plugin configuration |
| [`sdk/client`](sdk/client) | Host plugins: managed, remote (gRPC), over your own gRPC connection, or in-process |
| [`sdk/httpx`](sdk/httpx) | HTTP clients per source that follow network profiles: proxies, solvers, rate limits, DoH, cookies |
| [`sdk/settings`](sdk/settings) | JSON Schema settings and configuration: validation and defaults |
| [`sdk/conformance`](sdk/conformance), [`cmd/source-conformance`](cmd/source-conformance) | The conformance suite every plugin must pass, as a library and a CLI |
| [`sdk/plugintest`](sdk/plugintest), [`sdk/examples/static-source`](sdk/examples/static-source) | A made-up plugin for tests, and an example plugin built on it |

Any language can implement the API: plain protobuf and gRPC are enough. Go plugins and hosts get the SDK: see [writing a plugin](docs/writing-a-plugin.md) and [hosting plugins](docs/hosting.md).

## Using the protos

- **Go:** `go get github.com/panelarr/source-api` and import `github.com/panelarr/source-api/gen/panelarr/source/v1`.
- **Other languages:** copy `proto/` at a tagged version (or use it as a buf input) and generate code with your usual tools.

## Development

```sh
make setup   # installs pinned tools (buf, protoc plugins, golangci-lint, govulncheck) into ./bin
make gen     # regenerates gen/ from the protos
make check   # format check, lint, breaking-change check, vulnerabilities and tests
make help    # lists every target
```

See [CONTRIBUTING.md](CONTRIBUTING.md) and [docs/](docs/README.md).

## Licence

Apache License 2.0 ([LICENSE](LICENSE)): the protos, the generated code and the SDK, so that plugins and hosts under any licence can use them.
