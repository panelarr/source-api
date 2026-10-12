# Hosting source plugins

A host (the Panelarr server, or any program) talks to plugins through [`sdk/client`](../sdk/client/client.go). Every way of reaching a plugin gives the same `client.Client`:

| Constructor | For |
|---|---|
| `client.Managed(path, opts)` | a plugin binary the host starts itself (go-plugin, gRPC on a local socket); `Close` stops it |
| `client.Remote(url, client.RemoteOptions{APIKey: key})` | a plugin running as a service, in any language; speaks gRPC (`Connect: true` for the Connect protocol) |
| `client.GRPC(conn)` | a gRPC connection the host dialled itself |
| `client.InProcess(p)` | a plugin in the same process, for tests |

## Lifecycle

1. `GetInfo`: identity, capabilities, limits, `config_schema` and the common services it can use.
2. `SetConfig` with the configuration the admin entered (render `config_schema` as a form). Call it again whenever it changes.
3. Watch `Health().instance_id`: when it changes, the plugin restarted, so repeat step 2 and the profile pushes below.
4. `ListSources` (again whenever `DYNAMIC_SOURCES` is declared), then `SetNetworkProfile` per source for plugins that declare the network profile common service.
5. For plugins with `PACKAGES`: list, install, update, reload and remove packages, set each one's languages, and show their origins (`ListOrigins`, `RefreshOrigin`) and installed details. For plugins with `HEALTH_CHECKS`: show `Source.health` and offer `SetSourceHealthCheck` as a per-source switch. Install and update can take minutes; they're idempotent, so retrying is safe. Re-list sources after a change (`Source.package_id` says which package each comes from).
6. For plugins with `JOBS`: show their jobs next to your own, start them with `RunJob` (pass `on_behalf_of` with your user, so the plugin's history names them) and follow the run with `GetJobRun`. `ALREADY_RUNNING` errors carry the running run's ID. Show a job's `disabled_reason` and don't offer "Run now" while it's set (a start would answer `JOB_DISABLED`).
7. Browse, search and fetch. `GetNetworkProfile(source)` shows which profile a source really uses (pushed, the plugin's own, or none). Respect `limits` and `RATE_LIMITED` retry hints: `default_rate_limit` and `max_concurrency_per_source` pace the API calls; `image_rate_limit` and `max_image_concurrency_per_source` pace `FetchImage` alone. No image rate limit means the plugin paces images itself, so don't add your own. That never means unthrottled when you push a network profile with a rate limit: the plugin applies the profile's limit to all of the source's requests, images included.

## Errors

Errors come back as `*plugin.Error` with the gRPC code, the source error reason (`plugin.ErrorReason(err)`: `SOURCE_DOWN`, `RATE_LIMITED`, `CLOUDFLARE_CHALLENGE`, `NOT_FOUND`, `LOGIN_REQUIRED`, `UNSUPPORTED`) and any retry hint.

## A fixture for host tests

[`plugintest.New()`](../sdk/plugintest/fake.go) is a made-up plugin with two sources, series, chapters, pages and images, and packages: the two sources' packages (one with an update) and a multi-language package to install. Its origins are `store-a` and `store-b`; `fake.BreakOrigin(id, true)` makes refreshing one fail, and `fake.DisableJob(id, reason)` disables a job. It has two jobs: `refresh` finishes at once, and `hold` runs until it's cancelled or `fake.Release()` is called. Set `fake.Langs` for the plugin-level languages, `fake.Packages = nil` for a plugin without packages, or `fake.Jobs = nil` for a plugin without jobs. Serve it with `client.InProcess`, or with `plugin.NewHandler(fake, key)` on an `httptest` server for a remote plugin. Set `fake.Config` to test the configuration flow, and read `fake.Profile(id)` and `fake.Calls()` to check what the host sent.

## Images

`client.ReadImage(ctx, c, req, maxBytes)` reads a whole image; `FetchImage` streams it.
