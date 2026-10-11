# source-api

The source plugin API of [Panelarr](https://github.com/panelarr): the `panelarr.source.v1` protos, the code generated from them, and a Go SDK for writing and hosting source plugins.

A **source plugin** serves one or more sources: catalogues of series that can be browsed (popular, latest, search with filters), with chapters made of pages. Plugins run out of process. The Panelarr server starts them as child processes (managed) or connects to them by URL (standalone), and talks to them over gRPC.

## What's here

| Path | What |
|---|---|
| [`proto/panelarr/source/v1`](proto/panelarr/source/v1) | The protos: `SourcePluginService` and its messages |
| [`gen/`](gen) | Generated Go (messages, gRPC, Connect) and the OpenAPI document of the HTTP/JSON mapping |

Any language can implement the API: plain protobuf and gRPC are enough.

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
