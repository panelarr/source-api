// SPDX-License-Identifier: Apache-2.0

// Package openapi embeds the OpenAPI document generated from the source API
// protos: the HTTP/JSON mapping of panelarr.source.v1.
package openapi

import _ "embed"

// Source is the OpenAPI 3.1 document of panelarr.source.v1 (YAML).
//
//go:embed source.openapi.yaml
var Source []byte
