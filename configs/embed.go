// Package configs embeds built-in defaults.
package configs

import _ "embed"

//go:embed defaults.yaml
var Defaults []byte

// Sources is the built-in sources + mirrors catalog (sources.yaml format, docs/sources.md).
//
//go:embed sources.yaml
var Sources []byte
