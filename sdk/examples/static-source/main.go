// SPDX-License-Identifier: Apache-2.0

// Command static-source is a minimal source plugin that serves two made-up
// sources. It shows how a plugin is put together with the SDK, and the
// SDK's tests run it in managed and standalone mode.
//
//	go run ./sdk/examples/static-source --listen :8904
package main

import (
	"github.com/panelarr/source-api/sdk/plugin"
	"github.com/panelarr/source-api/sdk/plugintest"
	"github.com/panelarr/source-api/sdk/settings"
)

// configSchema is what the plugin asks its host for. The token is a secret:
// hosts never read it back.
const configSchema = `{
  "type": "object",
  "properties": {
    "greeting": {"type": "string", "title": "Greeting", "default": "hello"},
    "token": {"type": "string", "title": "Token", "writeOnly": true}
  },
  "additionalProperties": false
}`

func main() {
	schema, err := settings.Parse(configSchema)
	if err != nil {
		panic(err)
	}
	p := plugintest.New()
	p.ID = "static-source"
	p.Config = plugin.NewPluginConfig(schema, nil)
	plugin.Main(p, plugin.WithDefaultListen(":8904"))
}
