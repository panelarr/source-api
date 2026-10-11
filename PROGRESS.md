# Progress

The v0 plan for source-api and source-mihon, in slices. Each slice works end to end, has tests and ends with `make check` green.

- [x] **1. Scaffold:** Go module, `make` targets, lint, licence, repo docs, CI.
- [x] **2. Protos and codegen:** `panelarr.source.v1` (moved unchanged from mihon-source), buf lint and breaking checks, Go (messages, gRPC, Connect) and OpenAPI generation.
- [x] **3. SDK:** `plugin.Main` (managed and standalone), a client (managed, remote, in-process), `httpx`, settings validation and a conformance suite and runner. Plus plugin-level `GetConfig`/`SetConfig` in the protos.
- [x] **4. mihon-source consumes these protos** (pinned in `third_party/source-api` with a checksum guard), with no API change.
- [x] **5. source-mihon:** a source plugin that serves mihon-source's sources, keeps its stores and extensions and forwards network profiles; conformance end to end (`make e2e` in source-mihon).
- [x] **6. Image and docs** for source-mihon: a distroless image (`make docker`), a compose example next to mihon-source, and GoReleaser releases.

All v0 slices are done.
