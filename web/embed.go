// Package web embeds the static front-end into the binary.
package web

import "embed"

//go:embed static
var Static embed.FS
