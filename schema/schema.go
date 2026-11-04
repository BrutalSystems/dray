// Package schema embeds the published JSON Schemas for Dray's config files.
// They are generated from internal/config by its tests:
//
//	go test ./internal/config -run TestSchemasUpToDate -update
package schema

import _ "embed"

// Config is the JSON Schema for .dray/config.yaml.
//
//go:embed config.schema.json
var Config []byte

// Registry is the JSON Schema for a registry entry.
//
//go:embed registry.schema.json
var Registry []byte
