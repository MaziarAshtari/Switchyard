// Package web embeds the built GUI (web/dist, produced by `pnpm build`).
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// FS returns the built GUI files. Without a build it only contains .gitkeep.
func FS() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // unreachable: "dist" is embedded above
	}
	return sub
}
