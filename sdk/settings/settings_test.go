// SPDX-License-Identifier: Apache-2.0

package settings_test

import (
	"testing"

	"github.com/panelarr/source-api/sdk/settings"
)

const doc = `{
  "type": "object",
  "properties": {
    "variant": {"type": "string", "enum": ["default", "full"], "default": "default"},
    "batch": {"type": "integer", "minimum": 1, "default": 500}
  },
  "additionalProperties": false
}`

func TestValidateAndDefaults(t *testing.T) {
	s, err := settings.Parse(doc)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Validate(map[string]any{"variant": "full", "batch": float64(10)}); err != nil {
		t.Fatalf("valid values rejected: %v", err)
	}
	for _, bad := range []map[string]any{
		{"variant": "huge"},
		{"batch": float64(0)},
		{"unknown": true},
	} {
		if err := s.Validate(bad); err == nil {
			t.Errorf("%v accepted", bad)
		}
	}
	got := s.WithDefaults(map[string]any{"batch": float64(3)})
	if got["variant"] != "default" || got["batch"] != float64(3) {
		t.Fatalf("defaults: %v", got)
	}
	if _, err := settings.FromStruct(s.Struct()); err != nil {
		t.Fatalf("round trip: %v", err)
	}
}

func TestInvalidSchema(t *testing.T) {
	if _, err := settings.Parse(`{"type": 12}`); err == nil {
		t.Fatal("invalid schema accepted")
	}
	if _, err := settings.Parse(`not json`); err == nil {
		t.Fatal("non-JSON accepted")
	}
}

func TestWriteOnly(t *testing.T) {
	s, err := settings.Parse(`{"type": "object", "properties": {
	  "url": {"type": "string"},
	  "key": {"type": "string", "writeOnly": true},
	  "token": {"type": "string", "writeOnly": true}}}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.WriteOnly(); len(got) != 2 || got[0] != "key" || got[1] != "token" {
		t.Fatalf("write-only: %v", got)
	}
}

func TestParseStampsTheWrittenOrder(t *testing.T) {
	s, err := settings.Parse(`{
	  "type": "object",
	  "properties": {
	    "zeta": {"type": "string"},
	    "alpha": {"type": "string", "x-order": 9},
	    "mid": {
	      "type": "object",
	      "properties": {"second": {"type": "integer"}, "first": {"type": "integer", "propertyOrder": 4}}
	    },
	    "list": {
	      "type": "array",
	      "items": {"type": "object", "properties": {"url": {"type": "string"}, "key": {"type": "string"}}}
	    }
	  }
	}`)
	if err != nil {
		t.Fatal(err)
	}
	// Through a protobuf Struct, as a host gets it.
	back, err := settings.FromStruct(s.Struct())
	if err != nil {
		t.Fatal(err)
	}
	props := back.Doc()["properties"].(map[string]any)
	order := func(m map[string]any, name string) any { return m[name].(map[string]any)["x-order"] }
	if order(props, "zeta") != float64(1) || order(props, "alpha") != float64(9) || order(props, "mid") != float64(3) || order(props, "list") != float64(4) {
		t.Fatalf("top level: %v", props)
	}
	mid := props["mid"].(map[string]any)["properties"].(map[string]any)
	if order(mid, "second") != float64(1) || order(mid, "first") != nil || mid["first"].(map[string]any)["propertyOrder"] != float64(4) {
		t.Fatalf("nested: %v", mid)
	}
	items := props["list"].(map[string]any)["items"].(map[string]any)["properties"].(map[string]any)
	if order(items, "url") != float64(1) || order(items, "key") != float64(2) {
		t.Fatalf("array items: %v", items)
	}
	// The stamped schema still validates as before.
	if err := back.Validate(map[string]any{"zeta": "z", "mid": map[string]any{"second": 2}, "list": []any{map[string]any{"url": "u"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := settings.Parse(`{"type": "object"} {}`); err == nil {
		t.Fatal("trailing data accepted")
	}
}

func TestParseRefusesMalformedPropertiesCleanly(t *testing.T) {
	for _, doc := range []string{`{"type": "object", "properties": true}`, `{"properties": [1, 2]}`, `{"properties": {"a": 3}}`} {
		if _, err := settings.Parse(doc); err == nil {
			t.Errorf("%s: accepted", doc)
		}
	}
}
