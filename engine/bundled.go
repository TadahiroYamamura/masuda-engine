package engine

import (
	"embed"
	"io/fs"
)

// defaults lives under engine/ rather than at the module root because
// go:embed cannot reach outside the package directory.
//
//go:embed defaults
var defaults embed.FS

func bundledFS() fs.FS {
	sub, err := fs.Sub(defaults, "defaults")
	if err != nil {
		panic(err)
	}
	return sub
}
