# Writing a source plugin in Go

A source plugin is a program that implements `panelarr.source.v1`. With the SDK it's a type that implements [`plugin.Plugin`](../sdk/plugin/plugin.go) and a `main` that calls `plugin.Main`. [`sdk/examples/static-source`](../sdk/examples/static-source/main.go) is a complete example.

```go
type myPlugin struct {
	plugin.Unimplemented          // "not supported" for everything you leave out
	net *httpx.Sources            // one HTTP client per source, following its network profile
}

func main() {
	p := &myPlugin{net: httpx.NewSources(httpx.Options{UserAgent: "my-plugin/1.0"})}
	plugin.Main(p, plugin.WithDefaultListen(":8905"))
}
```

## Run modes

The same binary runs both ways:

- **Managed:** the host starts it as a child process and talks gRPC to it over a local socket (HashiCorp go-plugin). `plugin.Main` notices the handshake and ignores `--listen`.
- **Standalone:** `my-plugin --listen :8905 --api-key KEY [--config config.json]` serves gRPC and Connect on that port (HTTP/1.1 and cleartext HTTP/2). Every call needs `Authorization: Bearer KEY` except `Health` and `GET /healthz`. The flags can also come from `PLUGIN_LISTEN`, `PLUGIN_API_KEY` and `PLUGIN_CONFIG`.

Standalone plugins use ports from 8903 up. `plugin.WithRoutes(plugin.Route{Pattern: "GET /metrics", Handler: h})` adds HTTP routes next to the API (behind the API key unless `Public`), e.g. metrics or icons.

## What to implement

| Call | Notes |
|---|---|
| `GetInfo` | id (`^[a-z0-9][a-z0-9._-]*$`), name, version, `contracts: ["panelarr.source.v1"]`, capabilities, **honest limits** (concurrency and rate per source for API calls, and separately for `FetchImage`: hosts schedule against them; leave the image rate unset if the plugin paces images itself), `config_schema`, common services |
| `ListSources`, `GetSource` | stable source IDs, language, capabilities, health if you check sources |
| `Popular`, `Latest`, `Search` | 1-based pages and `has_next_page`. Hosts match series across sources by searching for titles and alternative titles, so **search by title must find the series** |
| `GetFilters` | typed filters; values come back addressed by position |
| `ResolveUrl` | turn a pasted web URL into a series; set `best_effort` when guessing |
| `GetSeries`, `ListChapters`, `ListPages` | keys are opaque to hosts; chapters newest first; pages in reading order from 0 |
| `FetchImage` | stream with `plugin.SendImage(send, contentType, size, reader)` |
| `GetConfig`, `SetConfig` | embed a `*plugin.PluginConfig` (see below) if you need configuration |
| settings calls | per-source settings as JSON Schema plus values |
| `SetNetworkProfile` | pass straight to `httpx.Sources.SetNetworkProfile` |
| `GetNetworkProfile` | optional: the profile a source uses and its origin, proxy password left out |
| `Health` | the SDK adds `instance_id` (a random value per process start) unless you set your own |

## Packages

A plugin that installs things that provide sources (extensions) declares `PLUGIN_CAPABILITY_PACKAGES` and implements the package calls (see [protos.md](protos.md#packages-and-languages)). Keep install and update idempotent and all-or-nothing, set `Source.package_id` on listed sources, list only sources whose language is enabled at the plugin's and the package's level, and answer `NOT_FOUND` for the others. If the plugin's configuration has a languages field, say in its description that `"all"`/`"multi"` must be listed to keep multi-language sources. `plugintest.New()` shows a complete fake.

## Errors

Return the SDK's errors so hosts see the right code and reason:

| Helper | Code | Reason |
|---|---|---|
| `plugin.ErrNotFound` | NOT_FOUND | `NOT_FOUND` |
| `plugin.ErrUnsupported` | UNIMPLEMENTED | `UNSUPPORTED` (e.g. `Latest` without the capability) |
| `plugin.ErrSourceDown` | UNAVAILABLE | `SOURCE_DOWN` |
| `plugin.ErrChallenge` | UNAVAILABLE | `CLOUDFLARE_CHALLENGE` |
| `plugin.ErrRateLimited(retryAfter, …)` | RESOURCE_EXHAUSTED | `RATE_LIMITED`, with the retry hint |
| `plugin.ErrLoginRequired` | UNAUTHENTICATED | `LOGIN_REQUIRED` |
| `plugin.ErrStoreFetchFailed` | UNAVAILABLE | `STORE_FETCH_FAILED` (a package's origin couldn't be downloaded) |
| `plugin.ErrStoreInvalid` | FAILED_PRECONDITION | `STORE_INVALID` (a package's origin can't be used) |
| `plugin.ErrInvalidArgument` | INVALID_ARGUMENT | — |

`httpx.StatusError(resp)` and `httpx.Wrap(err)` turn HTTP responses and transport errors into these.

## Configuration

Plugin-level configuration (an upstream URL, an API key) is a JSON Schema the plugin declares in `GetInfo.config_schema`; the host renders a form and sends the values with `SetConfig`.

```go
schema, _ := settings.Parse(`{"type": "object", "properties": {
  "url": {"type": "string", "format": "uri"},
  "key": {"type": "string", "writeOnly": true}}, "required": ["url"]}`)
cfg := plugin.NewPluginConfig(schema, func(ctx context.Context, v map[string]any) error {
	return p.reconnect(v["url"].(string), v["key"].(string)) // an error rejects the change
})
```

`settings.Parse` stamps the order the properties are written in onto each of them as `"x-order": 1, 2, …`, at every depth (nested objects and array items included), because the schema's key order is lost on its way to the host's form. Set `x-order` (or `propertyOrder`) yourself to override it for a property.

Embed `*plugin.PluginConfig` in the plugin to answer `GetConfig` and `SetConfig`, and put `cfg.SchemaStruct()` in `config_schema`. Properties marked `writeOnly` are secrets: they never come back from `GetConfig`, and an empty value keeps the current one.

## Networking

`httpx.Sources` keeps one HTTP client per source that follows the profile the host pushed for it: proxy (http, https, socks5 with credentials), challenge solver (FlareSolverr/Byparr), rate limit (per source or per host), concurrency, user agent, headers, DNS over HTTPS (for direct connections), shared cookie jars and timeouts. Use `net.HTTP(sourceID)` for every request of that source. `SetDefault` sets the profile for sources the host pushed none for.

## Testing

Run the conformance suite from your tests:

```go
func TestConformance(t *testing.T) {
	conformance.Run(t, client.InProcess(newPlugin()), conformance.Options{})
}
```

See [conformance.md](conformance.md).
