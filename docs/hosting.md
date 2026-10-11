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
3. `ListSources` (again whenever `DYNAMIC_SOURCES` is declared), then `SetNetworkProfile` per source for plugins that declare the network profile common service.
4. Browse, search and fetch. Respect `limits` and `RATE_LIMITED` retry hints.

## Errors

Errors come back as `*plugin.Error` with the gRPC code, the source error reason (`plugin.ErrorReason(err)`: `SOURCE_DOWN`, `RATE_LIMITED`, `CLOUDFLARE_CHALLENGE`, `NOT_FOUND`, `LOGIN_REQUIRED`, `UNSUPPORTED`) and any retry hint.

## A fixture for host tests

[`plugintest.New()`](../sdk/plugintest/fake.go) is a made-up plugin with two sources, series, chapters, pages and images. Serve it with `client.InProcess`, or with `plugin.NewHandler(fake, key)` on an `httptest` server for a remote plugin. Set `fake.Config` to test the configuration flow, and read `fake.Profile(id)` and `fake.Calls()` to check what the host sent.

## Images

`client.ReadImage(ctx, c, req, maxBytes)` reads a whole image; `FetchImage` streams it.
