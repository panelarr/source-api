# Progress

The v0 plan for source-api and source-mihon, in slices. Each slice works end to end, has tests and ends with `make check` green.

- [x] **1. Scaffold:** Go module, `make` targets, lint, licence, repo docs, CI.
- [x] **2. Protos and codegen:** `panelarr.source.v1` (moved unchanged from mihon-source), buf lint and breaking checks, Go (messages, gRPC, Connect) and OpenAPI generation.
- [x] **3. SDK:** `plugin.Main` (managed and standalone), a client (managed, remote, in-process), `httpx`, settings validation and a conformance suite and runner. Plus plugin-level `GetConfig`/`SetConfig` in the protos.
- [x] **4. mihon-source consumes these protos** (pinned in `third_party/source-api` with a checksum guard), with no API change.
- [x] **5. source-mihon:** a source plugin that serves mihon-source's sources, keeps its stores and extensions and forwards network profiles; conformance end to end (`make e2e` in source-mihon).
- [x] **6. Image and docs** for source-mihon: a distroless image (`make docker`), a compose example next to mihon-source, and GoReleaser releases.

All v0 slices are done.

## GOAL-0012: extension packages and languages

- [x] **1. source.v1 package capability** (source-api): `PACKAGES`, the package calls, `Source.package_id`, store error reasons; SDK, fake, conformance, docs.
- [x] **2. mihon-source:** per-extension enabled languages; installs survive a cancelled caller; fixture store export for end-to-end tests.
- [x] **3. source-mihon:** the package capability over mihon-source, plugin-level languages, filtered sources; end to end.
- [x] **4. Docs and done.**

## GOAL-0018: network profiles and per-source settings

- [x] **1. source.v1:** `HealthResponse.instance_id` (filled by the SDK), `GetNetworkProfile` with its origin; SDK, fake, conformance, docs.
- [x] **2. mihon-source:** instance ID, GetNetworkProfile, writeOnly secrets in source settings.
- [x] **3. source-mihon:** instance ID covering mihon-source's, GetNetworkProfile; end to end: proxy, solver, fallback, restarts, settings.
- [x] **4. Docs and done.**

## GOAL-0020: jobs, package origins and details

- [x] **1. source.v1 jobs:** `panelarr.jobs.v1` (the shared job and run messages), the `JOBS` capability and its six calls, `ALREADY_RUNNING`; `sdk/jobs`, fake, conformance, docs.
- [x] **2. source.v1 packages and health checks:** origins, package details, reload, per-source health checks.
- [x] **3. mihon-source:** health-check schedule over the admin API, step notes for targeted checks.
- [x] **4. source-mihon:** jobs, origins, details, reload, health checks; end to end.
