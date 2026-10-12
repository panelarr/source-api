# The source API

`panelarr.source.v1` ([proto](../proto/panelarr/source/v1/source_plugin.proto)) is what every source plugin implements. The proto comments are the reference; this page gives the overview.

## Concepts

- **Plugin:** one process (or remote endpoint). `GetInfo` describes it: identity, version, capabilities, limits, the configuration it expects (a JSON Schema) and the common services it can use (network profiles, solvers).
- **Source:** a catalogue inside a plugin, e.g. one site in one language. `ListSources` and `GetSource` list them with their capabilities and health.
- **Series, chapters, pages:** browse with `Popular`, `Latest` and `Search` (with typed filters from `GetFilters`); then `GetSeries`, `ListChapters`, `ListPages`, and `FetchImage` streams a page's bytes.
- **Keys are opaque:** series keys, chapter keys and page refs are chosen by the plugin. Callers store them and pass them back unchanged, and never parse them.
- **Settings:** per source, as JSON Schema plus values (`GetSettingsSchema`, `GetSettings`, `SetSettings`).
- **Network profiles:** the host owns proxies, solvers and rate limits, and pushes the profile for each source with `SetNetworkProfile`.

## Packages and languages

Plugins that host many sites (Mihon extensions, say) declare the `PACKAGES` capability. Hosts then show an Extensions list for them: `ListPackages` (with search and language, installed, updates and origin filters), `GetPackage`, `InstallPackage`, `UpdatePackage`, `UninstallPackage` and `SetPackageSettings`. Plugins without the capability answer `UNIMPLEMENTED`.

- **Package IDs** match `^[A-Za-z0-9][A-Za-z0-9._-]*$`: never a `:`, which HTTP paths read as a verb (`:install`).
- **Install and update are synchronous** and can take minutes, so give them a long deadline. They're idempotent: the installed version again answers OK. A cancelled call either finishes or leaves the previous state; never half a package. Uninstalling a package that isn't installed answers OK.
- **Languages** decide which sources exist, at two levels: the plugin's own setting (part of its configuration) and each package's `enabled_langs`. Empty means all at that level. A source is listed only when its language passes both, and calls to any other source answer `NOT_FOUND`. `"all"` and `"multi"` are languages like any other: name them to keep multi-language sources.
- `Source.package_id` names the package a listed source comes from; `PackageSource.available` says whether a package's source passes the languages.
- Failures of the package's origin carry `STORE_FETCH_FAILED` (UNAVAILABLE) or `STORE_INVALID` (FAILED_PRECONDITION).
- **Origins** are where packages come from (extension stores, say): `ListOrigins` shows each with its URL, last refresh, error, package count and pinned signing key, and `RefreshOrigin` fetches one again. `PackageOffer.origin_id` names them, and their IDs follow the package ID rule.
- **Package details:** an installed package can carry `details` for an admin to check: signing fingerprint, the plugin API version it was built against, when and from which origin it was installed, and its file's SHA-256. Why it doesn't work is in `error`.
- **`ReloadPackage`** loads an installed package again without downloading anything: it retries a package that failed to load and applies settings the package reads only at start. `UpdatePackage` doesn't, when the package is up to date.

## Health checks

Plugins that check their sources on a schedule declare `HEALTH_CHECKS`. They report each source's last check in `Source.health`, and `SetSourceHealthCheck(source_id, enabled)` turns a source's scheduled checks off or on again (`SourceHealth.checks_disabled` shows it). Checking now, and the schedule, are the plugin's jobs, when it has the `JOBS` capability.

## Jobs

Plugins with background work (refreshing what they can install, checking their sources) declare the `JOBS` capability. Hosts then show it in their Jobs view: `ListJobs`, `GetJob`, `ListJobRuns`, `GetJobRun`, `RunJob` and `CancelJobRun`. Plugins without the capability answer `UNIMPLEMENTED`.

The jobs and runs are messages of `panelarr.jobs.v1` ([proto](../proto/panelarr/jobs/v1/jobs.proto)), the model Panelarr's services share, so one view can show them all:

- **Job IDs** match `^[a-z0-9][a-z0-9._-]*$`: never a `:`, for the same reason as package IDs (`:run`).
- **One run at a time.** `RunJob` returns once the run has started; follow it with `GetJobRun`. Starting a running job fails with `ALREADY_EXISTS`, reason `ALREADY_RUNNING` (metadata `current_run_id`) and a `JobAlreadyRunning` detail. A scheduled run that finds its job busy is recorded as `SKIPPED`.
- **Params** are declared on the job (`bool`, `int`, `string`, `string_list`, with defaults and choices). Scheduled runs use the defaults; a run that leaves a param out gets its default. `RunJobRequest.on_behalf_of` names who asked the host, and becomes the run's `triggered_by`.
- **Disabled jobs:** a job's `disabled_reason`, when set, says why it can't run now (worked out by the plugin as things change). Starting it answers `FAILED_PRECONDITION`, reason `JOB_DISABLED` (metadata `job_id`), and its schedule doesn't fire: `next_run_at` is unset while `schedule.enabled` stays as configured. Hosts show the reason.
- Cancelling a run that isn't running, or of a job that isn't `cancellable`, answers `FAILED_PRECONDITION`. Unknown jobs and runs answer `NOT_FOUND`.

## Restarts and network profiles

- **`HealthResponse.instance_id`** changes whenever the plugin starts and stays the same while it runs. A host that sees a new value pushes the plugin's configuration and network profiles again. The Go SDK fills it in for every plugin.
- **`GetNetworkProfile(source_id)`** (optional) says which profile a source uses now and where it comes from: `PUSHED` (by a host), `PLUGIN` (the plugin's own configuration) or `NONE` (direct). Proxy passwords come back empty. A pushed profile wins over the plugin's own; removing it (`SetNetworkProfile` without a profile) falls back to the plugin's.

## Errors

Errors are gRPC status codes. When the upstream source is the problem, the status carries a `google.rpc.ErrorInfo` with domain `source.panelarr` and a reason from `ErrorReason` without its prefix: `CLOUDFLARE_CHALLENGE`, `RATE_LIMITED` (with a `google.rpc.RetryInfo`), `SOURCE_DOWN`, `NOT_FOUND`, `LOGIN_REQUIRED`, `UNSUPPORTED`, for packages `STORE_FETCH_FAILED` and `STORE_INVALID`, and for jobs `ALREADY_RUNNING` and `JOB_DISABLED`.

## Wire

gRPC is the canonical wire: every plugin must speak it, and hosts call plugins over it. The `google.api.http` annotations define an HTTP/JSON mapping ([OpenAPI](../gen/openapi/source.openapi.yaml)) that plugins may also serve.

## Compatibility

The package is versioned (`v1`). Changes within it are backwards compatible only: fields and RPCs are added, never removed, renumbered or retyped. `make breaking` enforces this against `main`.
