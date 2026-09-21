// Package bridgescript embeds the OBS-side Lua bridge script.
//
// agentic-obs-bridge.lua is the canonical copy -- OBS loads this exact file,
// and this package is how Go code reaches the same bytes without a second
// copy to drift out of sync. go:embed cannot reach outside its own package,
// which is the only reason this package exists.
package bridgescript

import _ "embed"

//go:embed agentic-obs-bridge.lua
var Script []byte
