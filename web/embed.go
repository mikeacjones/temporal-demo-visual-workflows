// Package web embeds the built builder UI (npm run build) into the app binary.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:build
var build embed.FS

// Build is the static site, rooted at the build output.
func Build() fs.FS {
	sub, err := fs.Sub(build, "build")
	if err != nil {
		panic(err)
	}
	return sub
}
