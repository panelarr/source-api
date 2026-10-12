# Conformance

The conformance suite ([`sdk/conformance`](../sdk/conformance/conformance.go)) checks that a plugin follows `panelarr.source.v1`.

## What it checks

- **Plugin:** `Health` (status, and an `instance_id` that doesn't change while it runs), `GetInfo` (id, name, version, contracts, capabilities, limits, a valid config schema), `GetConfig`/`SetConfig` (secrets never returned; `UNIMPLEMENTED` without a schema), `Health`.
- **Sources:** unique IDs, names, languages and capabilities; the language filter of `ListSources`; `NOT_FOUND` for an unknown source; removing a pushed network profile, after which `GetNetworkProfile` (when implemented) must not report it as pushed or return a password.
- **The walk, per source:** `GetSource` → `GetFilters` → `Popular` → `Latest` → `Search` → `GetSeries` → `ResolveUrl` → `ListChapters` → `ListPages` → `FetchImage` → settings. Each step feeds the next (the first popular series, its newest chapter, its first page). Search looks for the first popular series by its title and expects to find it: hosts match series across sources that way.
- **Packages** (plugins with `PACKAGES`): valid package IDs and fields, the list filters, `NOT_FOUND` for an unknown package, and that `ListSources` lists exactly the available sources of installed packages. With `--package ID` (`Options.PackageID`) it also installs the package, installs it again (must answer OK), checks its sources appear, narrows it to one language (the others must go and answer `NOT_FOUND`), restores the languages and, if it wasn't installed before, removes it again. That changes the plugin, so it's opt-in. Reloading the package is part of that walk. Without `--package`, it checks that `ListOrigins` lists every origin the offers name, that only installed packages carry `details`, and `NOT_FOUND` from `RefreshOrigin` and `ReloadPackage` for unknown IDs.
- **Health checks** (plugins with `HEALTH_CHECKS`): `SetSourceHealthCheck` answers `NOT_FOUND` for an unknown source. It never turns a real source's checks off.
- **Jobs** (plugins with `JOBS`): valid job IDs, names and param types, `running` matching `current_run_id`, `GetJob`, `ListJobRuns` and `GetJobRun` for what's listed, and `NOT_FOUND` for an unknown job or run. It never runs a job: runs do real work.
- **Errors:** every failure must carry a known error reason, and failures of the upstream (`UNAVAILABLE`, `RESOURCE_EXHAUSTED`) should say why. Calls a source doesn't support should answer `UNIMPLEMENTED`.

**Problems** break the protocol. **Warnings** are likely mistakes. A **failed step** usually means the source's site is failing, which is reported per source but isn't a protocol problem.

## From Go tests

```go
rep := conformance.Run(t, client.InProcess(p), conformance.Options{})
```

`Run` fails the test for every problem and logs warnings and failed steps.

## From the command line

```sh
make build
bin/source-conformance --target localhost:8904 --api-key KEY
bin/source-conformance --managed ./my-plugin --config config.json
bin/source-conformance --target localhost:8904 --max-sources 5 --skip-images --json
```

Flags: `--package ID`, `--sources a,b`, `--max-sources N`, `--query Q`, `--skip-images`, `--config FILE`, `--connect`, `--timeout`, `--json`, `--strict` (also fail on failed steps). Exit codes: 0 conformant, 1 protocol problems, 2 bad usage or unreachable, 3 failed steps with `--strict`.
