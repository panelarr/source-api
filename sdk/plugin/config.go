// SPDX-License-Identifier: Apache-2.0

package plugin

import (
	"context"
	"maps"
	"sync"

	"google.golang.org/protobuf/types/known/structpb"

	sourcev1 "github.com/panelarr/source-api/gen/panelarr/source/v1"
	"github.com/panelarr/source-api/sdk/settings"
)

// PluginConfig holds a plugin's plugin-level configuration, described by a JSON
// Schema, and implements the GetConfig and SetConfig calls. Embed it in a
// plugin (it then answers those calls) and put [PluginConfig.SchemaStruct] in
// PluginInfo.config_schema.
//
// Properties the schema marks "writeOnly" (secrets) are never returned, and
// an empty value for one keeps the current value, so a configuration read
// with GetConfig can be sent back unchanged.
type PluginConfig struct {
	schema *settings.Schema
	apply  func(ctx context.Context, values map[string]any) error

	mu     sync.RWMutex
	values map[string]any
}

// NewPluginConfig returns a configuration with the schema's defaults. apply is
// called with the full new configuration (defaults filled in) before a
// change takes effect; an error rejects the change. It may be nil.
func NewPluginConfig(schema *settings.Schema, apply func(ctx context.Context, values map[string]any) error) *PluginConfig {
	return &PluginConfig{schema: schema, apply: apply, values: schema.WithDefaults(nil)}
}

// Schema returns the configuration schema.
func (c *PluginConfig) Schema() *settings.Schema { return c.schema }

// SchemaStruct returns the schema for PluginInfo.config_schema.
func (c *PluginConfig) SchemaStruct() *structpb.Struct { return c.schema.Struct() }

// Values returns a copy of the configuration in effect, secrets included.
func (c *PluginConfig) Values() map[string]any {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return maps.Clone(c.values)
}

// Set validates values and makes them the configuration.
func (c *PluginConfig) Set(ctx context.Context, values map[string]any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	next := maps.Clone(values)
	if next == nil {
		next = map[string]any{}
	}
	for _, name := range c.schema.WriteOnly() {
		if v, ok := next[name]; !ok || v == "" || v == nil {
			if cur, set := c.values[name]; set {
				next[name] = cur
			} else {
				delete(next, name)
			}
		}
	}
	next = c.schema.WithDefaults(next)
	if err := c.schema.Validate(next); err != nil {
		return ErrInvalidArgument("%v", err)
	}
	if c.apply != nil {
		if err := c.apply(ctx, maps.Clone(next)); err != nil {
			return err
		}
	}
	c.values = next
	return nil
}

// redacted is the configuration without its secrets.
func (c *PluginConfig) redacted() (*structpb.Struct, error) {
	out := c.Values()
	for _, name := range c.schema.WriteOnly() {
		if _, set := out[name]; set {
			out[name] = ""
		}
	}
	return structpb.NewStruct(out)
}

// GetConfig implements the RPC.
func (c *PluginConfig) GetConfig(context.Context, *sourcev1.GetConfigRequest) (*sourcev1.GetConfigResponse, error) {
	values, err := c.redacted()
	if err != nil {
		return nil, err
	}
	return &sourcev1.GetConfigResponse{Values: values}, nil
}

// SetConfig implements the RPC.
func (c *PluginConfig) SetConfig(ctx context.Context, req *sourcev1.SetConfigRequest) (*sourcev1.SetConfigResponse, error) {
	if err := c.Set(ctx, req.GetValues().AsMap()); err != nil {
		return nil, err
	}
	values, err := c.redacted()
	if err != nil {
		return nil, err
	}
	return &sourcev1.SetConfigResponse{Values: values}, nil
}
