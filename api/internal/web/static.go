package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var dist embed.FS

// Static returns the built React app (the contents of dist/).
func Static() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		panic(err) // dist is embedded at compile time, so this cannot fail
	}
	return sub
}
