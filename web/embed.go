// Package web embeds the LoRaHub console (a dependency-free SPA).
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var files embed.FS

// FS returns the static file system rooted at static/.
func FS() fs.FS {
	f, _ := fs.Sub(files, "static")
	return f
}
