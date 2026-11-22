// Package configs embeds built-in defaults.
package configs

import _ "embed"

//go:embed defaults.yaml
var Defaults []byte
